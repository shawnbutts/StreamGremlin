package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
		if strings.Contains(rt.Path, "{") {
			// A path with parameters is built in a template literal:
			// `/v1/secrets/${key}` or `/v1/rewards/${id}/redemptions/${rid}`.
			var pat strings.Builder
			for i, part := range regexp.MustCompile(`\{[^}]+\}`).Split(rt.Path, -1) {
				if i > 0 {
					pat.WriteString(`\$\{[^}]+\}`)
				}
				pat.WriteString(regexp.QuoteMeta(part))
			}
			call := regexp.MustCompile("`" + pat.String() + "`")
			if !call.MatchString(app) {
				t.Errorf("%s %s isn't called by the desktop app; build its screen, or mark it CLIOnly with a reason", rt.Method, rt.Path)
			}
			continue
		}
		called := false
		for _, q := range []string{`"`, `'`, "`"} { // the path alone, or with a query string
			if strings.Contains(app, q+rt.Path+q) || strings.Contains(app, q+rt.Path+"?") {
				called = true
			}
		}
		if !called {
			t.Errorf("%s %s isn't called by the desktop app; build its screen, or mark it CLIOnly with a reason", rt.Method, rt.Path)
		}
	}
}
