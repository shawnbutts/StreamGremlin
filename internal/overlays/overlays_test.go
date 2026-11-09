package overlays

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

func newStore(t *testing.T) (*Store, *storage.DB, *secrets.Store) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	sec, err := secrets.Open(ctx, db, secrets.Options{DataDir: dir, Want: secrets.KindFile})
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db, sec), db, sec
}

var now = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// The database keeps only the token's hash; the link comes back from the
// secret store; a new link stops the old one at once; deleting forgets it.
func TestLinksAreSecretAndReplaceable(t *testing.T) {
	ctx := context.Background()
	s, db, sec := newStore(t)
	o, token, err := s.Create(ctx, "alerts", KindAlert, DefaultLook(), now)
	if err != nil || o.Name != "alerts" || o.Look.Position != "top" || len(token) < 40 {
		t.Fatal(o, token, err)
	}
	var stored string
	_ = db.QueryRowContext(ctx, "SELECT token_hash FROM overlays").Scan(&stored)
	if stored == token || strings.Contains(stored, token) {
		t.Fatal("the token itself is in the database")
	}
	if got, err := s.Link(ctx, "alerts"); err != nil || got != token {
		t.Fatal(got, err)
	}
	if got, err := s.ByToken(ctx, token); err != nil || got.Name != "alerts" {
		t.Fatal(got, err)
	}
	if _, _, err := s.Create(ctx, "alerts", KindAlert, DefaultLook(), now); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	fresh, err := s.NewLink(ctx, "alerts", now)
	if err != nil || fresh == token {
		t.Fatal(err)
	}
	if _, err := s.ByToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("the old link still works")
	}
	if got, _ := s.Link(ctx, "alerts"); got != fresh {
		t.Fatal(got)
	}
	// A link that isn't saved here (a backup restored elsewhere) says so.
	_ = sec.Delete(ctx, "overlay/alerts")
	if _, err := s.Link(ctx, "alerts"); !errors.Is(err, ErrNoLink) {
		t.Fatal(err)
	}
	if ok, err := s.Delete(ctx, "alerts"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err := s.ByToken(ctx, fresh); !errors.Is(err, ErrNotFound) {
		t.Fatal("a deleted overlay's link works")
	}
	if _, err := s.Get(ctx, "alerts"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestLooksAndNamesAreChecked(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStore(t)
	bad := []func(*Look){
		func(l *Look) { l.Position = "left" },
		func(l *Look) { l.Animation = "spin" },
		func(l *Look) { l.TextColor = "red" },
		func(l *Look) { l.Background = "#12345" },
		func(l *Look) { l.Opacity = 101 },
		func(l *Look) { l.Seconds = 1 },
		func(l *Look) { l.Seconds = 61 },
	}
	for i, change := range bad {
		l := DefaultLook()
		change(&l)
		if _, _, err := s.Create(ctx, "x", KindAlert, l, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%d: %v", i, err)
		}
	}
	for _, name := range []string{"", "Alerts", "a b", "-x", strings.Repeat("a", 65)} {
		if _, _, err := s.Create(ctx, name, KindAlert, DefaultLook(), now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, _, err := s.Create(ctx, "w", "wheel", DefaultLook(), now); !errors.Is(err, ErrInvalid) {
		t.Error(err)
	}
	if _, _, err := s.Create(ctx, "a", KindAlert, DefaultLook(), now); err != nil {
		t.Fatal(err)
	}
	l := DefaultLook()
	l.Position, l.Animation, l.Opacity = "bottom-left", "pop", 40
	o, err := s.SetLook(ctx, "a", l, now.Add(time.Minute))
	if err != nil || o.Look != l || !o.UpdatedAt.After(o.CreatedAt) {
		t.Fatal(o, err)
	}
	if _, err := s.SetLook(ctx, "nope", l, now); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// fakePage plays a page in OBS: it reports alerts as started, and as done
// when told to finish them.
type fakePage struct {
	h      *Hub
	c      *conn
	mu     sync.Mutex
	alerts []Alert
	got    chan Alert
}

func open(h *Hub, name string, preview, answer bool) *fakePage {
	p := &fakePage{h: h, c: h.join(Overlay{Name: name, Look: DefaultLook()}, preview), got: make(chan Alert, 32)}
	go func() {
		for {
			select {
			case b := <-p.c.send:
				var m struct {
					Type string
					Body struct{ Alert Alert }
				}
				_ = json.Unmarshal(b, &m)
				if m.Type != "alert" {
					continue
				}
				p.mu.Lock()
				p.alerts = append(p.alerts, m.Body.Alert)
				p.mu.Unlock()
				if answer {
					h.ack(p.c, m.Body.Alert.ID, "started", "")
				}
				p.got <- m.Body.Alert
			case <-p.c.kick:
				return
			}
		}
	}()
	return p
}

func (p *fakePage) done(id string) { p.h.ack(p.c, id, "done", "") }

func TestShowWaitsForAPageAndSaysHowItWent(t *testing.T) {
	ctx := context.Background()
	h := NewHub(nil)
	if _, err := h.Show(ctx, "alerts", Alert{Text: "hi", Seconds: 2}, ""); !errors.Is(err, ErrNotOpen) {
		t.Fatal("shown with no page open:", err)
	}
	// A preview alone is never an audience.
	pv := open(h, "alerts", true, true)
	if _, err := h.Show(ctx, "alerts", Alert{Text: "hi", Seconds: 2}, ""); !errors.Is(err, ErrNotOpen) {
		t.Fatal("a preview counted as OBS:", err)
	}
	p := open(h, "alerts", false, true)
	res, err := h.Show(ctx, "alerts", Alert{Text: "Fan followed", Seconds: 2}, "job/1")
	if err != nil || !res.Shown || res.Pages != 1 || res.Previews != 1 {
		t.Fatal(res, err)
	}
	a := <-p.got
	<-pv.got // the preview sees it too
	p.done(a.ID)
	// A retried step doesn't show it again.
	res, err = h.Show(ctx, "alerts", Alert{Text: "Fan followed", Seconds: 2}, "job/1")
	if err != nil || !res.Repeated || res.Shown {
		t.Fatal(res, err)
	}
	if pages, previews := h.Open("alerts"); pages != 1 || previews != 1 {
		t.Fatal(pages, previews)
	}
}

func TestAPageThatNeverAnswersIsUnknownNotShown(t *testing.T) {
	h := NewHub(nil)
	open(h, "alerts", false, false)
	start := time.Now()
	res, err := h.Show(context.Background(), "alerts", Alert{Text: "hi", Seconds: 2}, "k")
	if !errors.Is(err, ErrNoAnswer) || res.Shown || time.Since(start) < AnswerWait-100*time.Millisecond {
		t.Fatal(res, err)
	}
	// It may have shown: a retry doesn't show it again.
	if res, _ := h.Show(context.Background(), "alerts", Alert{Text: "hi", Seconds: 2}, "k"); !res.Repeated {
		t.Fatal(res)
	}
}

func TestAPageThatFailsSaysWhy(t *testing.T) {
	h := NewHub(nil)
	p := &fakePage{h: h, c: h.join(Overlay{Name: "a"}, false)}
	go func() {
		b := <-p.c.send // hello
		_ = b
		b = <-p.c.send
		var m struct{ Body struct{ Alert Alert } }
		_ = json.Unmarshal(b, &m)
		h.ack(p.c, m.Body.Alert.ID, "error", "NotAllowedError")
	}()
	_, err := h.Show(context.Background(), "a", Alert{Text: "x", Seconds: 2}, "k")
	if err == nil || !strings.Contains(err.Error(), "NotAllowedError") {
		t.Fatal(err)
	}
}

// Alerts on one overlay take turns: the next starts when the one before
// leaves the screen.
func TestAlertsTakeTurns(t *testing.T) {
	ctx := context.Background()
	h := NewHub(nil)
	p := open(h, "alerts", false, true)
	if _, err := h.Show(ctx, "alerts", Alert{ID: "one", Text: "1", Seconds: 30}, ""); err != nil {
		t.Fatal(err)
	}
	<-p.got
	second := make(chan error, 1)
	go func() {
		_, err := h.Show(ctx, "alerts", Alert{ID: "two", Text: "2", Seconds: 30}, "")
		second <- err
	}()
	select {
	case a := <-p.got:
		t.Fatal("shown while another was on screen:", a)
	case <-time.After(150 * time.Millisecond):
	}
	p.done("one")
	if a := <-p.got; a.ID != "two" {
		t.Fatal(a)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	// A waiting alert that's cancelled gives up its place.
	cctx, cancel := context.WithCancel(ctx)
	gaveUp := make(chan error, 1)
	go func() {
		_, err := h.Show(cctx, "alerts", Alert{ID: "three", Text: "3", Seconds: 30}, "")
		gaveUp <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-gaveUp; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.done("two")
	if _, err := h.Show(ctx, "alerts", Alert{ID: "four", Text: "4", Seconds: 30}, ""); err != nil {
		t.Fatal(err)
	}
	if a := <-p.got; a.ID != "four" {
		t.Fatal("the cancelled alert was shown:", a)
	}
}

func TestPreviewsOnlyGetTests(t *testing.T) {
	h := NewHub(nil)
	p := open(h, "alerts", false, true)
	pv := open(h, "alerts", true, false)
	res := h.Preview("alerts", Alert{Text: "test", Seconds: 2})
	if res.Previews != 1 || !res.Test || res.Shown {
		t.Fatal(res)
	}
	<-pv.got
	select {
	case a := <-p.got:
		t.Fatal("OBS saw a test:", a)
	case <-time.After(100 * time.Millisecond):
	}
}

// A page that opens while an alert is on screen gets it, with the time left.
func TestAPageThatOpensLateGetsTheAlertOnScreen(t *testing.T) {
	h := NewHub(nil)
	open(h, "alerts", false, true)
	if _, err := h.Show(context.Background(), "alerts", Alert{ID: "on", Text: "x", Seconds: 30}, ""); err != nil {
		t.Fatal(err)
	}
	late := open(h, "alerts", false, true)
	if a := <-late.got; a.ID != "on" {
		t.Fatal(a)
	}
}

func TestDisconnectClosesPages(t *testing.T) {
	h := NewHub(nil)
	p := open(h, "alerts", false, true)
	h.Disconnect("alerts")
	select {
	case <-p.c.kick:
	case <-time.After(time.Second):
		t.Fatal("not disconnected")
	}
	if pages, _ := h.Open("alerts"); pages != 0 {
		t.Fatal(pages)
	}
}

func serve(t *testing.T) (*Store, *Hub, *httptest.Server) {
	t.Helper()
	s, _, _ := newStore(t)
	h := NewHub(nil)
	srv := httptest.NewServer(NewServer(s, h, nil))
	t.Cleanup(srv.Close)
	return s, h, srv
}

func TestThePageIsServedSafely(t *testing.T) {
	s, _, srv := serve(t)
	_, token, err := s.Create(context.Background(), "alerts", KindAlert, DefaultLook(), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"", "overlay.js", "overlay.css"} {
		res, err := http.Get(srv.URL + "/o/" + token + "/" + f)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		csp := res.Header.Get("Content-Security-Policy")
		if res.StatusCode != 200 || !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors wails://localhost") ||
			res.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%q: %d %q", f, res.StatusCode, csp)
		}
	}
	// A wrong link shows nothing at all: OBS would show it to viewers.
	res, err := http.Get(srv.URL + "/o/not-a-token/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 404 || len(b) != 0 {
		t.Fatalf("%d %q", res.StatusCode, b)
	}
	// Another host name pointed at this computer is refused.
	req, _ := http.NewRequest("GET", srv.URL+"/o/"+token+"/", nil)
	req.Host = "evil.example:80"
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatal(res.StatusCode)
	}
}

// Through a real WebSocket: hello with the look, the alert, the page's
// answer; another site's page can't connect; a new link disconnects.
func TestAPageConnects(t *testing.T) {
	ctx := context.Background()
	s, h, srv := serve(t)
	_, token, _ := s.Create(ctx, "alerts", KindAlert, DefaultLook(), now)
	ws := "ws" + strings.TrimPrefix(srv.URL, "http") + "/o/" + token + "/ws"

	if c, err := dial(ctx, ws, http.Header{"Origin": {"https://evil.example"}}); err == nil {
		_ = c.CloseNow()
		t.Fatal("another site's page connected")
	}
	c, err := dial(ctx, ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	read := func() map[string]any {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, b, err := c.Read(rctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	hello := read()
	if hello["type"] != "hello" || !strings.Contains(mustJSON(hello), `"position":"top"`) {
		t.Fatal(hello)
	}
	shown := make(chan Result, 1)
	go func() {
		res, err := h.Show(ctx, "alerts", Alert{Text: "<b>Fan</b> followed", Seconds: 2}, "")
		if err != nil {
			t.Error(err)
		}
		shown <- res
	}()
	m := read()
	alert := m["body"].(map[string]any)["alert"].(map[string]any)
	if alert["text"] != "<b>Fan</b> followed" {
		t.Fatal(m)
	}
	ack, _ := json.Marshal(map[string]string{"type": "ack", "id": alert["id"].(string), "status": "started"})
	if err := c.Write(ctx, websocket.MessageText, ack); err != nil {
		t.Fatal(err)
	}
	if res := <-shown; !res.Shown || res.Pages != 1 {
		t.Fatal(res)
	}
	if _, err := s.NewLink(ctx, "alerts", now); err != nil {
		t.Fatal(err)
	}
	h.Disconnect("alerts")
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, _, err := c.Read(rctx); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatal("still connected after a new link:", err)
	}
}

func dial(ctx context.Context, url string, h http.Header) (*websocket.Conn, error) {
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{Subprotocol}, HTTPHeader: h})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return c, err
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
