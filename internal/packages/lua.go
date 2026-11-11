package packages

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/templates"
)

// Limits on a Lua plugin (docs/adr/0038-lua-plugins.md).
const (
	MaxScriptBytes   = 256 << 10
	MaxActions       = 50
	MaxParams        = 20
	MaxParamText     = 500
	DefaultTimeout   = 10
	MaxTimeout       = 30
	PermChatSend     = "chat.send"
	PermEvents       = "events.publish"
	MaxEvents        = 20
	MaxEventValues   = 20
	paramTextDefault = 200
)

// Permissions are what a plugin may ask for, with what each lets it do, in
// the words the install screen shows.
var Permissions = map[string]string{
	PermChatSend: "Send messages in your chat, as the bot when one is signed in (at most 20 a minute)",
	PermEvents:   "Start your workflows with its own events (at most 10 a second)",
}

// EventType is the event type a plugin's event has: plugin.<id>.<name>,
// with the id's - as _ (event types are lowercase words and _).
func EventType(id, name string) string {
	return "plugin." + strings.ReplaceAll(id, "-", "_") + "." + name
}

// TemplateDir holds a plugin's templates, one JSON file each, in the
// gallery's format (D-071).
const TemplateDir = "templates/"

// isTemplate reports whether a package path is one of its templates.
func isTemplate(p string) bool {
	return strings.HasPrefix(p, TemplateDir) && path.Ext(p) == ".json" && !strings.Contains(strings.TrimPrefix(p, TemplateDir), "/")
}

// readTemplates reads and checks a plugin's templates, by path order.
func readTemplates(r *Read) ([]templates.Template, error) {
	var out []templates.Template
	seen := map[string]bool{}
	for _, p := range r.Paths() {
		if !isTemplate(p) {
			continue
		}
		t, err := templates.ParseAddOn(r.Files[p])
		if err != nil {
			return nil, invalid("%s: %s", p, strings.TrimPrefix(err.Error(), templates.ErrInvalid.Error()+": "))
		}
		if seen[t.ID] {
			return nil, invalid("%s: the template id %q is used twice", p, t.ID)
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	if len(out) > templates.MaxAddOnTemplates {
		return nil, invalid("a plugin has up to %d templates", templates.MaxAddOnTemplates)
	}
	return out, nil
}

// Templates reads a plugin version's templates: what Check showed, the
// gallery offers.
func (r *Read) Templates() []templates.Template {
	ts, _ := readTemplates(r)
	return ts
}

// luaTypes are the files a Lua plugin may have.
var luaTypes = []string{".lua", ".json", ".md", ".txt"}

// Lua is a Lua plugin's part of the manifest.
type Lua struct {
	Script      string             `json:"script"`
	Permissions []string           `json:"permissions,omitempty"`
	Timeout     int                `json:"timeout,omitempty"` // seconds a call may take; 0 is DefaultTimeout
	Actions     []Action           `json:"actions"`
	Settings    []overlays.Setting `json:"settings,omitempty"`
	// Events it can send, to start workflows (D-073): with events.publish.
	Events []Event `json:"events,omitempty"`
}

// Event is one kind of event a plugin sends, as plugin.<id>.<name>.
type Event struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Values are fields of its payload offered in messages as {name}.
	Values []EventValue `json:"values,omitempty"`
}

// EventValue is a field of an event's payload, as messages can include it.
type EventValue struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// Action is a step a plugin offers, as <id>.<name>.
type Action struct {
	Name        string  `json:"name"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Params      []Param `json:"params,omitempty"`
}

// Param is one of an action's values: text, number, switch or choice.
type Param struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required,omitempty"`
	Default  any      `json:"default,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Options  []string `json:"options,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

// TimeoutSeconds is how long a call may take.
func (l *Lua) TimeoutSeconds() int {
	if l.Timeout == 0 {
		return DefaultTimeout
	}
	return l.Timeout
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// checkLua checks a Lua plugin's part of the manifest.
func checkLua(m Manifest) error {
	l := m.Lua
	if l == nil {
		return invalid("a Lua plugin's manifest has a lua section")
	}
	if m.Theme != nil {
		return invalid("a Lua plugin has no theme section")
	}
	if checkPath(l.Script) != nil || path.Ext(l.Script) != ".lua" {
		return invalid("the plugin's script is a .lua file in the package")
	}
	seen := map[string]bool{}
	for _, p := range l.Permissions {
		if _, ok := Permissions[p]; !ok {
			return invalid("the permission %q isn't one this version of StreamGremlin has (it has: %s)", p, strings.Join(knownPermissions(), ", "))
		}
		if seen[p] {
			return invalid("the permission %q is there twice", p)
		}
		seen[p] = true
	}
	if l.Timeout != 0 && (l.Timeout < 1 || l.Timeout > MaxTimeout) {
		return invalid("the timeout is 1 to %d seconds", MaxTimeout)
	}
	if len(l.Actions) == 0 || len(l.Actions) > MaxActions {
		return invalid("a plugin offers 1 to %d steps (actions)", MaxActions)
	}
	names := map[string]bool{}
	for _, a := range l.Actions {
		if !namePattern.MatchString(a.Name) {
			return invalid("action %q: a name is up to 32 lowercase letters, digits and _, starting with a letter", a.Name)
		}
		if names[a.Name] {
			return invalid("action %q is there twice", a.Name)
		}
		names[a.Name] = true
		if strings.TrimSpace(a.Label) == "" || !plain(a.Label, 60) || strings.TrimSpace(a.Description) == "" || !plain(a.Description, 300) {
			return invalid("action %q: a label is up to 60 characters and a description up to 300, neither empty", a.Name)
		}
		if err := checkParams(a); err != nil {
			return err
		}
	}
	if err := checkSettings(l.Settings); err != nil {
		return err
	}
	return checkEvents(l)
}

// checkEvents: events need events.publish, and events.publish needs events.
func checkEvents(l *Lua) error {
	asks := slices.Contains(l.Permissions, PermEvents)
	switch {
	case len(l.Events) > 0 && !asks:
		return invalid("a plugin with events asks for the %s permission", PermEvents)
	case len(l.Events) == 0 && asks:
		return invalid("the %s permission is for a plugin with events: it has none", PermEvents)
	case len(l.Events) > MaxEvents:
		return invalid("a plugin has up to %d events", MaxEvents)
	}
	names := map[string]bool{}
	for _, e := range l.Events {
		if !namePattern.MatchString(e.Name) {
			return invalid("event %q: a name is up to 32 lowercase letters, digits and _, starting with a letter", e.Name)
		}
		if names[e.Name] {
			return invalid("event %q is there twice", e.Name)
		}
		names[e.Name] = true
		if strings.TrimSpace(e.Label) == "" || !plain(e.Label, 60) || strings.TrimSpace(e.Description) == "" || !plain(e.Description, 300) {
			return invalid("event %q: a label is up to 60 characters and a description up to 300, neither empty", e.Name)
		}
		if len(e.Values) > MaxEventValues {
			return invalid("event %q: up to %d values", e.Name, MaxEventValues)
		}
		vals := map[string]bool{}
		for _, v := range e.Values {
			if !namePattern.MatchString(v.Name) || vals[v.Name] || strings.TrimSpace(v.Label) == "" || !plain(v.Label, 60) {
				return invalid("event %q, value %q: each has a different name (lowercase letters, digits and _) and a label up to 60 characters", e.Name, v.Name)
			}
			vals[v.Name] = true
		}
	}
	return nil
}

func knownPermissions() []string {
	out := make([]string, 0, len(Permissions))
	for p := range Permissions {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func checkParams(a Action) error {
	if len(a.Params) > MaxParams {
		return invalid("action %q: up to %d values", a.Name, MaxParams)
	}
	seen := map[string]bool{}
	for _, p := range a.Params {
		where := "action " + a.Name + ", value " + p.Name
		if !namePattern.MatchString(p.Name) {
			return invalid("%s: a name is up to 32 lowercase letters, digits and _, starting with a letter", where)
		}
		if seen[p.Name] {
			return invalid("%s is there twice", where)
		}
		seen[p.Name] = true
		if strings.TrimSpace(p.Label) == "" || !plain(p.Label, 60) || !plain(p.Hint, 120) {
			return invalid("%s: a label is up to 60 characters, a hint up to 120", where)
		}
		switch p.Kind {
		case "text":
			if p.Min != nil || len(p.Options) > 0 || p.Max != nil && (*p.Max < 1 || *p.Max > MaxParamText || *p.Max != float64(int(*p.Max))) {
				return invalid("%s: a text's max is 1 to %d characters", where, MaxParamText)
			}
		case "number":
			if len(p.Options) > 0 || p.Min != nil && p.Max != nil && *p.Min > *p.Max {
				return invalid("%s: a number's min is at most its max, and it has no options", where)
			}
		case "switch":
			if p.Min != nil || p.Max != nil || len(p.Options) > 0 {
				return invalid("%s: a switch has no min, max or options", where)
			}
		case "choice":
			if p.Min != nil || p.Max != nil || len(p.Options) < 1 || len(p.Options) > 20 {
				return invalid("%s: a choice has 1 to 20 options", where)
			}
			opts := map[string]bool{}
			for _, o := range p.Options {
				if o == "" || !plain(o, 60) || opts[o] {
					return invalid("%s: each option is different text, up to 60 characters", where)
				}
				opts[o] = true
			}
		default:
			return invalid("%s: the kind is text, number, switch or choice", where)
		}
		if p.Default != nil {
			if err := p.Check(p.Default); err != nil {
				return invalid("%s: its default doesn't fit it", where)
			}
		}
	}
	return nil
}

// Check reports what's wrong with v as this value. Text may hold anything
// up to its max; it's checked after ${…} is filled in.
func (p Param) Check(v any) error {
	switch p.Kind {
	case "text":
		s, ok := v.(string)
		limit := paramTextDefault
		if p.Max != nil {
			limit = int(*p.Max)
		}
		if !ok || utf8.RuneCountInString(s) > limit {
			return invalid("%s is text, up to %d characters", p.Label, limit)
		}
	case "number":
		f, ok := number(v)
		if !ok || p.Min != nil && f < *p.Min || p.Max != nil && f > *p.Max {
			return invalid("%s is a number%s", p.Label, numberRange(p))
		}
	case "switch":
		if _, ok := v.(bool); !ok {
			return invalid("%s is on or off", p.Label)
		}
	case "choice":
		s, ok := v.(string)
		if !ok || !slices.Contains(p.Options, s) {
			return invalid("%s is one of %s", p.Label, strings.Join(p.Options, ", "))
		}
	}
	return nil
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}

func numberRange(p Param) string {
	switch {
	case p.Min != nil && p.Max != nil:
		return " from " + trimFloat(*p.Min) + " to " + trimFloat(*p.Max)
	case p.Min != nil:
		return " of at least " + trimFloat(*p.Min)
	case p.Max != nil:
		return " of at most " + trimFloat(*p.Max)
	}
	return ""
}

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// checkSettings checks settings a package declares, a theme's or a
// plugin's.
func checkSettings(settings []overlays.Setting) error {
	if len(settings) > MaxSettings {
		return invalid("a package has up to %d settings", MaxSettings)
	}
	seen := map[string]bool{}
	for _, s := range settings {
		if !keyPattern.MatchString(s.Key) {
			return invalid("setting %q: a key is up to 32 lowercase letters, digits and _, starting with a letter", s.Key)
		}
		if seen[s.Key] {
			return invalid("setting %q is there twice", s.Key)
		}
		seen[s.Key] = true
		for _, w := range strings.Split(s.Key, "_") {
			if slices.Contains(secretWords, w) {
				return invalid("setting %q: settings never hold secrets, so none is named like one", s.Key)
			}
		}
		if strings.TrimSpace(s.Label) == "" || !plain(s.Label, 60) || !plain(s.Hint, 120) {
			return invalid("setting %q: a label is up to 60 characters, a hint up to 120", s.Key)
		}
		if err := overlays.CheckSetting(s); err != nil {
			return invalid("%s", strings.TrimPrefix(err.Error(), overlays.ErrInvalid.Error()+": "))
		}
	}
	return nil
}
