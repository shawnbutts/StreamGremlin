package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMarkdown(t *testing.T) {
	src := "# Title\n\nSome **bold**, `a <b>`, *it* and [a link](https://example.com) and [a file](docs/x.md).\n" +
		"<script>alert(1)</script>\n\n" +
		"- one\n  continued\n  - nested `x`\n- two\n\n1. first\n2. second\n\nUnder\n-----\n\nTail\n"
	got := markdown(src, 1)
	for _, want := range []string{
		`<h2 id="title">Title</h2>`,
		`<strong>bold</strong>`, `<code>a &lt;b&gt;</code>`, `<em>it</em>`,
		`<a href="https://example.com">a link</a>`, ` and a file.`,
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		"<ul>\n<li>one continued<ul>\n<li>nested <code>x</code></li>\n</ul>\n</li>\n<li>two</li>\n</ul>",
		"<ol>\n<li>first</li>\n<li>second</li>\n</ol>",
		`<h3 id="under">Under</h3>`, `<p>Tail</p>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<script") {
		t.Error("raw HTML got through")
	}
}

func TestVersionOrder(t *testing.T) {
	for _, c := range [][2]string{{"0.1.0-beta.2", "0.1.0-beta.10"}, {"0.1.0-beta.9", "0.1.0"}, {"0.1.0", "0.2.0"}, {"0.9.0", "0.10.0"}} {
		if !versionLess(c[0], c[1]) || versionLess(c[1], c[0]) {
			t.Errorf("%s should come before %s", c[0], c[1])
		}
	}
}

// TestSite builds the real site and checks it: every page, every link and
// picture inside it resolves, there are no scripts or outside resources,
// Help has every term from the app's Help with its pictures, and the
// go-import pages point git at the repository.
func TestSite(t *testing.T) {
	out := t.TempDir()
	rel := t.TempDir()
	for _, f := range []string{"StreamGremlin-0.1.0-beta.2-macos.dmg", "StreamGremlin-0.1.0-beta.10-macos.dmg", "StreamGremlin-0.1.0-beta.10-windows-x64-setup.exe",
		"StreamGremlin-0.1.0-beta.10-linux-x64.deb", "StreamGremlin-0.1.0-beta.10-linux-x64.rpm", "StreamGremlin-0.1.0-beta.10-linux-x64.tar.gz", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(rel, f), []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := build(Options{Root: "../..", Out: out, Release: rel, Repo: "https://example.com/sg.git"}); err != nil {
		t.Fatal(err)
	}
	www := filepath.Join(out, "www")
	var files []string
	for _, p := range pages {
		files = append(files, p+".html")
	}
	checkPages(t, www, files)

	help, _ := os.ReadFile(filepath.Join(www, "help.html"))
	guide, _ := os.ReadFile("../desktop/frontend/src/help/guide.html")
	for _, m := range regexp.MustCompile(`<dt id="([^"]+)">`).FindAllStringSubmatch(string(guide), -1) {
		if !strings.Contains(string(help), `id="`+m[1]+`"`) {
			t.Errorf("Help on the site is missing %s", m[1])
		}
	}
	for id, s := range helpShots {
		if _, ok := shots[s]; !ok {
			t.Errorf("%s: no shot %s", id, s)
		}
	}

	dl, _ := os.ReadFile(filepath.Join(www, "download.html"))
	if !strings.Contains(string(dl), "0.1.0-beta.10") || strings.Contains(string(dl), "beta.2-macos") || strings.Count(string(dl), "SHA-256:") != 5 {
		t.Errorf("the download page should offer only beta.10's five installers:\n%s", dl)
	}
	// Windows, then macOS, then Linux's three.
	order := []string{"Download for Windows", "Download for macOS", "Download .deb", "Download .rpm", "Download .tar.gz"}
	for i := 1; i < len(order); i++ {
		if a, b := strings.Index(string(dl), order[i-1]), strings.Index(string(dl), order[i]); a < 0 || b < a {
			t.Errorf("%q should come before %q", order[i-1], order[i])
		}
	}

	// code.streamgremlin.com (D-116): every page checked like the site's,
	// and every package page has go-import and go-source.
	code := filepath.Join(out, "code")
	files = nil
	err := filepath.WalkDir(code, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".html") {
			rel, _ := filepath.Rel(code, p)
			files = append(files, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 60 {
		t.Errorf("code.streamgremlin.com has only %d pages", len(files))
	}
	checkPages(t, code, files)
	const goImport = `<meta name="go-import" content="code.streamgremlin.com/streamgremlin git https://example.com/sg.git">`
	const goSource = `<meta name="go-source" content="code.streamgremlin.com/streamgremlin https://example.com/sg https://example.com/sg/tree/main{/dir} https://example.com/sg/blob/main{/dir}/{file}#L{line}">`
	for _, pkg := range []string{"", "internal/api", "internal/youtube/livechat", "cmd/desktop", "cmd/streamgremlin"} {
		page, err := os.ReadFile(filepath.Join(code, "streamgremlin", filepath.FromSlash(pkg), "index.html"))
		if err != nil || !strings.Contains(string(page), goImport) || !strings.Contains(string(page), goSource) {
			t.Errorf("%q: no go-import and go-source: %v", pkg, err)
		}
	}
	home, _ := os.ReadFile(filepath.Join(code, "index.html"))
	for _, want := range []string{goImport, `href="streamgremlin/cmd/streamgremlin/index.html"`, `href="streamgremlin/internal/banlists/index.html"`,
		"Command streamgremlin is StreamGremlin", "go install code.streamgremlin.com/streamgremlin/cmd/streamgremlin@latest"} {
		if !strings.Contains(string(home), want) {
			t.Errorf("code.streamgremlin.com's home page has no %q", want)
		}
	}
	if strings.Contains(string(home), "tests/") || strings.Contains(string(home), "testdata") {
		t.Error("the home page lists test-only folders")
	}
	cli, _ := os.ReadFile(filepath.Join(code, "streamgremlin", "cmd", "streamgremlin", "index.html"))
	if !strings.Contains(string(cli), "go install code.streamgremlin.com/streamgremlin/cmd/streamgremlin@latest") {
		t.Error("the command line's page doesn't say how to install it")
	}
}

// checkPages checks pages of a built site: no scripts, ids once each, every
// relative link and picture there (from the page's folder, or the site's
// root under <base href="/">), only https outside, and nothing loaded from
// outside.
func checkPages(t *testing.T, root string, files []string) {
	t.Helper()
	ref := regexp.MustCompile(`(?:href|src)="([^"]*)"`)
	ids := regexp.MustCompile(`id="([^"]+)"`)
	for _, p := range files {
		raw, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		page := string(raw)
		if strings.Contains(page, "<script") || regexp.MustCompile(`\son[a-z]+="`).MatchString(page) {
			t.Errorf("%s has a script", p)
		}
		have := map[string]bool{}
		for _, m := range ids.FindAllStringSubmatch(page, -1) {
			if have[m[1]] {
				t.Errorf("%s: id %q twice", p, m[1])
			}
			have[m[1]] = true
		}
		dir := filepath.Dir(filepath.Join(root, p))
		if strings.Contains(page, `<base href="/">`) {
			dir = root
		}
		for _, m := range ref.FindAllStringSubmatch(page, -1) {
			u := m[1]
			switch {
			case strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "mailto:"):
				continue
			case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "//"):
				t.Errorf("%s links to %s without https", p, u)
			case strings.HasPrefix(u, "#"):
				if !have[u[1:]] {
					t.Errorf("%s: no anchor %s", p, u)
				}
			default:
				file, _, _ := strings.Cut(u, "#")
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file))); err != nil {
					t.Errorf("%s: %s doesn't exist", p, u)
				}
			}
		}
		if regexp.MustCompile(`(?:src|rel="stylesheet" href)="https?://`).MatchString(page) {
			t.Errorf("%s loads something from outside", p)
		}
	}
}
