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
	if code != 0 || !strings.Contains(out, "Paper wheel 1.0.0: a wheel look, published by StreamGremlin examples (not verified)") ||
		!strings.Contains(out, "can't reach the internet") || !strings.Contains(out, "-yes") {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "packages"); !strings.Contains(out, "No add-ons installed") {
		t.Fatal("installed without -yes:", out)
	}
	if out, errOut, code := cli(t, dir, "packages", "install", zipPath, "-yes"); code != 0 || !strings.Contains(out, "Installed Paper wheel 1.0.0") {
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
		if u := regexp.MustCompile(`"url":"(http://127\.0\.0\.1:\d+/t/paper-wheel/1\.0\.0/[0-9a-f]{64}/index\.html)"`).FindStringSubmatch(m); u != nil {
			url = u[1]
		}
	}
	p.mu.Unlock()
	if url == "" || !strings.Contains(strings.Join(p.got, "\n"), `"odds_checked":false`) {
		t.Fatal("no look link in", p.got)
	}
	for _, f := range []string{url, strings.TrimSuffix(url, "index.html") + "wheel.js"} {
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
