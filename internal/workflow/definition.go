// Package workflow runs streamers' automations: when an event matches a
// trigger, run steps (docs/adr/0008-workflows.md). Definitions are
// versioned JSON; every event makes at most one job per workflow; every
// step is traced; test events never reach Twitch or OBS.
package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Schema is the definition format's name and version.
var Schema = brand.Format("workflow")

// Limits.
const (
	MaxDefinitionBytes = 256 << 10
	MaxDepth           = 8
	MaxStepsDefined    = 256
	MaxExprBytes       = 2000
	DefaultTimeout     = 5 * time.Minute
	MaxTimeout         = time.Hour
	DefaultMaxSteps    = 1000
	MaxMaxSteps        = 10000
	DefaultForeachMax  = 100
	MaxForeachMax      = 1000
	FinallyBudget      = 10 * time.Second
	MaxWhereAny        = 20 // values in one where list
)

// Definition is one workflow, as saved and exported.
type Definition struct {
	Schema      string    `json:"schema"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Triggers    []Trigger `json:"triggers"`
	Queue       Queue     `json:"queue,omitzero"`
	Timeout     string    `json:"timeout,omitempty"`
	MaxSteps    int       `json:"max_steps,omitempty"`
	Steps       []Step    `json:"steps"`
	Finally     []Step    `json:"finally,omitempty"`
}

// Trigger says which events start the workflow. All set fields must match.
type Trigger struct {
	// Type is an event type, or a pattern: "*" matches one segment and a
	// final "**" the rest ("chat.*.received", "broadcast.**").
	Type    string   `json:"type"`
	Sources []string `json:"sources,omitempty"`
	// DeliveryQuality limits to confirmed, estimated or synthetic events.
	// Unset accepts all; test (synthetic) events then run as dry runs.
	DeliveryQuality []events.Quality `json:"delivery_quality,omitempty"`
	// Where compares payload paths ("chatter.role") to exact JSON values. A
	// list matches any of its values: {"chatter.role": ["moderator",
	// "broadcaster"]} is "mods and you".
	Where map[string]json.RawMessage `json:"where,omitempty"`
}

// Queue policies.
const (
	QueueSerial   = "serial"   // one at a time, in order
	QueueParallel = "parallel" // up to Concurrency at once
	QueueDrop     = "drop"     // skip events while one runs or waits
	QueueReplace  = "replace"  // a new event cancels the running one
)

// Queue says what happens when events arrive faster than the workflow runs.
type Queue struct {
	Policy      string `json:"policy,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
	MaxPending  int    `json:"max_pending,omitempty"`
	// OnOverflow: "reject" (default) records the new job as rejected;
	// "drop_oldest" drops the oldest waiting one instead.
	OnOverflow string `json:"on_overflow,omitempty"`
}

// Step is one thing to do. Exactly one of Action, Set, Do, Foreach, Choose
// or Wait is set.
type Step struct {
	ID      string                     `json:"id,omitempty"`
	If      string                     `json:"if,omitempty"`
	Action  string                     `json:"action,omitempty"`
	With    map[string]json.RawMessage `json:"with,omitempty"`
	Timeout string                     `json:"timeout,omitempty"`
	OnError string                     `json:"on_error,omitempty"` // "fail" (default) or "continue"
	Set     map[string]string          `json:"set,omitempty"`
	Do      []Step                     `json:"do,omitempty"`
	Else    []Step                     `json:"else,omitempty"`
	Foreach string                     `json:"foreach,omitempty"`
	Steps   []Step                     `json:"steps,omitempty"` // foreach body
	Max     int                        `json:"max,omitempty"`
	Choose  []Branch                   `json:"choose,omitempty"`
	Wait    string                     `json:"wait,omitempty"`
}

// Branch is one weighted choice.
type Branch struct {
	Weight int    `json:"weight"`
	Steps  []Step `json:"steps"`
}

// Kind names a step's kind.
func (s Step) Kind() string {
	switch {
	case s.Action != "":
		return "action"
	case s.Set != nil:
		return "set"
	case s.Do != nil:
		return "do"
	case s.Foreach != "":
		return "foreach"
	case s.Choose != nil:
		return "choose"
	case s.Wait != "":
		return "wait"
	}
	return ""
}

// Problem is one thing wrong with a definition, at a JSON path.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// InvalidError lists everything wrong with a definition.
type InvalidError struct{ Problems []Problem }

func (e *InvalidError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.Path + ": " + p.Message
	}
	return "workflow: " + strings.Join(parts, "; ")
}

// ErrInvalid matches every *InvalidError.
var ErrInvalid = errors.New("workflow: invalid")

func (e *InvalidError) Is(target error) bool { return target == ErrInvalid }

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(\.[a-z0-9][a-z0-9_-]*)*$`)
	stepIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	varPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	typePattern   = regexp.MustCompile(`^([a-z][a-z0-9_]*|\*)(\.([a-z][a-z0-9_]*|\*))*(\.\*\*)?$`)
)

// Parse reads a definition strictly (unknown fields are errors), checks it
// against the actions available, and compiles every expression.
func Parse(raw []byte, actions *Registry) (*Definition, error) {
	if len(raw) > MaxDefinitionBytes {
		return nil, &InvalidError{[]Problem{{"$", "the definition is over 256 KiB"}}}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d Definition
	if err := dec.Decode(&d); err != nil {
		return nil, &InvalidError{[]Problem{{"$", "not a workflow definition: " + err.Error()}}}
	}
	if dec.More() {
		return nil, &InvalidError{[]Problem{{"$", "more than one JSON value"}}}
	}
	v := &validator{actions: actions, ids: map[string]string{}}
	v.definition(&d)
	if len(v.problems) > 0 {
		return nil, &InvalidError{v.problems}
	}
	return &d, nil
}

// Canonical is the definition's stored form; Checksum identifies it.
func (d *Definition) Canonical() ([]byte, string) {
	b, _ := json.Marshal(d)
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])
}

// TimeoutDuration is the workflow's time limit.
func (d *Definition) TimeoutDuration() time.Duration {
	if t, err := time.ParseDuration(d.Timeout); err == nil && t > 0 {
		return t
	}
	return DefaultTimeout
}

// StepBudget is the most steps one run may execute, loops included.
func (d *Definition) StepBudget() int {
	if d.MaxSteps > 0 {
		return d.MaxSteps
	}
	return DefaultMaxSteps
}

type validator struct {
	actions  *Registry
	problems []Problem
	ids      map[string]string // step id -> path
	count    int
}

func (v *validator) add(path, format string, a ...any) {
	v.problems = append(v.problems, Problem{Path: path, Message: fmt.Sprintf(format, a...)})
}

func (v *validator) definition(d *Definition) {
	if d.Schema != Schema {
		v.add("$.schema", "must be %q", Schema)
	}
	if !idPattern.MatchString(d.ID) || len(d.ID) > 64 {
		v.add("$.id", "lowercase words separated by dots, like chat.say-hello")
	}
	if strings.TrimSpace(d.Name) == "" || len(d.Name) > 100 {
		v.add("$.name", "a name of 1 to 100 characters")
	}
	if len(d.Triggers) == 0 {
		v.add("$.triggers", "at least one trigger")
	}
	for i, t := range d.Triggers {
		v.trigger(fmt.Sprintf("$.triggers[%d]", i), t)
	}
	v.queue(d.Queue)
	if d.Timeout != "" {
		if t, err := time.ParseDuration(d.Timeout); err != nil || t <= 0 || t > MaxTimeout {
			v.add("$.timeout", "a duration up to 1h, like 30s or 5m")
		}
	}
	if d.MaxSteps < 0 || d.MaxSteps > MaxMaxSteps {
		v.add("$.max_steps", "0 to %d", MaxMaxSteps)
	}
	if len(d.Steps) == 0 {
		v.add("$.steps", "at least one step")
	}
	v.steps("$.steps", d.Steps, 1)
	v.steps("$.finally", d.Finally, 1)
	if v.count > MaxStepsDefined {
		v.add("$", "%d steps; at most %d", v.count, MaxStepsDefined)
	}
}

func (v *validator) trigger(path string, t Trigger) {
	if !typePattern.MatchString(t.Type) || len(t.Type) > 128 || strings.Count(t.Type, ".") < 1 && t.Type != "**" {
		v.add(path+".type", "an event type like chat.command.received, or a pattern like broadcast.**")
	}
	for _, q := range t.DeliveryQuality {
		if q != events.Confirmed && q != events.Estimated && q != events.Synthetic {
			v.add(path+".delivery_quality", "confirmed, estimated or synthetic")
		}
	}
	for k, raw := range t.Where {
		var val any
		if json.Unmarshal(raw, &val) != nil {
			v.add(path+".where."+k, "a JSON value")
			continue
		}
		switch x := val.(type) {
		case string, float64, bool, nil:
		case []any:
			if len(x) == 0 || len(x) > MaxWhereAny {
				v.add(path+".where."+k, "a list of 1 to %d values, any of which matches", MaxWhereAny)
			}
			for _, e := range x {
				switch e.(type) {
				case string, float64, bool, nil:
				default:
					v.add(path+".where."+k, "a list of strings, numbers, true, false or null")
				}
			}
		default:
			v.add(path+".where."+k, "a string, number, true, false or null, or a list of them")
		}
	}
}

func (v *validator) queue(q Queue) {
	switch q.Policy {
	case "", QueueSerial, QueueDrop, QueueReplace:
		if q.Concurrency != 0 {
			v.add("$.queue.concurrency", "only for the parallel policy")
		}
	case QueueParallel:
		if q.Concurrency < 2 || q.Concurrency > 64 {
			v.add("$.queue.concurrency", "2 to 64")
		}
	default:
		v.add("$.queue.policy", "serial, parallel, drop or replace")
	}
	if q.MaxPending < 0 || q.MaxPending > 10000 {
		v.add("$.queue.max_pending", "0 to 10000 (0 means 100)")
	}
	if q.OnOverflow != "" && q.OnOverflow != "reject" && q.OnOverflow != "drop_oldest" {
		v.add("$.queue.on_overflow", "reject or drop_oldest")
	}
}

func (v *validator) steps(path string, steps []Step, depth int) {
	if depth > MaxDepth {
		v.add(path, "nested more than %d deep", MaxDepth)
		return
	}
	for i, s := range steps {
		v.step(fmt.Sprintf("%s[%d]", path, i), s, depth)
	}
}

func (v *validator) expr(path, src string) {
	if len(src) > MaxExprBytes {
		v.add(path, "an expression over %d bytes", MaxExprBytes)
		return
	}
	if _, err := compile(src); err != nil {
		v.add(path, "%s", err.Error())
	}
}

// template checks every ${…} inside a with-value.
func (v *validator) template(path string, raw json.RawMessage) {
	var val any
	if json.Unmarshal(raw, &val) != nil {
		v.add(path, "a JSON value")
		return
	}
	walkStrings(val, func(s string) {
		exprs, err := templateExprs(s)
		if err != nil {
			v.add(path, "%s", err.Error())
		}
		for _, e := range exprs {
			v.expr(path, e)
		}
	})
}

// literal checks a with-value written as plain text, without ${…}, against
// its parameter's choices and pattern, so the mistake shows when saving
// rather than mid-stream.
func (v *validator) literal(path string, p *Param, raw json.RawMessage) {
	var s string
	if p.Literal && (json.Unmarshal(raw, &s) != nil || strings.Contains(s, "${")) {
		v.add(path, "written out, not computed with ${…}")
		return
	}
	if json.Unmarshal(raw, &s) != nil || strings.Contains(s, "${") {
		return
	}
	switch {
	case p.Type == "choice" && !contains(p.Choices, s):
		v.add(path, "one of %s", strings.Join(p.Choices, ", "))
	case p.Type == "text" && p.Pattern != "" && !regexp.MustCompile(p.Pattern).MatchString(s):
		v.add(path, "%s", p.PatternHint)
	}
}

func (v *validator) step(path string, s Step, depth int) {
	v.count++
	kinds := 0
	for _, set := range []bool{s.Action != "", s.Set != nil, s.Do != nil, s.Foreach != "", s.Choose != nil, s.Wait != ""} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		v.add(path, "exactly one of action, set, do, foreach, choose or wait")
		return
	}
	if s.ID != "" {
		if !stepIDPattern.MatchString(s.ID) {
			v.add(path+".id", "lowercase letters, digits and _, starting with a letter")
		} else if other, dup := v.ids[s.ID]; dup {
			v.add(path+".id", "also used at %s", other)
		} else {
			v.ids[s.ID] = path
		}
	}
	if s.If != "" {
		v.expr(path+".if", s.If)
	}
	if s.OnError != "" && s.OnError != "fail" && s.OnError != "continue" {
		v.add(path+".on_error", "fail or continue")
	}
	if s.Else != nil && s.Do == nil {
		v.add(path+".else", "only with do")
	}
	if s.Else != nil && s.If == "" {
		v.add(path+".else", "needs an if")
	}
	if s.Steps != nil && s.Foreach == "" {
		v.add(path+".steps", "only with foreach")
	}
	if s.With != nil && s.Action == "" {
		v.add(path+".with", "only with action")
	}
	if s.Timeout != "" && s.Action == "" {
		v.add(path+".timeout", "only with action")
	}
	switch s.Kind() {
	case "action":
		a, ok := v.actions.Get(s.Action)
		if !ok {
			v.add(path+".action", "there's no action %q", s.Action)
			break
		}
		for _, p := range a.Params {
			if _, ok := s.With[p.Name]; p.Required && !ok {
				v.add(path+".with."+p.Name, "required: %s", p.Label)
			}
		}
		for name, raw := range s.With {
			if a.Param(name) == nil {
				v.add(path+".with."+name, "%s has no parameter %q", s.Action, name)
				continue
			}
			v.template(path+".with."+name, raw)
			v.literal(path+".with."+name, a.Param(name), raw)
		}
		if s.Timeout != "" {
			if t, err := time.ParseDuration(s.Timeout); err != nil || t <= 0 || t > MaxTimeout {
				v.add(path+".timeout", "a duration like 10s")
			}
		}
	case "set":
		if len(s.Set) == 0 {
			v.add(path+".set", "at least one variable")
		}
		for name, e := range s.Set {
			if !varPattern.MatchString(name) {
				v.add(path+".set."+name, "a variable name: lowercase letters, digits and _")
			}
			v.expr(path+".set."+name, e)
		}
	case "do":
		v.steps(path+".do", s.Do, depth+1)
		v.steps(path+".else", s.Else, depth+1)
	case "foreach":
		v.expr(path+".foreach", s.Foreach)
		if s.Max < 0 || s.Max > MaxForeachMax {
			v.add(path+".max", "0 to %d (0 means %d)", MaxForeachMax, DefaultForeachMax)
		}
		if len(s.Steps) == 0 {
			v.add(path+".steps", "at least one step to repeat")
		}
		v.steps(path+".steps", s.Steps, depth+1)
	case "choose":
		if len(s.Choose) < 1 {
			v.add(path+".choose", "at least one branch")
		}
		for i, b := range s.Choose {
			bp := fmt.Sprintf("%s.choose[%d]", path, i)
			if b.Weight < 1 || b.Weight > 1_000_000 {
				v.add(bp+".weight", "1 to 1000000")
			}
			v.steps(bp+".steps", b.Steps, depth+1)
		}
	case "wait":
		if t, err := time.ParseDuration(s.Wait); err != nil || t <= 0 || t > MaxTimeout {
			v.add(path+".wait", "a duration up to 1h, like 5s")
		}
	}
}

func walkStrings(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case []any:
		for _, e := range x {
			walkStrings(e, f)
		}
	case map[string]any:
		for _, e := range x {
			walkStrings(e, f)
		}
	}
}
