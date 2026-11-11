package packages

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

var now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

// file is one entry in a test ZIP.
type file struct {
	name   string
	body   string
	mode   fs.FileMode // 0: a plain file
	method uint16
	flags  uint16
}

func zipOf(t *testing.T, files ...file) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	w.RegisterCompressor(99, func(out io.Writer) (io.WriteCloser, error) { return nopCloser{out}, nil })
	for _, f := range files {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Flags: f.flags}
		if f.method != 0 {
			h.Method = f.method
		}
		if f.mode != 0 {
			h.SetMode(f.mode)
		}
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

const manifest = `{"format":"streamgremlin.package/v1","id":"cosy-wheel","name":"Cosy wheel","version":"1.0.0","publisher":"Someone",
"kind":"theme","api":"1","theme":{"for":["wheel"],"entry":"index.html","settings":[
{"key":"title","label":"Title","kind":"text","default":"","max":70},
{"key":"size","label":"Size","kind":"number","default":3,"min":1,"max":5},
{"key":"paper","label":"Paper","kind":"choice","default":"Cream","options":["Cream","Sky"]}]}}`

func theme(t *testing.T, m string, extra ...file) []byte {
	t.Helper()
	return zipOf(t, append([]file{{name: "streamgremlin.json", body: m}, {name: "index.html", body: "<!doctype html>"},
		{name: "js/main.js", body: "export {};"}}, extra...)...)
}

// The example theme in the repository is a valid package.
func TestTheExampleThemeIsAPackage(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "themes", "paper-wheel")
	var files []file
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // the repository's own example
		rel, _ := filepath.Rel(dir, p)
		files = append(files, file{name: "paper-wheel/" + filepath.ToSlash(rel), body: string(b)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// As Finder compresses a folder: inside it, with its extras.
	files = append(files, file{name: "__MACOSX/paper-wheel/._index.html", body: "x"}, file{name: "paper-wheel/.DS_Store", body: "x"})
	r, err := ReadZip(zipOf(t, files...))
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest.ID != "paper-wheel" || r.Manifest.Theme.Entry != "index.html" || len(r.Files) != 5 || len(r.Manifest.Theme.Settings) != 5 {
		t.Fatal(r.Manifest, r.Paths())
	}
}

func TestPackagesAreReadWithLimits(t *testing.T) {
	m := func(old, new string) string {
		if !strings.Contains(manifest, old) {
			t.Fatalf("no %q in the manifest", old)
		}
		return strings.Replace(manifest, old, new, 1)
	}
	zeros := strings.Repeat("\x00", 1<<20)
	cases := map[string][]byte{
		"not a ZIP":                 []byte("hello"),
		"too big":                   make([]byte, MaxZipBytes+1),
		"no manifest":               zipOf(t, file{name: "index.html", body: "x"}),
		"going up":                  theme(t, manifest, file{name: "../evil.js", body: "x"}),
		"from the top":              theme(t, manifest, file{name: "/etc/evil.js", body: "x"}),
		"a backslash":               theme(t, manifest, file{name: `js\evil.js`, body: "x"}),
		"a drive":                   theme(t, manifest, file{name: "C:/evil.js", body: "x"}),
		"a dot folder":              theme(t, manifest, file{name: "js/./evil.js", body: "x"}),
		"a NUL":                     theme(t, manifest, file{name: "evil\x00.js", body: "x"}),
		"a space":                   theme(t, manifest, file{name: "my file.js", body: "x"}),
		"a link":                    theme(t, manifest, file{name: "link.js", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}),
		"encrypted":                 theme(t, manifest, file{name: "secret.js", body: "x", flags: 1}),
		"another packing":           theme(t, manifest, file{name: "odd.js", body: "x", method: 99}),
		"capitals":                  theme(t, manifest, file{name: "Index.html", body: "x"}),
		"a bomb":                    theme(t, manifest, file{name: "zeros.txt", body: zeros}),
		"a program":                 theme(t, manifest, file{name: "run.exe", body: "MZ"}),
		"unknown fields":            theme(t, m(`"api":"1"`, `"api":"1","run":"rm -rf /"`)),
		"another format":            theme(t, m("streamgremlin.package/v1", "streamgremlin.package/v2")),
		"a bad id":                  theme(t, m("cosy-wheel", "Cosy Wheel")),
		"one of ours":               theme(t, m("cosy-wheel", "points")),
		"a builtin id":              theme(t, m("cosy-wheel", "builtin-wheel")),
		"a bad version":             theme(t, m("1.0.0", "1.0")),
		"build metadata":            theme(t, m("1.0.0", "1.0.0+abc")),
		"another API":               theme(t, m(`"api":"1"`, `"api":"2"`)),
		"Lua for now":               theme(t, m(`"kind":"theme"`, `"kind":"lua"`)),
		"not for wheels":            theme(t, m(`["wheel"]`, `["alert"]`)),
		"a missing page":            theme(t, m(`"entry":"index.html"`, `"entry":"page.html"`)),
		"a page that isn't":         theme(t, m(`"entry":"index.html"`, `"entry":"js/main.js"`)),
		"a secret setting":          theme(t, m(`"key":"title"`, `"key":"api_key"`)),
		"our sounds setting":        theme(t, m(`"key":"title"`, `"key":"sounds"`)),
		"a setting twice":           theme(t, m(`"key":"size"`, `"key":"title"`)),
		"a default that won't do":   theme(t, m(`"default":3`, `"default":9`)),
		"a choice not offered":      theme(t, m(`"default":"Cream"`, `"default":"Plaid"`)),
		"a kind we don't know":      theme(t, m(`"kind":"number"`, `"kind":"script"`)),
		"a homepage that isn't":     theme(t, m(`"publisher":"Someone"`, `"publisher":"Someone","homepage":"javascript:alert(1)"`)),
		"a control in the name":     theme(t, m("Cosy wheel", `Cosy\nwheel`)),
		"more after the manifest":   theme(t, manifest+"{}"),
		"text too long for a theme": theme(t, m(`"max":70`, `"max":5000`)),
	}
	many := []file{}
	for i := range MaxFiles + 1 {
		many = append(many, file{name: "f" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + "/" + string(rune('a'+i/26)) + ".txt", body: "x"})
	}
	cases["too many files"] = theme(t, manifest, many...)
	for name, data := range cases {
		_, err := ReadZip(data)
		t.Logf("%s: %v", name, err)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The digest depends on every path and file, not on the ZIP's order.
	a, err := ReadZip(theme(t, manifest))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ReadZip(zipOf(t, file{name: "js/main.js", body: "export {};"}, file{name: "index.html", body: "<!doctype html>"},
		file{name: "streamgremlin.json", body: manifest}))
	c, _ := ReadZip(theme(t, manifest, file{name: "more.txt", body: "x"}))
	if a.Digest != b.Digest || a.Digest == c.Digest || len(a.Digest) != 64 {
		t.Fatal(a.Digest, b.Digest, c.Digest)
	}
}

func newStore(t *testing.T) (*Store, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	return NewStore(db), db
}

func TestInstallingAskedForAndRemoving(t *testing.T) {
	ctx := context.Background()
	s, db := newStore(t)
	data := theme(t, manifest, file{name: "start.wav", body: "RIFF"}, file{name: "cheer.wav", body: "RIFF"})
	pv, _, err := s.Check(ctx, data)
	if err != nil || pv.ID != "cosy-wheel" || pv.Installed != nil || pv.Same || !pv.Sounds || pv.Files != 5 {
		t.Fatal(pv, err)
	}
	// Nothing is stored by checking, or by a confirmation of something else.
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Fatal(list)
	}
	if _, _, err := s.Install(ctx, data, strings.Repeat("0", 64), now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	p, created, err := s.Install(ctx, data, pv.Digest, now)
	if err != nil || !created || p.Name != "Cosy wheel" || p.Publisher != "Someone" || p.Digest != pv.Digest || len(p.Settings) != 3 {
		t.Fatal(p, created, err)
	}
	// Again: nothing changes.
	if _, created, err := s.Install(ctx, data, pv.Digest, now); err != nil || created {
		t.Fatal(created, err)
	}
	pv, _, _ = s.Check(ctx, data)
	if !pv.Same || pv.Installed == nil {
		t.Fatal(pv)
	}
	// The same version with other files is another package: refused, and audited.
	other := theme(t, manifest, file{name: "evil.js", body: "x"})
	if _, _, err := s.Check(ctx, other); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "same version") {
		t.Fatal(err)
	}
	if _, _, err := s.Install(ctx, other, "", now); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	// Another version: updating comes in slice 3b.
	if _, _, err := s.Check(ctx, theme(t, strings.Replace(manifest, "1.0.0", "1.1.0", 1))); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	// Its files, and its theme with its sounds.
	if b, err := s.File(ctx, "cosy-wheel", "1.0.0", "js/main.js"); err != nil || string(b) != "export {};" {
		t.Fatal(string(b), err)
	}
	if _, err := s.File(ctx, "cosy-wheel", "1.0.0", "missing.js"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	themes, err := s.Themes(ctx)
	if err != nil || len(themes) != 1 || themes[0].ID != "cosy-wheel" || themes[0].OddsChecked || len(themes[0].Settings) != 4 ||
		themes[0].Settings[3].Key != "sounds" || !themes[0].Installed() {
		t.Fatal(themes, err)
	}
	// Removed: gone, its audit kept.
	if _, err := s.Remove(ctx, "cosy-wheel", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "cosy-wheel"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.File(ctx, "cosy-wheel", "1.0.0", "js/main.js"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Remove(ctx, "cosy-wheel", now); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	audit, err := s.Audit(ctx, 10)
	if err != nil || len(audit) != 3 || audit[0].Action != "remove" || audit[1].Action != "refuse" || audit[2].Action != "install" ||
		!strings.Contains(audit[2].Detail, "not verified") || !strings.Contains(audit[1].Detail, "same version") {
		t.Fatal(audit, err)
	}
	for _, q := range []string{"UPDATE package_audit SET detail = 'x'", "DELETE FROM package_audit"} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Fatal(q, "worked")
		}
	}
	// Installed again after removing.
	if _, created, err := s.Install(ctx, data, pv.Digest, now); err != nil || !created {
		t.Fatal(err)
	}
	// A version's files never change.
	if _, err := db.ExecContext(ctx, "UPDATE package_files SET data = x'00'"); err == nil {
		t.Fatal("a file changed")
	}
}

func TestThereIsALimitOnPackages(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	for i := range MaxPackages + 1 {
		id := "pkg-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		data := theme(t, strings.Replace(manifest, "cosy-wheel", id, 1))
		pv, _, err := s.Check(ctx, data)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = s.Install(ctx, data, pv.Digest, now)
		if i < MaxPackages && err != nil || i == MaxPackages && !errors.Is(err, ErrFull) {
			t.Fatal(i, err)
		}
	}
}
