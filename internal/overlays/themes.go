package overlays

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Looks for wheel overlays (docs/adr/0036-packages-plugins-and-themes.md).
// Classic is drawn by the overlay page itself and is the fallback; the
// others are themes: pages that run in a sandboxed frame from a second
// origin with no network, and animate to the result they're sent. The
// built-in ones are StreamGremlin's own code, in themes/.

// Setting kinds a theme's settings may have.
const (
	SettingText   = "text"
	SettingSwitch = "switch"
	SettingColour = "colour"
)

// Setting is one of a theme's settings.
type Setting struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Default any    `json:"default"`
	Max     int    `json:"max,omitempty"` // a text's longest, in characters
	Hint    string `json:"hint,omitempty"`
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
	dir         string    // its folder in themes/
}

func commonSettings(accent string) []Setting {
	return []Setting{
		{Key: "title", Label: "Title", Kind: SettingText, Default: "", Max: 70, Hint: "Empty: the wheel's own title"},
		{Key: "show_title", Label: "Show the title", Kind: SettingSwitch, Default: true},
		{Key: "accent", Label: "Accent colour", Kind: SettingColour, Default: accent},
		{Key: "sounds", Label: "Play the look's sounds", Kind: SettingSwitch, Default: true,
			Hint: "A sound you pick under Sounds plays instead of the look's"},
	}
}

// Themes are the built-in looks besides Classic (whose id is "").
var Themes = []Theme{
	{ID: "builtin:workshop", Name: "Workshop", Description: "A brass and wood frame, with parchment and copper wedges.",
		OddsChecked: true, Settings: commonSettings("#e8b04e"), dir: "workshop"},
	{ID: "builtin:neon-arcade", Name: "Neon arcade", Description: "Glowing neon wedges on a dark arcade cabinet.",
		OddsChecked: true, Settings: commonSettings("#3cf2c8"), dir: "neon-arcade"},
	{ID: "builtin:selector-board", Name: "Selector board", Description: "Not a wheel: a list that rolls to the result, each choice with its chance.",
		OddsChecked: true, Settings: commonSettings("#9cf03b"), dir: "selector-board"},
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

// checkTheme reports what's wrong with a look's theme and its settings.
func (l Look) checkTheme() error {
	if l.Theme == "" {
		if len(l.ThemeSettings) > 0 {
			return invalid("the Classic look has no theme settings")
		}
		return nil
	}
	t, ok := ThemeByID(l.Theme)
	if !ok {
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

func (t Theme) setting(key string) (Setting, bool) {
	for _, s := range t.Settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

func (s Setting) check(v any) error {
	switch s.Kind {
	case SettingText:
		x, ok := v.(string)
		if !ok || utf8.RuneCountInString(x) > s.Max || strings.IndexFunc(x, unicode.IsControl) >= 0 {
			return invalid("%s is text, up to %d characters", strings.ToLower(s.Label), s.Max)
		}
	case SettingSwitch:
		if _, ok := v.(bool); !ok {
			return invalid("%s is on or off", strings.ToLower(s.Label))
		}
	case SettingColour:
		x, ok := v.(string)
		if !ok || !colorPattern.MatchString(x) {
			return invalid("%s is a colour like #ffffff", strings.ToLower(s.Label))
		}
	default:
		return fmt.Errorf("overlays: setting %q has an unknown kind %q", s.Key, s.Kind)
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
}

// ThemeCues are the moments a look's sounds play, each a file <cue>.wav
// in the look's folder (made by internal/overlays/themesounds).
var ThemeCues = []string{"start", "end", "cheer"}

// themeView is the theme a wheel overlay shows, or nil for Classic (or
// when themes aren't served).
func themeView(o Overlay, origin string) *ThemeView {
	if o.Kind != KindWheel || o.Look.Theme == "" || origin == "" {
		return nil
	}
	t, ok := ThemeByID(o.Look.Theme)
	if !ok {
		return nil
	}
	v := &ThemeView{ID: t.ID, Name: t.Name, URL: origin + themePath(t.dir) + "index.html",
		Settings: t.themeSettings(o.Look.ThemeSettings), OddsChecked: t.OddsChecked}
	if v.Settings["sounds"] == true {
		v.Sounds = map[string]string{}
		for _, cue := range ThemeCues {
			v.Sounds[cue] = origin + themePath(t.dir) + cue + ".wav"
		}
	}
	return v
}

// themePath is where a built-in theme's files are served: the page
// version keeps an old page from loading a new theme's files. Files the
// looks share are in shared/, beside them.
func themePath(dir string) string { return "/t/builtin/" + PageVersion + "/" + dir + "/" }
