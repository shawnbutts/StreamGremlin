package workflow

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse: a workflow can come from an import, a template or an add-on.
// An accepted definition's stored form is accepted again, unchanged.
func FuzzParse(f *testing.F) {
	f.Add(def("chat.hello", hello))
	f.Add(def("a.b", `"triggers":[],"steps":[],"surprise":1`))
	files, _ := filepath.Glob(filepath.Join("..", "templates", "data", "*.json"))
	for _, p := range files {
		var tf struct {
			Workflow json.RawMessage `json:"workflow"`
		}
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &tf) == nil { //nolint:gosec // our own templates
			f.Add([]byte(tf.Workflow))
		}
	}
	reg := testRegistry(&recorder{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		d, err := Parse(raw, reg)
		if err != nil {
			return
		}
		canon, sum := d.Canonical()
		again, err := Parse(canon, reg)
		if err != nil {
			t.Fatalf("the stored form isn't accepted: %v\n%s", err, canon)
		}
		canon2, sum2 := again.Canonical()
		if sum != sum2 || !bytes.Equal(canon, canon2) {
			t.Fatalf("stored form changed:\n%s\n%s", canon, canon2)
		}
	})
}
