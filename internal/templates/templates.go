// Package templates holds the ready-made workflows the gallery offers
// (D-019 in docs/DECISIONS.md). They're data only: workflow definitions
// with a title and the blanks a streamer fills in, embedded in the build
// and checked against the real engine by tests. A template is never added
// unasked: it opens in the editor, turned off, and is saved only when the
// streamer saves it.
package templates

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
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
	// AddOn is the add-on (a Lua plugin) whose template it is, or nil for
	// StreamGremlin's own (docs/adr/0038-lua-plugins.md). Its category is
	// the add-on's name.
	AddOn *AddOn `json:"add_on,omitempty"`
}

// AddOn says which add-on a template comes from.
type AddOn struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Publisher string `json:"publisher"` // as stated: not verified
	// Enabled: the add-on is on, so its steps work.
	Enabled bool `json:"enabled"`
}

// Limits on an add-on's templates.
const (
	MaxAddOnTemplates = 20
	maxBlanks         = 20
)

// ErrInvalid wraps every problem with an add-on's template.
var ErrInvalid = errors.New("templates: invalid")

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// file is a template's file: the gallery's part, and the workflow.
type file struct {
	Template Template        `json:"template"`
	Workflow json.RawMessage `json:"workflow"`
}

// ParseAddOn reads an add-on's template file, strictly: unknown fields are
// refused, it has no category (its section is the add-on's) and isn't a
// starter, and each blank is an empty text in the workflow. The workflow
// itself is checked against the engine's steps by the caller.
func ParseAddOn(raw []byte) (Template, error) {
	var f file
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Template{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	t := f.Template
	t.Workflow = f.Workflow
	if t.Blanks == nil {
		t.Blanks = []Blank{}
	}
	plain := func(s string, most int) bool {
		return utf8.RuneCountInString(s) <= most && strings.IndexFunc(s, unicode.IsControl) < 0
	}
	switch {
	case !idPattern.MatchString(t.ID):
		return t, fmt.Errorf("%w: the id is up to 32 lowercase letters, digits, - and _, starting with a letter", ErrInvalid)
	case strings.TrimSpace(t.Title) == "" || !plain(t.Title, 60):
		return t, fmt.Errorf("%w: the title is one line, up to 60 characters", ErrInvalid)
	case !plain(t.Description, 300):
		return t, fmt.Errorf("%w: the description is up to 300 characters", ErrInvalid)
	case t.Category != "":
		return t, fmt.Errorf("%w: an add-on's template has no category: it's in the add-on's own section", ErrInvalid)
	case t.Starter:
		return t, fmt.Errorf("%w: an add-on's template isn't a starter", ErrInvalid)
	case t.AddOn != nil:
		return t, fmt.Errorf("%w: add_on is StreamGremlin's to say", ErrInvalid)
	case len(t.Blanks) > maxBlanks:
		return t, fmt.Errorf("%w: up to %d blanks", ErrInvalid, maxBlanks)
	}
	var wf any
	if err := json.Unmarshal(t.Workflow, &wf); err != nil {
		return t, fmt.Errorf("%w: the workflow isn't JSON", ErrInvalid)
	}
	if _, ok := wf.(map[string]any); !ok {
		return t, fmt.Errorf("%w: the workflow is a JSON object", ErrInvalid)
	}
	for _, b := range t.Blanks {
		if !plain(b.Hint, 120) || strings.TrimSpace(b.Hint) == "" {
			return t, fmt.Errorf("%w: blank %s: a hint is up to 120 characters", ErrInvalid, b.Path)
		}
		if v, ok := At(wf, b.Path); !ok || v != "" {
			return t, fmt.Errorf("%w: blank %s isn't an empty text in the workflow", ErrInvalid, b.Path)
		}
	}
	return t, nil
}

// At finds the value at a $.a[0].b path.
func At(d any, path string) (any, bool) {
	if !strings.HasPrefix(path, "$.") {
		return nil, false
	}
	cur := d
	for _, p := range strings.Split(strings.ReplaceAll(strings.TrimPrefix(path, "$."), "[", ".["), ".") {
		if p == "" {
			return nil, false
		}
		if strings.HasPrefix(p, "[") {
			var n int
			if _, err := fmt.Sscanf(p, "[%d]", &n); err != nil {
				return nil, false
			}
			list, ok := cur.([]any)
			if !ok || n < 0 || n >= len(list) {
				return nil, false
			}
			cur = list[n]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// Categories, in the order the gallery shows them.
var Categories = []string{"Chat commands", "Welcome", "Twitch events", "Moderation", "Quotes", "Scenes", "Stream", "Discord"}

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
		var tf file
		if err := json.Unmarshal(raw, &tf); err != nil {
			return nil, fmt.Errorf("templates: %s: %w", f, err)
		}
		t := tf.Template
		t.Workflow = tf.Workflow
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
