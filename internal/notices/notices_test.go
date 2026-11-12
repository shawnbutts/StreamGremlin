package notices

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func module(t *testing.T, path, version string, files map[string]string) Module {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mod")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Module{Path: path, Version: version, Dir: dir}
}

// go list's lines become modules: duplicates across platforms once, our own
// module and the standard library (no module) left out.
func TestParse(t *testing.T) {
	out := "example.com/b\tv1.0.0\t/m/b\n" +
		"\n" +
		"example.com/a\tv2.0.0\t/m/a\n" +
		"code.streamgremlin.com/streamgremlin\t\t/repo\n" +
		"example.com/b\tv1.0.0\t/m/b\n" +
		"example.com/b\tv1.1.0\t/m/b2\n"
	got := Parse([]byte(out), "code.streamgremlin.com/")
	want := []Module{{"example.com/a", "v2.0.0", "/m/a"}, {"example.com/b", "v1.0.0", "/m/b"}, {"example.com/b", "v1.1.0", "/m/b2"}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v", i, got[i])
		}
	}
}

// Every licence file a module carries is copied whole, in a stable order,
// after Go's own; a module with none stops it.
func TestRender(t *testing.T) {
	a := module(t, "example.com/a", "v1.0.0", map[string]string{"LICENSE": "MIT, A\n", "NOTICE": "Notice A", "README.md": "not this", "LICENSE-SQLITE": "public domain"})
	b := module(t, "example.com/b", "v0.1.0", map[string]string{"copying.txt": "BSD, B\n"})
	out, err := Render("Go's licence\n", []Module{b, a})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"Go's licence", "example.com/a v1.0.0", "MIT, A", "Notice A", "public domain", "example.com/b v0.1.0", "BSD, B"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "not this") {
		t.Error("copied a README")
	}
	if strings.Index(s, "Go's licence") > strings.Index(s, "example.com/a") || strings.Index(s, "example.com/a") > strings.Index(s, "example.com/b") {
		t.Error("out of order")
	}
	if strings.Index(s, "== LICENSE\n") > strings.Index(s, "== LICENSE-SQLITE") || strings.Index(s, "== LICENSE-SQLITE") > strings.Index(s, "== NOTICE") {
		t.Error("files out of order")
	}
	again, _ := Render("Go's licence\n", []Module{a, b})
	if string(again) != s {
		t.Error("not stable")
	}

	none := module(t, "example.com/none", "v1.0.0", map[string]string{"README": "x"})
	if _, err := Render("Go", []Module{a, none}); err == nil || !strings.Contains(err.Error(), "example.com/none") {
		t.Fatal("a module without a licence:", err)
	}
}

// The notices file must name every module, as `path`.
func TestMissing(t *testing.T) {
	text := "| `example.com/a` | MIT |\n| `example.com/ab` | MIT |\nexample.com/b in prose\n"
	mods := []Module{{Path: "example.com/a"}, {Path: "example.com/a", Version: "v2"}, {Path: "example.com/b"}, {Path: "example.com/c"}}
	got := Missing(text, mods)
	if len(got) != 2 || got[0] != "example.com/b" || got[1] != "example.com/c" {
		t.Fatal(got)
	}
}
