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
	"code.streamgremlin.com/streamgremlin/internal/sounds"
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
	more := []func(*Look){
		func(l *Look) { l.Background2 = "blue" },
		func(l *Look) { l.GradientAngle = 360 },
		func(l *Look) { l.Radius = 49 },
		func(l *Look) { l.BorderWidth = 13 },
		func(l *Look) { l.BorderColor = "" },
		func(l *Look) { l.Shadow = -1 },
		func(l *Look) { l.TextShadow = 101 },
		func(l *Look) { l.Font = "papyrus" },
		func(l *Look) { l.Font, l.FontName = "custom", "" },
		func(l *Look) { l.Font, l.FontName = "custom", `Arial"; } body { color: red` },
		func(l *Look) { l.Font, l.FontName = "custom", "a\\b" },
		func(l *Look) { l.TextSize = 49 },
		func(l *Look) { l.TextOutline = 7 },
		func(l *Look) { l.TitleColor = "#fff" },
		func(l *Look) { l.Pace = 49 },
		func(l *Look) { l.Pace = 301 },
	}
	for i, change := range more {
		l := DefaultLook()
		change(&l)
		if _, _, err := s.Create(ctx, "x", KindAlert, l, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("more %d: %v", i, err)
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
	unfold := DefaultLook()
	unfold.Animation = "unfold"
	if _, err := s.SetLook(ctx, "a", unfold, now); err != nil {
		t.Fatal("unfold:", err)
	}
	fancy := DefaultLook()
	fancy.Background2, fancy.GradientAngle, fancy.BorderWidth, fancy.TextOutline, fancy.TextShadow = "#ff00aa", 90, 3, 2, 60
	fancy.Font, fancy.FontName, fancy.TextSize, fancy.TitleColor = "custom", "Comic Neue", 150, "#ffcc00"
	if o, err := s.SetLook(ctx, "a", fancy, now); err != nil || o.Look != fancy {
		t.Fatal(o, err)
	}
}

// A look saved before the newer options keeps how it looked: their
// defaults, and a title in the text's color.
func TestOlderLooksKeepTheirAppearance(t *testing.T) {
	ctx := context.Background()
	s, db, _ := newStore(t)
	if _, _, err := s.Create(ctx, "old", KindAlert, DefaultLook(), now); err != nil {
		t.Fatal(err)
	}
	old := `{"position":"bottom","animation":"pop","text_color":"#112233","background":"#ffffff","opacity":70,"seconds":5}`
	if _, err := db.ExecContext(ctx, "UPDATE overlays SET look = ?", old); err != nil {
		t.Fatal(err)
	}
	o, err := s.Get(ctx, "old")
	want := DefaultLook()
	want.Position, want.Animation, want.TextColor, want.TitleColor, want.Background, want.Opacity, want.Seconds =
		"bottom", "pop", "#112233", "#112233", "#ffffff", 70, 5
	if err != nil || o.Look != want || o.Look.Check() != nil {
		t.Fatalf("%+v %v", o.Look, err)
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
	// An unsaved look reaches previews only.
	if n := h.PreviewLook(Overlay{Name: "alerts", Look: DefaultLook()}); n != 1 {
		t.Fatal(n)
	}
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
	srv := httptest.NewServer(NewServer(s, h, nil, nil))
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

// Through the real server: with two overlays open, an alert reaches only
// the pages of the overlay it's for.
func TestAnAlertReachesOnlyItsOwnOverlay(t *testing.T) {
	ctx := context.Background()
	s, h, srv := serve(t)
	_, tokA, _ := s.Create(ctx, "alerts", KindAlert, DefaultLook(), now)
	_, tokB, _ := s.Create(ctx, "corner", KindAlert, DefaultLook(), now)
	type page struct {
		c   *websocket.Conn
		got chan string
	}
	openPage := func(token string) page {
		c, err := dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/o/"+token+"/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		p := page{c: c, got: make(chan string, 10)}
		go func() {
			for {
				_, b, err := c.Read(ctx)
				if err != nil {
					return
				}
				var m struct {
					Type string
					Body struct{ Alert Alert }
				}
				_ = json.Unmarshal(b, &m)
				if m.Type == "alert" {
					p.got <- m.Body.Alert.Text
					ack, _ := json.Marshal(map[string]string{"type": "ack", "id": m.Body.Alert.ID, "status": "started"})
					_ = c.Write(ctx, websocket.MessageText, ack)
				}
			}
		}()
		return p
	}
	a, b := openPage(tokA), openPage(tokB)
	for {
		pa, _ := h.Open("alerts")
		pb, _ := h.Open("corner")
		if pa == 1 && pb == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if res, err := h.Show(ctx, "corner", Alert{Text: "for corner", Seconds: 2}, ""); err != nil || res.Pages != 1 {
		t.Fatal(res, err)
	}
	if got := <-b.got; got != "for corner" {
		t.Fatal(got)
	}
	select {
	case got := <-a.got:
		t.Fatalf("the other overlay showed %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// An alert's sound: the run says it played when a page started it, why
// not when every page refused, and that it doesn't know when none said.
func TestAlertSoundsAreReported(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		answer string // what the page says about the sound
		want   error
	}{{"sound_started", nil}, {"sound_error", ErrSoundFailed}, {"", ErrSoundNoAnswer}} {
		h := NewHub(nil)
		c := h.join(Overlay{Name: "alerts"}, false)
		go func() {
			for b := range c.send {
				var m struct {
					Type string
					Body struct{ Alert Alert }
				}
				_ = json.Unmarshal(b, &m)
				if m.Type != "alert" {
					continue
				}
				h.ack(c, m.Body.Alert.ID, "started", "")
				if tc.answer != "" {
					h.ack(c, m.Body.Alert.ID, tc.answer, "NotAllowedError")
				}
			}
		}()
		res, err := h.Show(ctx, "alerts", Alert{Text: "x", Seconds: 2, Sound: &AlertSound{Name: "chime", Volume: 70}}, "")
		if !errors.Is(err, tc.want) || !res.Shown || res.SoundPlayed != (tc.want == nil) {
			t.Errorf("%q: %+v %v", tc.answer, res, err)
		}
		if tc.answer == "sound_error" && !strings.Contains(err.Error(), "NotAllowedError") {
			t.Errorf("the reason is missing: %v", err)
		}
		close(c.send)
	}
}

type fakeSounds map[string][]byte

func (f fakeSounds) Audio(_ context.Context, name string) (sounds.Sound, []byte, error) {
	b, ok := f[name]
	if !ok {
		return sounds.Sound{}, nil, sounds.ErrNotFound
	}
	return sounds.Sound{Name: name, Format: "wav", UpdatedAt: now}, b, nil
}

// Sounds are served under a working link only, with their type, and ranges.
func TestSoundsAreServedUnderALink(t *testing.T) {
	s, _, _ := newStore(t)
	h := NewHub(nil)
	srv := httptest.NewServer(NewServer(s, h, fakeSounds{"chime": []byte("RIFF....WAVEdata")}, nil))
	t.Cleanup(srv.Close)
	_, token, _ := s.Create(context.Background(), "alerts", KindAlert, DefaultLook(), now)
	get := func(path string, hdr ...string) (int, string, string) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if len(hdr) == 2 {
			req.Header.Set(hdr[0], hdr[1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Content-Type"), string(b)
	}
	if code, ct, body := get("/o/" + token + "/sound/chime"); code != 200 || ct != "audio/wav" || body != "RIFF....WAVEdata" {
		t.Fatal(code, ct, body)
	}
	if code, _, body := get("/o/"+token+"/sound/chime", "Range", "bytes=0-3"); code != 206 || body != "RIFF" {
		t.Fatal(code, body)
	}
	for _, p := range []string{"/o/not-a-token/sound/chime", "/o/" + token + "/sound/nope", "/o/" + token + "/sound/..%2Fx", "/o/" + token + "/sound/"} {
		if code, _, _ := get(p); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if code, _, _ := get("/o/" + token + "/"); code != 200 {
		t.Fatal(code)
	}
	res, _ := http.Get(srv.URL + "/o/" + token + "/")
	_ = res.Body.Close()
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "media-src 'self'") {
		t.Fatal(res.Header.Get("Content-Security-Policy"))
	}
}
