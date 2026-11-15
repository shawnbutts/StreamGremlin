package templates

import (
	"os"
	"path/filepath"
	"testing"
	"unicode"
)

// FuzzParseAddOn: an add-on's template file comes from whoever made the
// add-on. An accepted one has a valid id and plain text, and its blanks
// are listed (never nil).
func FuzzParseAddOn(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "examples", "plugins", "*", "templates", "*.json"))
	for _, p := range files {
		if b, err := os.ReadFile(p); err == nil { //nolint:gosec // our own examples
			f.Add(b)
		}
	}
	f.Add([]byte(`{"template":{"id":"x"},"workflow":{}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		tp, err := ParseAddOn(raw)
		if err != nil {
			return
		}
		if !idPattern.MatchString(tp.ID) || tp.Blanks == nil {
			t.Fatalf("accepted %+v", tp)
		}
		for _, r := range tp.Title + tp.Description {
			if unicode.IsControl(r) {
				t.Fatalf("control character in %q", tp.Title+tp.Description)
			}
		}
	})
}
