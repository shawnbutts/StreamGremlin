package overlays

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Looks for wheel overlays (docs/adr/0036-packages-plugins-and-themes.md).
// Classic is drawn by the overlay page itself and is the fallback; the
// others are themes: pages that run in a sandboxed frame from a second
// origin with no network, and animate to the result they're sent. The
// built-in ones are StreamGremlin's own code, in themes/; installed ones
// come from packages (internal/packages), and the Catalogue lists them.

// Setting kinds a theme's settings may have.
const (
	SettingText   = "text"
	SettingSwitch = "switch"
	SettingColour = "colour"
	SettingNumber = "number" // a whole number, Min to Max
	SettingChoice = "choice" // one of Options
)

// Setting is one of a theme's settings.
type Setting struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Default any    `json:"default"`
	Max     int    `json:"max,omitempty"` // a text's longest, in characters; a number's largest
	Min     int    `json:"min,omitempty"` // a number's smallest
	// Options are a choice's values, shown as they are.
	Options []string `json:"options,omitempty"`
	Hint    string   `json:"hint,omitempty"`
}

// Theme is a look a wheel overlay can have.
type Theme struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// OddsChecked: StreamGremlin's tests check this look shows the real
	// odds (wedges at their real size, or each choice's chance). Never
	// true for an installed theme: its drawing can't be checked.
	OddsChecked bool      `json:"odds_checked"`
	Settings    []Setting `json:"settings"`
	// An installed theme's package: its version and publisher, as stated
	// (not verified). Empty for the built-in looks.
	Version   string `json:"version,omitempty"`
	Publisher string `json:"publisher,omitempty"`

	dir   string   // a built-in look's folder in themes/
	base  string   // where its files are served, like /t/<id>/<version>/<digest>/
	entry string   // its page, under base
	cues  []string // the sounds it has beside its page (D-065)
}

// Installed reports whether a theme came from a package.
func (t Theme) Installed() bool { return t.dir == "" }

// soundsSetting turns a look's own sounds on and off (D-065).
var soundsSetting = Setting{Key: "sounds", Label: "Play the look's sounds", Kind: SettingSwitch, Default: true,
	Hint: "A sound you pick under Sounds plays instead of the look's"}

// InstalledTheme is the theme an installed package's version provides.
// settings are its manifest's, already checked; files are its paths, for
// finding its sounds beside its page.
func InstalledTheme(id, name, description, version, publisher, digest, entry string, settings []Setting, files []string) Theme {
	t := Theme{ID: id, Name: name, Description: description, Version: version, Publisher: publisher,
		Settings: append([]Setting(nil), settings...),
		base:     "/t/" + id + "/" + version + "/" + digest + "/", entry: entry}
	dir := path.Dir(entry)
	for _, cue := range ThemeCues {
		if slices.Contains(files, path.Join(dir, cue+".wav")) {
			t.cues = append(t.cues, cue)
		}
	}
	if len(t.cues) > 0 {
		t.Settings = append(t.Settings, soundsSetting)
	}
	return t
}

// PackageIDPattern is what an installed package's id looks like
// (docs/adr/0036-packages-plugins-and-themes.md).
const PackageIDPattern = `^[a-z][a-z0-9-]{2,39}$`

var installedID = regexp.MustCompile(PackageIDPattern)

// Catalogue is every look a wheel overlay can have besides Classic: the
// built-in ones and the installed ones. It's safe to share; the zero
// value and nil hold only the built-in looks.
type Catalogue struct {
	mu        sync.RWMutex
	installed map[string]Theme
}

// NewCatalogue returns a Catalogue with the built-in looks.
func NewCatalogue() *Catalogue { return &Catalogue{} }

// SetInstalled replaces the installed themes.
func (c *Catalogue) SetInstalled(themes []Theme) {
	m := map[string]Theme{}
	for _, t := range themes {
		m[t.ID] = t
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.installed = m
}

// Theme finds a look by id.
func (c *Catalogue) Theme(id string) (Theme, bool) {
	if t, ok := ThemeByID(id); ok || c == nil {
		return t, ok
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.installed[id]
	return t, ok
}

// All lists the built-in looks, then the installed ones by name.
func (c *Catalogue) All() []Theme {
	out := append([]Theme(nil), Themes...)
	if c == nil {
		return out
	}
	c.mu.RLock()
	var inst []Theme
	for _, t := range c.installed {
		inst = append(inst, t)
	}
	c.mu.RUnlock()
	sort.Slice(inst, func(i, j int) bool {
		if a, b := strings.ToLower(inst[i].Name), strings.ToLower(inst[j].Name); a != b {
			return a < b
		}
		return inst[i].ID < inst[j].ID
	})
	return append(out, inst...)
}

// serves reports whether files under /t/<id>/<version>/<digest>/ belong to
// an installed theme as it is now.
func (c *Catalogue) serves(id, version, digest string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.installed[id]
	return ok && t.base == "/t/"+id+"/"+version+"/"+digest+"/"
}

func commonSettings(accent string) []Setting {
	return []Setting{
		{Key: "title", Label: "Title", Kind: SettingText, Default: "", Max: 70, Hint: "Empty: the wheel's own title"},
		{Key: "show_title", Label: "Show the title", Kind: SettingSwitch, Default: true},
		{Key: "accent", Label: "Accent colour", Kind: SettingColour, Default: accent},
		soundsSetting,
	}
}

// Themes are the built-in looks besides Classic (whose id is "").
var Themes = builtin([]Theme{
	{ID: "builtin:workshop", Name: "Workshop", Description: "A brass and wood frame, with parchment and copper wedges.",
		OddsChecked: true, Settings: commonSettings("#e8b04e"), dir: "workshop"},
	{ID: "builtin:neon-arcade", Name: "Neon arcade", Description: "Glowing neon wedges on a dark arcade cabinet.",
		OddsChecked: true, Settings: commonSettings("#3cf2c8"), dir: "neon-arcade"},
	{ID: "builtin:selector-board", Name: "Selector board", Description: "Not a wheel: a list that rolls to the result, each choice with its chance.",
		OddsChecked: true, Settings: commonSettings("#9cf03b"), dir: "selector-board"},
})

func builtin(ts []Theme) []Theme {
	for i := range ts {
		ts[i].entry, ts[i].cues = "index.html", ThemeCues
	}
	return ts
}

// base is where a theme's files are served. A built-in look's path has
// the page version, which keeps an old page from loading new files.
func (t Theme) path() string {
	if t.dir != "" {
		return themePath(t.dir)
	}
	return t.base
}

// ThemeByID returns a built-in theme.
func ThemeByID(id string) (Theme, bool) {
	for _, t := range Themes {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}

// checkTheme reports what's wrong with a look's theme and its settings,
// against the looks in c (nil: the built-in ones). A look naming an
// installed theme passes when c is nil, if its id could be one: the store
// checks it against what's installed.
func (l Look) checkTheme(c *Catalogue) error {
	if l.Theme == "" {
		if len(l.ThemeSettings) > 0 {
			return invalid("the Classic look has no theme settings")
		}
		return nil
	}
	t, ok := c.Theme(l.Theme)
	if !ok {
		if c == nil && installedID.MatchString(l.Theme) {
			return checkSettingValues(l.ThemeSettings)
		}
		return invalid("there's no look %q", l.Theme)
	}
	for k, v := range l.ThemeSettings {
		s, ok := t.setting(k)
		if !ok {
			return invalid("%s has no setting %q", t.Name, k)
		}
		if err := s.check(v); err != nil {
			return err
		}
	}
	return nil
}

// checkSettingValues checks what any setting's value may be, before the
// theme is known: short, plain values.
func checkSettingValues(m map[string]any) error {
	if len(m) > 40 {
		return invalid("a look has up to 40 settings")
	}
	for _, v := range m {
		switch x := v.(type) {
		case bool, float64, int:
		case string:
			if utf8.RuneCountInString(x) > 200 || strings.IndexFunc(x, unicode.IsControl) >= 0 {
				return invalid("a look's setting is text up to 200 characters")
			}
		default:
			return invalid("a look's settings are text, numbers, colours, choices or on/off")
		}
	}
	return nil
}

func (t Theme) setting(key string) (Setting, bool) {
	for _, s := range t.Settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// Check reports what's wrong with v as this setting's value (a plugin's
// settings use the same kinds as a theme's).
func (s Setting) Check(v any) error { return s.check(v) }

// FillSettings is settings' values: each saved one that's still valid, the
// default for the rest.
func FillSettings(settings []Setting, saved map[string]any) map[string]any {
	return Theme{Settings: settings}.themeSettings(saved)
}

func (s Setting) check(v any) error {
	label := strings.ToLower(s.Label)
	switch s.Kind {
	case SettingText:
		x, ok := v.(string)
		if !ok || utf8.RuneCountInString(x) > s.Max || strings.IndexFunc(x, unicode.IsControl) >= 0 {
			return invalid("%s is text, up to %d characters", label, s.Max)
		}
	case SettingSwitch:
		if _, ok := v.(bool); !ok {
			return invalid("%s is on or off", label)
		}
	case SettingColour:
		x, ok := v.(string)
		if !ok || !colorPattern.MatchString(x) {
			return invalid("%s is a colour like #ffffff", label)
		}
	case SettingNumber:
		n, ok := wholeNumber(v)
		if !ok || n < s.Min || n > s.Max {
			return invalid("%s is a whole number from %d to %d", label, s.Min, s.Max)
		}
	case SettingChoice:
		x, ok := v.(string)
		if !ok || !slices.Contains(s.Options, x) {
			return invalid("%s is one of %s", label, strings.Join(s.Options, ", "))
		}
	default:
		return fmt.Errorf("overlays: setting %q has an unknown kind %q", s.Key, s.Kind)
	}
	return nil
}

// wholeNumber reads a whole number as JSON gives it (a float64) or Go does.
func wholeNumber(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > 1e9 {
			return 0, false
		}
		return int(x), true
	}
	return 0, false
}

// CheckSetting reports what's wrong with a setting a package declares,
// default included.
func CheckSetting(s Setting) error {
	switch s.Kind {
	case SettingText:
		if s.Max < 1 || s.Max > 200 {
			return invalid("setting %q: a text's max is 1 to 200 characters", s.Key)
		}
	case SettingNumber:
		if s.Min >= s.Max || s.Min < -1e6 || s.Max > 1e6 {
			return invalid("setting %q: a number's min is below its max, both within a million", s.Key)
		}
	case SettingChoice:
		if len(s.Options) < 1 || len(s.Options) > 20 {
			return invalid("setting %q: a choice has 1 to 20 options", s.Key)
		}
		seen := map[string]bool{}
		for _, o := range s.Options {
			if o == "" || utf8.RuneCountInString(o) > 60 || strings.IndexFunc(o, unicode.IsControl) >= 0 || seen[o] {
				return invalid("setting %q: each option is different text, up to 60 characters", s.Key)
			}
			seen[o] = true
		}
	case SettingSwitch, SettingColour:
	default:
		return invalid("setting %q: the kind is text, number, switch, colour or choice", s.Key)
	}
	if s.Kind != SettingText && s.Kind != SettingNumber && s.Max != 0 || s.Kind != SettingNumber && s.Min != 0 || s.Kind != SettingChoice && len(s.Options) > 0 {
		return invalid("setting %q: min, max and options are for the kinds that have them", s.Key)
	}
	if err := s.check(s.Default); err != nil {
		return invalid("setting %q: its default doesn't fit it", s.Key)
	}
	return nil
}

// themeSettings is a theme's settings with the defaults filled in.
func (t Theme) themeSettings(saved map[string]any) map[string]any {
	out := map[string]any{}
	for _, s := range t.Settings {
		out[s.Key] = s.Default
		if v, ok := saved[s.Key]; ok && s.check(v) == nil {
			out[s.Key] = v
		}
	}
	return out
}

// ThemeView is what a page is told about its overlay's theme.
type ThemeView struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Settings    map[string]any `json:"settings"`
	OddsChecked bool           `json:"odds_checked"`
	// Sounds are the look's own sounds (D-065), by cue (start, end,
	// cheer), when its "sounds" setting is on. The overlay page plays
	// them, as it does the streamer's: a sound picked for a cue wins.
	Sounds map[string]string `json:"sounds,omitempty"`
	// AddOn: an installed look, someone else's code. The page only frames
	// one while OBS gives the browser source no page permissions (D-067).
	AddOn bool `json:"add_on,omitempty"`
}

// ThemeCues are the moments a look's sounds play, each a file <cue>.wav
// in the look's folder (made by internal/overlays/themesounds).
var ThemeCues = []string{"start", "end", "cheer"}

// themeView is the theme a wheel overlay shows, or nil for Classic (or
// when themes aren't served, or its theme isn't installed any more).
func themeView(o Overlay, origin string, c *Catalogue) *ThemeView {
	if o.Kind != KindWheel || o.Look.Theme == "" || origin == "" {
		return nil
	}
	t, ok := c.Theme(o.Look.Theme)
	if !ok {
		return nil
	}
	v := &ThemeView{ID: t.ID, Name: t.Name, URL: origin + t.path() + t.entry,
		Settings: t.themeSettings(o.Look.ThemeSettings), OddsChecked: t.OddsChecked, AddOn: t.Installed()}
	if v.Settings["sounds"] == true && len(t.cues) > 0 {
		v.Sounds = map[string]string{}
		dir := path.Dir(t.entry)
		for _, cue := range t.cues {
			v.Sounds[cue] = origin + t.path() + path.Join(dir, cue+".wav")
		}
	}
	return v
}

// themePath is where a built-in theme's files are served: the page
// version keeps an old page from loading a new theme's files. Files the
// looks share are in shared/, beside them.
func themePath(dir string) string { return "/t/builtin/" + PageVersion + "/" + dir + "/" }
