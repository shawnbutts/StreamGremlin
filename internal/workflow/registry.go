package workflow

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"
)

// Param describes one input of an action.
type Param struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // text, number, bool, choice or any (any JSON value)
	Choices []string `json:"choices,omitempty"`
	// Pattern, when set, is a regular expression a text value must match;
	// PatternHint says it in words.
	Pattern     string `json:"pattern,omitempty"`
	PatternHint string `json:"pattern_hint,omitempty"`
	// Literal parameters must be written out, never computed with ${…},
	// so chat can never choose them (which workflow to run, say).
	Literal bool `json:"literal,omitempty"`
	// Editor says how the plain-words editor shows it: message (text with
	// values), scene, source, workflow, seconds or variable. Empty means
	// by Type.
	Editor   string `json:"editor,omitempty"`
	Required bool   `json:"required"`
	Default  any    `json:"default,omitempty"`
}

// Action is something a step can do.
type Action struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
	// External actions reach Twitch, OBS or anything outside StreamGremlin.
	// They never run for test events: the step is recorded as skipped with
	// its evaluated input (a dry run).
	External bool `json:"external"`
	// SavesState actions change StreamGremlin's own saved data (variables).
	// They run for test events too, but the change is kept only for that
	// run and never saved.
	SavesState bool `json:"saves_state,omitempty"`
	// More puts the action behind "More steps…" in the editor.
	More   bool                                           `json:"more,omitempty"`
	Params []Param                                        `json:"params"`
	Run    func(ctx context.Context, c Call) (any, error) `json:"-"`
}

// Param returns the parameter named name, or nil.
func (a *Action) Param(name string) *Param {
	for i := range a.Params {
		if a.Params[i].Name == name {
			return &a.Params[i]
		}
	}
	return nil
}

// Call is one run of an action.
type Call struct {
	Args map[string]any
	// IdempotencyKey is the same whenever the same step runs for the same
	// job and loop iteration: job id / step path.
	IdempotencyKey string
	JobID          string
	WorkflowID     string
	DryRun         bool

	run *runVars // the run's saved variables, for the variable actions
}

// String returns a text argument, or def when absent.
func (c Call) String(name, def string) (string, error) {
	v, ok := c.Args[name]
	if !ok || v == nil {
		return def, nil
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case int64, float64, bool:
		return format(x), nil
	}
	return "", fmt.Errorf("%s must be text", name)
}

// Bool returns a true/false argument, or def when absent.
func (c Call) Bool(name string, def bool) (bool, error) {
	v, ok := c.Args[name]
	if !ok || v == nil {
		return def, nil
	}
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		if b, err := strconv.ParseBool(x); err == nil {
			return b, nil
		}
	}
	return false, fmt.Errorf("%s must be true or false", name)
}

// Registry holds the actions available to workflows.
type Registry struct {
	mu      sync.RWMutex
	actions map[string]*Action
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{actions: map[string]*Action{}} }

// Add registers an action. Names are unique.
func (r *Registry) Add(a Action) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.actions[a.Name]; dup {
		panic("workflow: action " + a.Name + " registered twice")
	}
	if a.Params == nil {
		a.Params = []Param{}
	}
	for _, p := range a.Params {
		if p.Pattern != "" {
			regexp.MustCompile(p.Pattern)
		}
	}
	r.actions[a.Name] = &a
}

// Get returns the action named name.
func (r *Registry) Get(name string) (*Action, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[name]
	return a, ok
}

// List returns every action, by group then name.
func (r *Registry) List() []*Action {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Action, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// checkArgs checks evaluated arguments against the action's parameters.
func checkArgs(a *Action, args map[string]any) error {
	for _, p := range a.Params {
		v, ok := args[p.Name]
		if !ok || v == nil {
			if p.Required {
				return fmt.Errorf("%s is required", p.Label)
			}
			continue
		}
		switch p.Type {
		case "bool":
			if _, ok := v.(bool); !ok {
				if s, isStr := v.(string); !isStr || (s != "true" && s != "false") {
					return fmt.Errorf("%s must be true or false", p.Label)
				}
			}
		case "number":
			switch v.(type) {
			case int64, float64:
			default:
				return fmt.Errorf("%s must be a number", p.Label)
			}
		case "text":
			if s, ok := v.(string); ok && p.Pattern != "" && !regexp.MustCompile(p.Pattern).MatchString(s) {
				return fmt.Errorf("%s: %q isn't %s", p.Label, s, p.PatternHint)
			}
		case "choice":
			s, _ := v.(string)
			found := false
			for _, c := range p.Choices {
				if s == c {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%s must be one of %v", p.Label, p.Choices)
			}
		}
	}
	return nil
}
