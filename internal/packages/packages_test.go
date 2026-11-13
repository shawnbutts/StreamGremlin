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
	"slices"
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
	if r.Manifest.ID != "paper-wheel" || r.Manifest.Theme.Entry != "index.html" || len(r.Files) != 8 || len(r.Manifest.Theme.Settings) != 5 {
		t.Fatal(r.Manifest, r.Paths())
	}
}

// The example clock and timer look is a valid package, for timers only
// (D-092).
func TestTheExampleTimerLookIsAPackage(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "themes", "flip-clock")
	var files []file
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // the repository's own example
		rel, _ := filepath.Rel(dir, p)
		files = append(files, file{name: "flip-clock/" + filepath.ToSlash(rel), body: string(b)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := ReadZip(zipOf(t, files...))
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest.ID != "flip-clock" || !slices.Equal(r.Manifest.Theme.For, []string{"timer"}) || len(r.Manifest.Theme.Settings) != 4 {
		t.Fatal(r.Manifest, r.Paths())
	}
	// A theme is for one kind of overlay, and only the kinds with looks.
	for _, bad := range []string{`["wheel","timer"]`, `["alert"]`, `[]`} {
		m := strings.Replace(string(files[slices.IndexFunc(files, func(f file) bool { return strings.HasSuffix(f.name, "streamgremlin.json") })].body),
			`"for": ["timer"]`, `"for": `+bad, 1)
		var other []file
		for _, f := range files {
			if strings.HasSuffix(f.name, "streamgremlin.json") {
				f.body = m
			}
			other = append(other, f)
		}
		if _, err := ReadZip(zipOf(t, other...)); err == nil {
			t.Errorf("for %s was read", bad)
		}
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
	if _, _, _, err := s.Install(ctx, data, strings.Repeat("0", 64), "", now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	p, from, created, err := s.Install(ctx, data, pv.Digest, "", now)
	if err != nil || !created || from != "" || p.Name != "Cosy wheel" || p.Publisher != "Someone" || p.Digest != pv.Digest || len(p.Settings) != 3 ||
		len(p.Kept) != 0 || p.RollbackTo != "" {
		t.Fatal(p, created, err)
	}
	// Again: nothing changes.
	if _, _, created, err := s.Install(ctx, data, pv.Digest, "", now); err != nil || created {
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
	if _, _, _, err := s.Install(ctx, other, "", "", now); !errors.Is(err, ErrConflict) {
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
	if _, _, created, err := s.Install(ctx, data, pv.Digest, "", now); err != nil || !created {
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
		_, _, _, err = s.Install(ctx, data, pv.Digest, "", now)
		if i < MaxPackages && err != nil || i == MaxPackages && !errors.Is(err, ErrFull) {
			t.Fatal(i, err)
		}
	}
}

func TestVersionsAreOrderedAsSemverSays(t *testing.T) {
	ordered := []string{"0.9.0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11",
		"1.0.0-rc.1", "1.0.0", "1.0.1", "1.2.0", "1.10.0", "2.0.0"}
	for i := range ordered {
		for j := range ordered {
			want := compareInts(i, j)
			if got := CompareVersions(ordered[i], ordered[j]); got != want {
				t.Errorf("%s vs %s: %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

// install checks and installs a version of the test theme.
func install(t *testing.T, s *Store, version, publisher, confirm string, extra ...file) (Package, string, error) {
	t.Helper()
	m := strings.Replace(manifest, `"version":"1.0.0"`, `"version":"`+version+`"`, 1)
	m = strings.Replace(m, `"publisher":"Someone"`, `"publisher":"`+publisher+`"`, 1)
	data := theme(t, m, extra...)
	pv, _, err := s.Check(context.Background(), data)
	if err != nil {
		return Package{}, "", err
	}
	p, from, _, err := s.Install(context.Background(), data, pv.Digest, confirm, now)
	return p, from, err
}

func TestUpdatingKeepsTwoVersionsToRollBackTo(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	if _, _, err := install(t, s, "1.0.0", "Someone", ""); err != nil {
		t.Fatal(err)
	}
	// What an update looks like before it's installed.
	pv, _, err := s.Check(ctx, theme(t, strings.Replace(manifest, "1.0.0", "1.1.0", 1)))
	if err != nil || !pv.Update || pv.Older || pv.PublisherChange || pv.Same || pv.Installed.Version != "1.0.0" {
		t.Fatal(pv, err)
	}
	p, from, err := install(t, s, "1.1.0", "Someone", "")
	if err != nil || from != "1.0.0" || p.Version != "1.1.0" || p.RollbackTo != "1.0.0" || !slices.Equal(p.Kept, []string{"1.0.0"}) {
		t.Fatal(p, from, err)
	}
	if _, err := s.File(ctx, "cosy-wheel", "1.0.0", "index.html"); err != nil {
		t.Fatal("the old version's files went:", err)
	}
	if _, _, err := install(t, s, "1.2.0", "Someone", ""); err != nil {
		t.Fatal(err)
	}
	p, _, err = install(t, s, "2.0.0", "Someone", "")
	// 1.0.0 goes: two kept besides the installed one.
	if err != nil || !slices.Equal(p.Kept, []string{"1.2.0", "1.1.0"}) || p.RollbackTo != "1.2.0" {
		t.Fatal(p, err)
	}
	if _, err := s.File(ctx, "cosy-wheel", "1.0.0", "index.html"); !errors.Is(err, ErrNotFound) {
		t.Fatal("1.0.0 kept:", err)
	}
	// Roll back, and again: each time to the one before. The one rolled back from is kept.
	p, from, err = s.Rollback(ctx, "cosy-wheel", now)
	if err != nil || from != "2.0.0" || p.Version != "1.2.0" || !slices.Equal(p.Kept, []string{"2.0.0", "1.1.0"}) || p.RollbackTo != "1.1.0" {
		t.Fatal(p, from, err)
	}
	p, _, err = s.Rollback(ctx, "cosy-wheel", now)
	if err != nil || p.Version != "1.1.0" || p.RollbackTo != "" {
		t.Fatal(p, err)
	}
	if _, _, err := s.Rollback(ctx, "cosy-wheel", now); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "no version") {
		t.Fatal(err)
	}
	// Installing a kept version again uses what's kept; with other files it's refused.
	p, from, err = install(t, s, "2.0.0", "Someone", "")
	if err != nil || from != "1.1.0" || p.Version != "2.0.0" {
		t.Fatal(p, from, err)
	}
	if _, _, err := install(t, s, "1.2.0", "Someone", "", file{name: "extra.js", body: "x"}); !errors.Is(err, ErrConflict) ||
		!strings.Contains(err.Error(), "same version") {
		t.Fatal(err)
	}
	// An older version is an update too, said to be older.
	pv, _, _ = s.Check(ctx, theme(t, strings.Replace(manifest, "1.0.0", "0.9.0", 1)))
	if !pv.Update || !pv.Older {
		t.Fatal(pv)
	}
	themes, _ := s.Themes(ctx)
	if len(themes) != 1 || themes[0].Version != "2.0.0" {
		t.Fatal(themes)
	}
	audit, _ := s.Audit(ctx, 20)
	var actions []string
	for _, a := range audit {
		actions = append(actions, a.Action+" "+a.Version)
	}
	want := []string{"update 2.0.0", "rollback 1.1.0", "rollback 1.2.0", "update 2.0.0", "update 1.2.0", "update 1.1.0", "install 1.0.0"}
	if !slices.Equal(actions, want) || !strings.Contains(audit[0].Detail, "1.1.0 to 2.0.0") {
		t.Fatal(actions, audit[0].Detail)
	}
}

// AGENTS.md: a pack never replaces another publisher's without an exact
// identity check.
func TestAnotherPublishersVersionNeedsTheirNameExactly(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	if _, _, err := install(t, s, "1.0.0", "Someone", ""); err != nil {
		t.Fatal(err)
	}
	m := strings.Replace(strings.Replace(manifest, "1.0.0", "1.1.0", 1), `"publisher":"Someone"`, `"publisher":"Someone Else"`, 1)
	data := theme(t, m)
	pv, _, err := s.Check(ctx, data)
	if err != nil || !pv.Update || !pv.PublisherChange || pv.Installed.Publisher != "Someone" || pv.Publisher != "Someone Else" {
		t.Fatal(pv, err)
	}
	for _, wrong := range []string{"", "Someone", "someone else", "Someone Else ", "Cosy wheel"} {
		if _, _, _, err := s.Install(ctx, data, pv.Digest, wrong, now); !errors.Is(err, ErrConfirm) ||
			!strings.Contains(err.Error(), "confirm the new publisher by name") {
			t.Fatalf("%q: %v", wrong, err)
		}
	}
	if p, _ := s.Get(ctx, "cosy-wheel"); p.Version != "1.0.0" || p.Publisher != "Someone" {
		t.Fatal("changed without confirming:", p)
	}
	p, _, _, err := s.Install(ctx, data, pv.Digest, "Someone Else", now)
	if err != nil || p.Publisher != "Someone Else" || p.Version != "1.1.0" {
		t.Fatal(p, err)
	}
	audit, _ := s.Audit(ctx, 1)
	if !strings.Contains(audit[0].Detail, "the publisher changed from Someone, confirmed by name") {
		t.Fatal(audit[0].Detail)
	}
	// Rolling back returns to the version the streamer had, with its publisher.
	p, _, err = s.Rollback(ctx, "cosy-wheel", now)
	if err != nil || p.Publisher != "Someone" {
		t.Fatal(p, err)
	}
	// Another kind with the same id is refused.
	lua := strings.Replace(strings.Replace(manifest, `"kind":"theme"`, `"kind":"lua"`, 1), "1.0.0", "1.2.0", 1)
	if _, _, err := s.Check(ctx, theme(t, lua)); err == nil {
		t.Fatal("a lua package with a theme's id")
	}
}
