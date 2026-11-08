//go:build acceptance

package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

// The example workflows must stay valid as the engine changes.
func TestExampleWorkflowsAreValid(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	files, _ := filepath.Glob("../../examples/workflows/*.json")
	if len(files) == 0 {
		t.Fatal("no examples found")
	}
	for _, f := range files {
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Errorf("%s: %s%s", f, out, errOut)
		} else if !strings.Contains(out, "Saved") {
			t.Errorf("%s: %s", f, out)
		}
	}
}

// The templates the gallery offers, through the CLI: listed, printed with
// their blanks, and saved as ordinary workflows.
func TestTemplatesThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	out, _, code := cli(t, dir, "templates")
	if code != 0 || !strings.Contains(out, "Lurk") || !strings.Contains(out, "Be right back") || !strings.Contains(out, "(1 to fill in)") {
		t.Fatalf("%d\n%s", code, out)
	}
	out, errOut, code := cli(t, dir, "templates", "show", "brb")
	if code != 0 || !strings.Contains(out, `"scene": ""`) || !strings.Contains(errOut, "Fill in $.steps[0].with.scene") {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	out, _, _ = cli(t, dir, "templates", "show", "lurk")
	f := writeFile(t, t.TempDir(), "lurk.json", out)
	if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 || !strings.Contains(out, "Saved chat.lurk") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "templates", "show", "nope"); code == 0 || !strings.Contains(errOut, "no template") {
		t.Fatal(errOut)
	}
}
