//go:build acceptance

package acceptance

import (
	"archive/zip"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// zipFolder compresses a folder as Finder or Explorer does: everything in
// a folder of its name.
func zipFolder(t *testing.T, dir, to string) {
	t.Helper()
	f, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		fw, err := w.Create(filepath.Base(dir) + "/" + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = fw.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// Installed wheel looks through the real engine and CLI
// (docs/adr/0036-packages-plugins-and-themes.md): the example theme is
// checked, installed only when confirmed, set on a wheel overlay with its
// own settings, served sandboxed from its own path, and removed with the
// overlay back to Classic.
func TestInstalledLooksThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	zipPath := filepath.Join(t.TempDir(), "paper-wheel.zip")
	zipFolder(t, filepath.Join("..", "..", "examples", "themes", "paper-wheel"), zipPath)

	out, errOut, code := cli(t, dir, "packages", "install", zipPath)
	if code != 0 || !strings.Contains(out, "Paper wheel 1.1.0: a wheel look, published by StreamGremlin examples (not verified)") ||
		!strings.Contains(out, "It has its own sounds.") ||
		!strings.Contains(out, "can't reach the internet") || !strings.Contains(out, `"Page permissions" at "No access to OBS"`) || !strings.Contains(out, "-yes") {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "packages"); !strings.Contains(out, "No add-ons installed") {
		t.Fatal("installed without -yes:", out)
	}
	if out, errOut, code := cli(t, dir, "packages", "install", zipPath, "-yes"); code != 0 || !strings.Contains(out, "Installed Paper wheel 1.1.0") {
		t.Fatal(out, errOut)
	}
	if out, _, code := cli(t, dir, "packages", "install", zipPath, "-yes"); code != 0 || !strings.Contains(out, "installed already") {
		t.Fatal(out)
	}
	// A wheel overlay with it, and its own settings.
	out, errOut, code = cli(t, dir, "overlays", "add", "-wheel", "wheel")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	p := openPage(t, linkIn(t, out))
	if _, errOut, code := cli(t, dir, "overlays", "look", "wheel", "-look", "paper-wheel", "-set", "paper=Plaid"); code == 0 {
		t.Fatal("a choice it doesn't offer:", errOut)
	}
	if out, errOut, code := cli(t, dir, "overlays", "look", "wheel", "-look", "paper-wheel", "-set", "paper=Sky", "-set", "outline=5"); code != 0 ||
		!strings.Contains(out, "the Paper wheel look") {
		t.Fatal(out, errOut)
	}
	eventually(t, "the look in the hello", func() string { return p.has("hello", `"paper":"Sky"`) })
	var url string
	p.mu.Lock()
	for _, m := range p.got {
		if u := regexp.MustCompile(`"url":"(http://127\.0\.0\.1:\d+/t/paper-wheel/1\.1\.0/[0-9a-f]{64}/index\.html)"`).FindStringSubmatch(m); u != nil {
			url = u[1]
		}
	}
	p.mu.Unlock()
	if url == "" || !strings.Contains(strings.Join(p.got, "\n"), `"odds_checked":false`) {
		t.Fatal("no look link in", p.got)
	}
	// Its own sounds, beside its page, on unless the streamer turns them off.
	base := strings.TrimSuffix(url, "index.html")
	eventually(t, "the look's sounds in the hello", func() string { return p.has("hello", `"start":"`+base+`start.wav"`) })
	for _, f := range []string{url, base + "wheel.js", base + "start.wav"} {
		res, err := http.Get(f)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if csp := res.Header.Get("Content-Security-Policy"); res.StatusCode != 200 || !strings.HasPrefix(csp, "sandbox allow-scripts;") ||
			!strings.Contains(csp, "connect-src 'none'") {
			t.Fatalf("%s: %d %q", f, res.StatusCode, csp)
		}
	}
	if out, _, _ := cli(t, dir, "packages"); !strings.Contains(out, "used by wheel") {
		t.Fatal(out)
	}
	// Removing names the overlay first; then it's back to Classic, and
	// the files are gone.
	if out, _, code := cli(t, dir, "packages", "remove", "paper-wheel"); code != 0 || !strings.Contains(out, "back to the Classic look: wheel") {
		t.Fatal(out)
	}
	if out, errOut, code := cli(t, dir, "packages", "remove", "paper-wheel", "-yes"); code != 0 || !strings.Contains(out, "Back to Classic: wheel") {
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
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("served after removing:", res.StatusCode)
	}
	if out, _, _ := cli(t, dir, "packages", "history"); !strings.Contains(out, "install") || !strings.Contains(out, "remove") {
		t.Fatal(out)
	}
}

// copyLook copies the example look to a folder of the same name, with
// its manifest changed by edit, for a new version of it.
func copyLook(t *testing.T, edit func(string) string) string {
	t.Helper()
	src := filepath.Join("..", "..", "examples", "themes", "paper-wheel")
	dst := filepath.Join(t.TempDir(), "paper-wheel")
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	m := filepath.Join(dst, "streamgremlin.json")
	b, err := os.ReadFile(m) //nolint:gosec // a test's own copy
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m, []byte(edit(string(b))), 0o600); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "paper-wheel.zip")
	zipFolder(t, dst, zipPath)
	return zipPath
}

// Updating, rolling back, and another publisher's version, through the
// real engine and CLI (D-068): overlays move with each version, keeping
// the settings it takes; a version not installed is never served.
func TestUpdatingAndRollingBackThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	v1 := copyLook(t, func(m string) string { return m })
	if out, errOut, code := cli(t, dir, "packages", "install", v1, "-yes"); code != 0 || !strings.Contains(out, "Installed Paper wheel 1.1.0") {
		t.Fatal(out, errOut)
	}
	out, errOut, code := cli(t, dir, "overlays", "add", "-wheel", "wheel")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	p := openPage(t, linkIn(t, out))
	if out, errOut, code := cli(t, dir, "overlays", "look", "wheel", "-look", "paper-wheel", "-set", "paper=Blush", "-set", "outline=5"); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "1.1.0 in the hello", func() string { return p.has("hello", "/t/paper-wheel/1.1.0/") })

	// 1.2.0 drops Blush: an update, and the overlay's paper goes back to the default.
	v2 := copyLook(t, func(m string) string {
		return strings.Replace(strings.Replace(m, `"version": "1.1.0"`, `"version": "1.2.0"`, 1), `["Cream", "Sky", "Blush"]`, `["Cream", "Sky"]`, 1)
	})
	if out, _, code := cli(t, dir, "packages", "install", v2); code != 0 || !strings.Contains(out, "An update: 1.1.0 is installed, and is kept") {
		t.Fatal(out)
	}
	if out, errOut, code := cli(t, dir, "packages", "install", v2, "-yes"); code != 0 ||
		!strings.Contains(out, "Updated Paper wheel from 1.1.0 to 1.2.0. Using it: wheel. On wheel, Paper went back to the default.") {
		t.Fatal(out, errOut)
	}
	eventually(t, "1.2.0 in the hello", func() string { return p.has("hello", "/t/paper-wheel/1.2.0/") })
	if out, _, _ := cli(t, dir, "overlays", "-json"); strings.Contains(out, "Blush") || !strings.Contains(out, `"outline": 5`) {
		t.Fatal("settings after the update:", out)
	}
	if out, _, _ := cli(t, dir, "packages"); !strings.Contains(out, "Paper wheel 1.2.0") || !strings.Contains(out, "can roll back to 1.1.0") {
		t.Fatal(out)
	}

	// Rolled back: asked first, then the overlay moves back.
	if out, _, code := cli(t, dir, "packages", "rollback", "paper-wheel"); code != 0 || !strings.Contains(out, "keeps 1.2.0") ||
		!strings.Contains(out, "switch back with it: wheel") {
		t.Fatal(out)
	}
	if out, errOut, code := cli(t, dir, "packages", "rollback", "paper-wheel", "-yes"); code != 0 ||
		!strings.Contains(out, "Rolled Paper wheel back from 1.2.0 to 1.1.0.") {
		t.Fatal(out, errOut)
	}
	eventually(t, "back to 1.1.0", func() string {
		p.mu.Lock()
		defer p.mu.Unlock()
		last := ""
		for _, m := range p.got {
			if strings.HasPrefix(m, "hello ") {
				last = m
			}
		}
		if !strings.Contains(last, "/t/paper-wheel/1.1.0/") {
			return "the last hello isn't 1.1.0"
		}
		return ""
	})
	if _, errOut, code := cli(t, dir, "packages", "rollback", "paper-wheel", "-yes"); code == 0 || !strings.Contains(errOut, "no version") {
		t.Fatal("rolled back past the first:", errOut)
	}

	// Another publisher's: -yes alone isn't enough.
	v3 := copyLook(t, func(m string) string {
		return strings.Replace(strings.Replace(m, `"version": "1.1.0"`, `"version": "1.3.0"`, 1),
			`"publisher": "StreamGremlin examples"`, `"publisher": "Someone Else"`, 1)
	})
	if out, _, _ := cli(t, dir, "packages", "install", v3); !strings.Contains(out, "Another publisher") || !strings.Contains(out, `-publisher "Someone Else"`) {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "packages", "install", v3, "-yes"); code == 0 || !strings.Contains(errOut, `-publisher "Someone Else"`) {
		t.Fatal("replaced without the name:", errOut)
	}
	if _, errOut, code := cli(t, dir, "packages", "install", v3, "-yes", "-publisher", "someone else"); code == 0 {
		t.Fatal("replaced with the wrong name:", errOut)
	}
	if out, errOut, code := cli(t, dir, "packages", "install", v3, "-yes", "-publisher", "Someone Else"); code != 0 ||
		!strings.Contains(out, "Updated Paper wheel from 1.1.0 to 1.3.0") {
		t.Fatal(out, errOut)
	}
	out, _, _ = cli(t, dir, "packages", "history")
	for _, want := range []string{"install", "update", "rollback", "the publisher changed from StreamGremlin examples, confirmed by name"} {
		if !strings.Contains(out, want) {
			t.Fatalf("history without %q:\n%s", want, out)
		}
	}
}

// The hostile test look (D-068) installs like any look: no check of a
// package's files can tell what its code does. What holds it is how it's
// served: sandboxed with scripts only (no same origin, top navigation,
// pop-ups, forms or modals), no connections, frames or workers.
func TestAHostileLookIsServedSandboxed(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	zipPath := filepath.Join(t.TempDir(), "hostile-wheel.zip")
	zipFolder(t, filepath.Join("..", "testdata", "hostile-wheel"), zipPath)
	if out, errOut, code := cli(t, dir, "packages", "install", zipPath, "-yes"); code != 0 || !strings.Contains(out, "Installed Hostile wheel (test) 1.0.0") {
		t.Fatal(out, errOut)
	}
	out, errOut, code := cli(t, dir, "overlays", "add", "-wheel", "wheel")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	p := openPage(t, linkIn(t, out))
	if out, errOut, code := cli(t, dir, "overlays", "look", "wheel", "-look", "hostile-wheel"); code != 0 {
		t.Fatal(out, errOut)
	}
	var url string
	eventually(t, "its link", func() string {
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, m := range p.got {
			if u := regexp.MustCompile(`"url":"(http://127\.0\.0\.1:\d+/t/hostile-wheel/1\.0\.0/[0-9a-f]{64}/)index\.html"`).FindStringSubmatch(m); u != nil {
				url = u[1]
				return ""
			}
		}
		return "no link yet"
	})
	eventually(t, "marked an add-on", func() string { return p.has("hello", `"add_on":true`) })
	for _, f := range []string{"index.html", "hostile.js", "style.css"} {
		res, err := http.Get(url + f)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		csp := res.Header.Get("Content-Security-Policy")
		if res.StatusCode != 200 || !strings.HasPrefix(csp, "sandbox allow-scripts;") || strings.Count(csp, "allow-") != 1 {
			t.Fatalf("%s: %d %q", f, res.StatusCode, csp)
		}
		for _, want := range []string{"default-src 'none'", "connect-src 'none'", "frame-src 'none'", "worker-src 'none'", "form-action 'none'", "media-src 'none'"} {
			if !strings.Contains(csp, want) {
				t.Fatalf("%s: no %s in %q", f, want, csp)
			}
		}
	}
}
