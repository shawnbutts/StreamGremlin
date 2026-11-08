// Package templates holds the ready-made workflows the gallery offers
// (D-019 in docs/DECISIONS.md). They're data only: workflow definitions
// with a title and the blanks a streamer fills in, embedded in the build
// and checked against the real engine by tests. A template is never added
// unasked: it opens in the editor, turned off, and is saved only when the
// streamer saves it.
package templates

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed data/*.json
var data embed.FS

// Blank is a value the streamer must fill in before saving, at a JSON path
// in the workflow, left as "" in the template.
type Blank struct {
	Path string `json:"path"`
	Hint string `json:"hint"`
}

// Template is one ready-made workflow.
type Template struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Starter     bool            `json:"starter"` // a good first workflow
	Blanks      []Blank         `json:"blanks"`
	Workflow    json.RawMessage `json:"workflow"`
}

// Categories, in the order the gallery shows them.
var Categories = []string{"Chat commands", "Twitch events", "Quotes", "Scenes", "Stream"}

// All returns every template, by category then title.
func All() ([]Template, error) {
	files, err := fs.Glob(data, "data/*.json")
	if err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(files))
	for _, f := range files {
		raw, err := data.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var file struct {
			Template Template        `json:"template"`
			Workflow json.RawMessage `json:"workflow"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("templates: %s: %w", f, err)
		}
		t := file.Template
		t.Workflow = file.Workflow
		if t.Blanks == nil {
			t.Blanks = []Blank{}
		}
		out = append(out, t)
	}
	rank := func(c string) int {
		for i, x := range Categories {
			if x == c {
				return i
			}
		}
		return len(Categories)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return rank(out[i].Category) < rank(out[j].Category)
		}
		return out[i].Title < out[j].Title
	})
	return out, nil
}
