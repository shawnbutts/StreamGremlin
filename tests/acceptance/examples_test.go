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
