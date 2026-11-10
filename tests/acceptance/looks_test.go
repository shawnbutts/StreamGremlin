//go:build acceptance

package acceptance

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Wheel looks through the real engine and CLI
// (docs/adr/0036-packages-plugins-and-themes.md): a wheel overlay set to a
// look tells its page where the look is; the look comes from a second
// origin, sandboxed with no network, framed only by the overlay page and
// the app; the overlay page may frame it; Classic again sends none.
func TestWheelLooksThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	out, errOut, code := cli(t, dir, "overlays", "add", "-wheel", "wheel")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	link := linkIn(t, out)
	p := openPage(t, link)
	if _, errOut, code := cli(t, dir, "overlays", "look", "wheel", "-look", "disco"); code == 0 || !strings.Contains(errOut, "no look") {
		t.Fatal("an unknown look:", errOut)
	}
	out, errOut, code = cli(t, dir, "overlays", "look", "wheel", "-look", "workshop", "-look-title", "Pick a game", "-accent", "#123456")
	if code != 0 || !strings.Contains(out, "the Workshop look") {
		t.Fatal(out, errOut)
	}
	eventually(t, "the look in the page's hello", func() string { return p.has("hello", `"title":"Pick a game"`) })
	var url string
	p.mu.Lock()
	for _, m := range p.got {
		if u := regexp.MustCompile(`"url":"(http://127\.0\.0\.1:\d+/t/builtin/[0-9a-f]+/workshop/index\.html)"`).FindStringSubmatch(m); u != nil {
			url = u[1]
		}
	}
	p.mu.Unlock()
	if url == "" {
		t.Fatal("no look link in", p.got)
	}
	overlayOrigin := regexp.MustCompile(`^http://[^/]+`).FindString(link)
	themeOrigin := regexp.MustCompile(`^http://[^/]+`).FindString(url)
	if themeOrigin == overlayOrigin {
		t.Fatal("the look shares the overlay page's origin")
	}
	for _, f := range []string{url, strings.TrimSuffix(url, "index.html") + "main.js", strings.TrimSuffix(url, "workshop/index.html") + "shared/bridge.js"} {
		res, err := http.Get(f)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		csp := res.Header.Get("Content-Security-Policy")
		if res.StatusCode != 200 || !strings.HasPrefix(csp, "sandbox allow-scripts;") || !strings.Contains(csp, "connect-src 'none'") ||
			!strings.Contains(csp, "frame-ancestors "+overlayOrigin) {
			t.Fatalf("%s: %d %q", f, res.StatusCode, csp)
		}
	}
	// The theme server serves nothing else, and nothing to another name.
	for _, f := range []string{themeOrigin + "/", themeOrigin + "/o/x/", strings.TrimSuffix(url, "index.html") + "../../../../server.go"} {
		res, err := http.Get(f)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatalf("%s: %d", f, res.StatusCode)
		}
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = "evil.example"
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatal("another host name:", err)
	} else {
		_ = res.Body.Close()
	}
	// The overlay page may frame the theme server.
	res, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-src http://127.0.0.1:*") {
		t.Fatal(res.Header.Get("Content-Security-Policy"))
	}
	// Back to Classic: the next hello has no look.
	if out, errOut, code := cli(t, dir, "overlays", "look", "wheel", "-look", "classic"); code != 0 || !strings.Contains(out, "the Classic look") {
		t.Fatal(out, errOut)
	}
	eventually(t, "Classic", func() string {
		p.mu.Lock()
		defer p.mu.Unlock()
		last := ""
		for _, m := range p.got {
			if strings.HasPrefix(m, "hello ") {
				last = m
			}
		}
		if strings.Contains(last, `"theme":{`) {
			return "the last hello still has a look"
		}
		return ""
	})
}
