package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryRouteHasAScreen enforces AGENTS.md: the desktop app calls every
// API route, unless the route says why it is CLI-only.
func TestEveryRouteHasAScreen(t *testing.T) {
	src := filepath.Join("..", "..", "cmd", "desktop", "frontend", "src")
	var code strings.Builder
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.Contains(path, ".test.") {
			return nil
		}
		switch filepath.Ext(path) {
		case ".ts", ".svelte":
			b, err := os.ReadFile(path) //nolint:gosec // reading our own source tree in a test
			if err != nil {
				return err
			}
			code.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the desktop app's source: %v", err)
	}
	app := code.String()
	for _, rt := range newTestServer(t).Routes() {
		if rt.CLIOnly != "" {
			continue
		}
		if i := strings.Index(rt.Path, "{"); i >= 0 {
			// A path with a parameter is built in a template literal:
			// `/v1/secrets/${key}`.
			if !strings.Contains(app, "`"+rt.Path[:i]+"${") {
				t.Errorf("%s %s isn't called by the desktop app; build its screen, or mark it CLIOnly with a reason", rt.Method, rt.Path)
			}
			continue
		}
		if !strings.Contains(app, `"`+rt.Path+`"`) && !strings.Contains(app, `'`+rt.Path+`'`) {
			t.Errorf("%s %s isn't called by the desktop app; build its screen, or mark it CLIOnly with a reason", rt.Method, rt.Path)
		}
	}
}
