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
	"slices"
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
		"sounds that aren't on/off": look("builtin:workshop", map[string]any{"sounds": "loud"}),
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
	if got["title"] != "Raid wheel" || got["show_title"] != true || got["accent"] != "#3cf2c8" || got["sounds"] != true {
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
	if !ok || th["url"] != "http://127.0.0.1:50123/t/builtin/"+PageVersion+"/selector-board/index.html" || th["odds_checked"] != true || th["add_on"] != nil ||
		th["settings"].(map[string]any)["show_title"] != true {
		t.Fatal(th)
	}
	// Its sounds (D-065), unless they're turned off.
	sounds, _ := th["sounds"].(map[string]any)
	for _, cue := range ThemeCues {
		if sounds[cue] != "http://127.0.0.1:50123/t/builtin/"+PageVersion+"/selector-board/"+cue+".wav" {
			t.Fatal(sounds)
		}
	}
	l.ThemeSettings = map[string]any{"sounds": false}
	if th := hello(Overlay{Name: "w", Kind: KindWheel, Look: l})["theme"].(map[string]any); th["sounds"] != nil || th["url"] == nil {
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
			"form-action 'none'", "base-uri 'none'", "media-src 'none'", "script-src 'self' " + srv.URL + ";",
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
		// Its sounds, for the overlay page to play (D-065).
		for _, cue := range ThemeCues {
			res, body := get(t, base+th.dir+"/"+cue+".wav")
			if res.StatusCode != 200 || res.Header.Get("Content-Type") != "audio/wav" || !strings.HasPrefix(body, "RIFF") ||
				res.Header.Get("Cross-Origin-Resource-Policy") != "cross-origin" {
				t.Errorf("%s: %s.wav: %d %v", th.ID, cue, res.StatusCode, res.Header)
			}
			req, _ := http.NewRequest("GET", base+th.dir+"/"+cue+".wav", nil)
			req.Header.Set("Range", "bytes=0-3")
			part, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(part.Body)
			_ = part.Body.Close()
			if part.StatusCode != http.StatusPartialContent || string(b) != "RIFF" {
				t.Errorf("%s: %s.wav in parts: %d %q", th.ID, cue, part.StatusCode, b)
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

// The overlay page may frame the theme server, and nothing else, and play
// sounds from itself and the theme server.
func TestTheOverlayPageMayFrameOnlyTheThemeServer(t *testing.T) {
	if !strings.Contains(CSP, "frame-src http://127.0.0.1:*;") || strings.Count(CSP, "frame-src") != 1 ||
		!strings.Contains(CSP, "media-src 'self' http://127.0.0.1:*;") || strings.Count(CSP, "media-src") != 1 {
		t.Fatal(CSP)
	}
}

// installedTheme is a theme as an installed package gives it.
func installedTheme(files ...string) Theme {
	return InstalledTheme("cosy-wheel", "Cosy wheel", "Warm.", "1.0.0", "Someone", strings.Repeat("ab", 32), "page/index.html", KindWheel,
		[]Setting{{Key: "size", Label: "Size", Kind: SettingNumber, Default: 3, Min: 1, Max: 5},
			{Key: "paper", Label: "Paper", Kind: SettingChoice, Default: "Cream", Options: []string{"Cream", "Sky"}}},
		append([]string{"streamgremlin.json", "page/index.html"}, files...))
}

// Installed themes (ADR-0036): checked against what's installed, never
// marked as checking the odds, served from their own version's path, and
// gone back to Classic when removed.
func TestInstalledThemes(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStore(t)
	c := NewCatalogue()
	s.UseThemes(c)
	l := DefaultLook()
	l.Theme, l.ThemeSettings = "cosy-wheel", map[string]any{"size": float64(4), "paper": "Sky"}
	if _, _, err := s.Create(ctx, "wheel", KindWheel, l, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("not installed:", err)
	}
	c.SetInstalled([]Theme{installedTheme("page/start.wav", "start.wav")})
	if _, _, err := s.Create(ctx, "wheel", KindWheel, l, now); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]map[string]any{
		"a number out of range": {"size": float64(9)},
		"a fraction":            {"size": 2.5},
		"a choice not offered":  {"paper": "Plaid"},
		"a setting it hasn't":   {"title": "x"},
	} {
		bad := l
		bad.ThemeSettings = v
		if _, err := s.SetLook(ctx, "wheel", bad, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	all := c.All()
	if len(all) != 4 || all[3].ID != "cosy-wheel" || all[3].OddsChecked || all[3].Publisher != "Someone" || all[3].Settings[2].Key != "sounds" {
		t.Fatal(all)
	}
	// The hello: its page and sounds (beside its page only), its settings.
	h := NewHub(nil)
	h.UseThemes(c)
	h.SetThemeOrigin("http://127.0.0.1:50123")
	o, _ := s.Get(ctx, "wheel")
	var m struct {
		Body struct {
			Theme ThemeView `json:"theme"`
		} `json:"body"`
	}
	if err := json.Unmarshal(h.hello(o, false), &m); err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:50123/t/cosy-wheel/1.0.0/" + strings.Repeat("ab", 32) + "/"
	if v := m.Body.Theme; v.URL != base+"page/index.html" || v.OddsChecked || !v.AddOn || v.Settings["paper"] != "Sky" ||
		len(v.Sounds) != 1 || v.Sounds["start"] != base+"page/start.wav" {
		t.Fatal(v)
	}
	// Removed: back to Classic, other settings kept; and a hello without it.
	o.Look.TextSize = 150
	if _, err := s.SetLook(ctx, "wheel", o.Look, now); err != nil {
		t.Fatal(err)
	}
	c.SetInstalled(nil)
	m.Body.Theme = ThemeView{}
	if err := json.Unmarshal(h.hello(o, false), &m); err != nil || m.Body.Theme.URL != "" {
		t.Fatal("a hello for a theme no longer installed", m.Body.Theme)
	}
	reset, err := s.ResetTheme(ctx, "cosy-wheel", now)
	if err != nil || len(reset) != 1 || reset[0].Look.Theme != "" || reset[0].Look.ThemeSettings != nil || reset[0].Look.TextSize != 150 {
		t.Fatal(reset, err)
	}
	if reset, err := s.ResetTheme(ctx, "cosy-wheel", now); err != nil || len(reset) != 0 {
		t.Fatal(reset, err)
	}
}

// A new version (an update or roll back): each overlay keeps the settings
// it still takes; the rest go back to the new version's defaults, named.
func TestOverlaysFitANewVersion(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newStore(t)
	c := NewCatalogue()
	s.UseThemes(c)
	c.SetInstalled([]Theme{installedTheme()})
	l := DefaultLook()
	l.Theme, l.ThemeSettings = "cosy-wheel", map[string]any{"size": float64(4), "paper": "Sky"}
	if _, _, err := s.Create(ctx, "wheel", KindWheel, l, now); err != nil {
		t.Fatal(err)
	}
	l.ThemeSettings = map[string]any{"size": float64(2)}
	if _, _, err := s.Create(ctx, "other", KindWheel, l, now); err != nil {
		t.Fatal(err)
	}
	// 2.0.0: size now 1-3, no "paper", a new setting.
	c.SetInstalled([]Theme{InstalledTheme("cosy-wheel", "Cosy wheel", "", "2.0.0", "Someone", strings.Repeat("cd", 32), "page/index.html", KindWheel,
		[]Setting{{Key: "size", Label: "Size", Kind: SettingNumber, Default: 3, Min: 1, Max: 3},
			{Key: "glow", Label: "Glow", Kind: SettingSwitch, Default: true}}, []string{"page/index.html"})})
	got, err := s.RefitTheme(ctx, "cosy-wheel", now)
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	byName := map[string]Refit{}
	for _, r := range got {
		byName[r.Overlay.Name] = r
	}
	if r := byName["wheel"]; !slices.Equal(r.Reset, []string{"Size", "paper"}) || r.Overlay.Look.ThemeSettings != nil {
		t.Fatal(r)
	}
	if r := byName["other"]; len(r.Reset) != 0 || r.Overlay.Look.ThemeSettings["size"] != float64(2) {
		t.Fatal(r)
	}
	if o, _ := s.Get(ctx, "wheel"); o.Look.Theme != "cosy-wheel" || o.Look.ThemeSettings != nil {
		t.Fatal(o.Look)
	}
}

func TestTheThemeServerServesOnlyTheInstalledVersion(t *testing.T) {
	ts, srv := themeServer(t)
	c := NewCatalogue()
	c.SetInstalled([]Theme{installedTheme()})
	read := map[string]string{}
	ts.UseThemes(c, func(_ context.Context, id, version, p string) ([]byte, error) {
		read[p] = id + "@" + version
		switch p {
		case "page/index.html":
			return []byte("<!doctype html>"), nil
		case "page/Logo.PNG":
			return []byte("png"), nil
		case "notes.exe":
			return []byte("MZ"), nil
		}
		return nil, errors.New("no such file")
	})
	base := srv.URL + "/t/cosy-wheel/1.0.0/" + strings.Repeat("ab", 32) + "/"
	res, body := get(t, base+"page/index.html")
	csp := res.Header.Get("Content-Security-Policy")
	if res.StatusCode != 200 || body != "<!doctype html>" || !strings.HasPrefix(csp, "sandbox allow-scripts;") || !strings.Contains(csp, "connect-src 'none'") ||
		res.Header.Get("Content-Type") != "text/html; charset=utf-8" || read["page/index.html"] != "cosy-wheel@1.0.0" {
		t.Fatal(res, body)
	}
	if res, _ := get(t, base+"page/Logo.PNG"); res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Fatal(res)
	}
	for _, u := range []string{
		srv.URL + "/t/cosy-wheel/1.0.1/" + strings.Repeat("ab", 32) + "/page/index.html", // another version
		srv.URL + "/t/cosy-wheel/1.0.0/" + strings.Repeat("cd", 32) + "/page/index.html", // another digest
		srv.URL + "/t/other-wheel/1.0.0/" + strings.Repeat("ab", 32) + "/page/index.html",
		base + "missing.js",
		base + "notes.exe", // a type a theme doesn't have
		base + "page/../page/index.html",
		base + "page/%2e%2e/x.js",
		base,
	} {
		if res, body := get(t, u); res.StatusCode != 404 || body != "" {
			t.Errorf("%s: %d %q", u, res.StatusCode, body)
		}
	}
	c.SetInstalled(nil) // removed
	if res, _ := get(t, base+"page/index.html"); res.StatusCode != 404 {
		t.Fatal("served after removing:", res.StatusCode)
	}
}
