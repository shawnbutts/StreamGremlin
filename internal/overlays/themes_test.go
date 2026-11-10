package overlays

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
)

// Wheel looks (docs/adr/0036-packages-plugins-and-themes.md): a theme and
// its settings are checked, only wheel overlays have one, and unset
// settings take the theme's defaults.
func TestWheelLooksAreChecked(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStore(t)
	look := func(theme string, settings map[string]any) Look {
		l := DefaultLook()
		l.Theme, l.ThemeSettings = theme, settings
		return l
	}
	for name, l := range map[string]Look{
		"an unknown look":           look("builtin:disco", nil),
		"an installed id":           look("someone.wheel", nil),
		"an unknown setting":        look("builtin:workshop", map[string]any{"script": "x"}),
		"a title that isn't text":   look("builtin:workshop", map[string]any{"title": 5}),
		"a title that's too long":   look("builtin:workshop", map[string]any{"title": strings.Repeat("x", 71)}),
		"a title with a newline":    look("builtin:workshop", map[string]any{"title": "a\nb"}),
		"a switch that isn't":       look("builtin:neon-arcade", map[string]any{"show_title": "yes"}),
		"a colour that isn't":       look("builtin:selector-board", map[string]any{"accent": "red"}),
		"Classic with settings":     look("", map[string]any{"title": "x"}),
		"a colour that's injection": look("builtin:workshop", map[string]any{"accent": "#fff;}body{"}),
	} {
		if err := l.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	good := look("builtin:workshop", map[string]any{"title": "Pick a game", "show_title": false, "accent": "#112233"})
	if err := good.Check(); err != nil {
		t.Fatal(err)
	}
	// Only wheel overlays have a look other than Classic.
	if _, _, err := s.Create(ctx, "alerts", KindAlert, good, now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, _, err := s.Create(ctx, "alerts", KindAlert, DefaultLook(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetLook(ctx, "alerts", good, now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	o, _, err := s.Create(ctx, "wheel", KindWheel, good, now)
	if err != nil || o.Look.Theme != "builtin:workshop" || o.Look.ThemeSettings["title"] != "Pick a game" {
		t.Fatal(o, err)
	}
	// Back to Classic.
	if o, err := s.SetLook(ctx, "wheel", DefaultLook(), now); err != nil || o.Look.Theme != "" {
		t.Fatal(o, err)
	}
	// Defaults fill the gaps.
	th, _ := ThemeByID("builtin:neon-arcade")
	got := th.themeSettings(map[string]any{"title": "Raid wheel"})
	if got["title"] != "Raid wheel" || got["show_title"] != true || got["accent"] != "#3cf2c8" {
		t.Fatal(got)
	}
}

// A wheel overlay's hello names its look's page on the theme server, with
// its settings and whether its odds are checked. Classic, and an engine
// with no theme server, send none.
func TestAWheelOverlaysHelloNamesItsLook(t *testing.T) {
	h := NewHub(nil)
	hello := func(o Overlay) map[string]any {
		var m struct {
			Body map[string]any `json:"body"`
		}
		if err := json.Unmarshal(h.hello(o, false), &m); err != nil {
			t.Fatal(err)
		}
		return m.Body
	}
	l := DefaultLook()
	l.Theme = "builtin:selector-board"
	wheel := Overlay{Name: "w", Kind: KindWheel, Look: l}
	if _, ok := hello(wheel)["theme"]; ok {
		t.Fatal("a look without a theme server")
	}
	h.SetThemeOrigin("http://127.0.0.1:50123")
	th, ok := hello(wheel)["theme"].(map[string]any)
	if !ok || th["url"] != "http://127.0.0.1:50123/t/builtin/"+PageVersion+"/selector-board/index.html" || th["odds_checked"] != true ||
		th["settings"].(map[string]any)["show_title"] != true {
		t.Fatal(th)
	}
	if _, ok := hello(Overlay{Name: "w", Kind: KindWheel, Look: DefaultLook()})["theme"]; ok {
		t.Fatal("Classic has no theme")
	}
	if _, ok := hello(Overlay{Name: "a", Kind: KindAlert, Look: l})["theme"]; ok {
		t.Fatal("an alert overlay has no theme")
	}
}

func themeServer(t *testing.T) (*ThemeServer, *httptest.Server) {
	t.Helper()
	ts := NewThemeServer(nil)
	srv := httptest.NewServer(ts)
	t.Cleanup(srv.Close)
	ts.SetOrigins(srv.URL, []string{"http://127.0.0.1:7431", "http://localhost:7431"})
	return ts, srv
}

// got is an answer: its status, headers and body.
type got struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, url string) (got, string) {
	t.Helper()
	res, err := http.Get(url) //nolint:gosec // the test server's own URL
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return got{res.StatusCode, res.Header}, string(b)
}

// Every built-in look's files are served with a policy that sandboxes the
// page and allows no network, framed only by overlay pages and the app.
func TestLooksAreServedSandboxedWithNoNetwork(t *testing.T) {
	_, srv := themeServer(t)
	for _, th := range Themes {
		base := srv.URL + "/t/builtin/" + PageVersion + "/"
		res, body := get(t, base+th.dir+"/index.html")
		csp := res.Header.Get("Content-Security-Policy")
		for _, want := range []string{"sandbox allow-scripts;", "connect-src 'none'", "default-src 'none'", "frame-src 'none'", "worker-src 'none'",
			"form-action 'none'", "base-uri 'none'", "script-src 'self' " + srv.URL + ";",
			"frame-ancestors http://127.0.0.1:7431 http://localhost:7431 wails://localhost http://wails.localhost"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: the policy lacks %q: %s", th.ID, want, csp)
			}
		}
		if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "unsafe") {
			t.Errorf("%s: %s", th.ID, csp)
		}
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/html; charset=utf-8" || res.Header.Get("X-Content-Type-Options") != "nosniff" ||
			res.Header.Get("Referrer-Policy") != "no-referrer" || res.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: %d %v", th.ID, res.StatusCode, res.Header)
		}
		// Everything the page loads, and everything its scripts import,
		// is served: a look is complete. Nothing comes from elsewhere.
		pending := refs(t, th.dir, body)
		seen := map[string]bool{}
		for len(pending) > 0 {
			f := pending[0]
			pending = pending[1:]
			if seen[f] {
				continue
			}
			seen[f] = true
			res, body := get(t, base+f)
			if res.StatusCode != 200 || res.Header.Get("Content-Security-Policy") == "" {
				t.Errorf("%s: %s: %d", th.ID, f, res.StatusCode)
			}
			if strings.HasSuffix(f, ".js") {
				pending = append(pending, refs(t, path.Dir(f), body)...)
			}
		}
		if !seen[th.dir+"/main.js"] || !seen[th.dir+"/theme.css"] || !seen["shared/bridge.js"] {
			t.Errorf("%s loads %v", th.ID, seen)
		}
	}
}

var refPattern = regexp.MustCompile(`(?:src|href)="([^"]+)"|from '([^']+)'|import\('([^']+)'\)`)

// refs lists the files a page or script in dir refers to, as paths under
// /t/builtin/<version>/, refusing any outside the built-in looks' files
// (a URL, or a path out of them).
func refs(t *testing.T, dir, text string) []string {
	t.Helper()
	var out []string
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		ref := m[1] + m[2] + m[3]
		f := path.Clean(path.Join(dir, ref))
		if strings.Contains(ref, ":") || strings.HasPrefix(ref, "/") || strings.HasPrefix(f, "..") || strings.Count(f, "/") != 1 {
			t.Errorf("%s refers to %q, outside the looks' own files", dir, ref)
			continue
		}
		out = append(out, f)
	}
	return out
}

func TestTheThemeServerServesNothingElse(t *testing.T) {
	ts, srv := themeServer(t)
	_ = ts
	ok := srv.URL + themePath("workshop")
	for _, u := range []string{
		srv.URL + "/",
		srv.URL + "/o/anything/",
		srv.URL + "/t/builtin/0000000000000000/workshop/index.html", // another build's
		srv.URL + "/t/builtin/" + PageVersion + "/disco/index.html",
		srv.URL + "/t/builtin/" + PageVersion + "/page/overlay.js", // the overlay page's, not a look's
		srv.URL + "/t/builtin/" + PageVersion + "/shared/missing.js",
		ok + "../../../server.go",
		ok + "..%2f..%2fserver.go",
		ok + "Index.html",
		ok + "index.go",
		ok + "missing.js",
		ok + "sub/index.html",
	} {
		res, body := get(t, u)
		if res.StatusCode != 404 || body != "" {
			t.Errorf("%s: %d %q", u, res.StatusCode, body)
		}
	}
	req, _ := http.NewRequest("POST", ok+"index.html", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("POST:", res.StatusCode)
	}
	// Another host name pointed at this computer is refused.
	req, _ = http.NewRequest("GET", ok+"index.html", nil)
	req.Host = "evil.example"
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatal(res.StatusCode)
	}
}

// The overlay page may frame the theme server, and nothing else.
func TestTheOverlayPageMayFrameOnlyTheThemeServer(t *testing.T) {
	if !strings.Contains(CSP, "frame-src http://127.0.0.1:*;") || strings.Count(CSP, "frame-src") != 1 {
		t.Fatal(CSP)
	}
}
