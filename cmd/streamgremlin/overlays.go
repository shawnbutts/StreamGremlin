package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/overlays"
)

const overlaysUsage = `usage:
  streamgremlin overlays [-json]          list your overlays (links aren't listed)
  streamgremlin overlays add [-giveaway | -wheel | -song | -timer] NAME
                                          add an alert overlay (or a giveaway overlay, which shows
                                          the running giveaway and rolls to each winner, a wheel overlay,
                                          which spins every wheel, or a Now playing overlay, which shows
                                          the song a music player writes to a file; or, with -timer, a clock,
                                          countdown or stopwatch), and print its link
  streamgremlin overlays link NAME        print its link, to paste into OBS
  streamgremlin overlays new-link NAME    replace its link; the old one stops working
  streamgremlin overlays look NAME [flags]   change its look; see overlays look -h. For example:
                                          -position bottom-left -animation unfold -seconds 8
                                          -background #6a00ff -gradient #ff0080 -corners 24 -border 2
                                          -font-name "Comic Neue" -size 120 -outline 2 -text-shadow 50
                                          giveaway and wheel overlays: -start-sound drumroll -end-sound clunk
                                          -cheer-sound cheer
                                          clock and timer overlays: -mode countdown -label "Starting soon"
                                          -length 5m -end-text "Starting now!" -auto-start, or -mode clock -24h -date short
                                          Now playing overlays: -song-file /path/song.txt -art-file /path/art.jpg
                                          -paused-text Paused -song-hides
                                          wheel overlays: -look workshop (or classic, neon-arcade,
                                          selector-board) -look-title "Pick a game" -accent #e8b04e
                                          -look-sounds=false (its own sounds, where none is picked);
                                          an installed look: -look ID -set KEY=VALUE (see packages)
  streamgremlin overlays timer NAME start [-minutes N | -until 19:00] | pause | reset | add -minutes N
                                          start, pause, reset or add time to a countdown or stopwatch
  streamgremlin overlays test NAME        show a sample alert (or giveaway and draw) in OBS
  streamgremlin overlays delete NAME      delete it; its link stops working

Each overlay is one browser source in OBS. Keep links private: anyone with
one can see that overlay. Positions: top-left, top, top-right, center,
bottom-left, bottom, bottom-right.`

type overlayList struct {
	Overlays []struct {
		overlays.Overlay
		Pages    int                  `json:"pages"`
		Previews int                  `json:"previews"`
		Song     *overlays.SongStatus `json:"song,omitempty"`
		Timer    *overlays.TimerState `json:"timer,omitempty"`
	} `json:"overlays"`
	Server     string           `json:"server"`
	ServerNote string           `json:"server_note"`
	Themes     []overlays.Theme `json:"themes"`
}

func overlaysCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, overlaysUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := func(name string) string { return "/v1/overlays/" + url.PathEscape(name) }
	usage := func() int { fmt.Fprintln(stderr, overlaysUsage); return 2 }
	const keepPrivate = "Paste it into OBS as a browser source (1920 by 1080). Keep it private: anyone with it can see this overlay."
	if len(args) > 0 {
		switch args[0] {
		case "add":
			fs := flag.NewFlagSet("overlays add", flag.ContinueOnError)
			fs.SetOutput(stderr)
			giveaway := fs.Bool("giveaway", false, "a giveaway overlay")
			wheel := fs.Bool("wheel", false, "a wheel overlay")
			song := fs.Bool("song", false, "a Now playing overlay: the song a music player writes to a file")
			timer := fs.Bool("timer", false, "a clock and timer overlay: a clock, a countdown or a stopwatch")
			if fs.Parse(args[1:]) != nil || fs.NArg() != 1 {
				return usage()
			}
			kind := overlays.KindAlert
			if *giveaway {
				kind = overlays.KindGiveaway
			}
			if *wheel {
				kind = overlays.KindWheel
			}
			if *song {
				kind = overlays.KindSong
			}
			if *timer {
				kind = overlays.KindTimer
			}
			var out struct {
				Overlay overlays.Overlay `json:"overlay"`
				URL     string           `json:"url"`
			}
			if code := apiExit(c.Do(ctx, "POST", "/v1/overlays", map[string]string{"name": fs.Arg(0), "kind": kind}, &out), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Added %s.\n%s\n%s\n", out.Overlay.Name, out.URL, keepPrivate)
			return 0
		case "link", "new-link":
			if len(args) != 2 {
				return usage()
			}
			method := "GET"
			if args[0] == "new-link" {
				method = "POST"
			}
			var out struct {
				URL string `json:"url"`
			}
			if code := apiExit(c.Do(ctx, method, path(args[1])+"/link", nil, &out), stderr); code != 0 {
				return code
			}
			if method == "POST" {
				fmt.Fprintln(stdout, "The old link stopped working. The new one:")
			}
			fmt.Fprintf(stdout, "%s\n%s\n", out.URL, keepPrivate)
			return 0
		case "look":
			if len(args) < 2 {
				return usage()
			}
			var o overlays.Overlay
			var list overlayList
			if code := apiExit(c.Do(ctx, "GET", "/v1/overlays", nil, &list), stderr); code != 0 {
				return code
			}
			found := false
			for _, x := range list.Overlays {
				if x.Name == args[1] {
					o, found = x.Overlay, true
				}
			}
			if !found {
				fmt.Fprintf(stderr, "There's no overlay %q.\n", args[1])
				return 1
			}
			l := o.Look
			fs := flag.NewFlagSet("overlays look", flag.ContinueOnError)
			fs.SetOutput(stderr)
			fs.StringVar(&l.Position, "position", l.Position, "where alerts appear")
			fs.StringVar(&l.Animation, "animation", l.Animation, "slide, pop or unfold (the picture, then the words)")
			fs.StringVar(&l.TextColor, "text-color", l.TextColor, "#RRGGBB")
			fs.StringVar(&l.Background, "background", l.Background, "#RRGGBB")
			fs.IntVar(&l.Opacity, "opacity", l.Opacity, "the background's, 0 to 100")
			fs.IntVar(&l.Seconds, "seconds", l.Seconds, "how long an alert stays, 2 to 60")
			fs.IntVar(&l.Pace, "pace", l.Pace, "how long the animation takes, percent of the usual: 50 (fast) to 300 (slow)")
			fs.StringVar(&l.Background2, "gradient", l.Background2, "#RRGGBB: the gradient's second color (none for one color)")
			fs.IntVar(&l.GradientAngle, "gradient-angle", l.GradientAngle, "0 to 359 degrees")
			fs.IntVar(&l.Radius, "corners", l.Radius, "corner roundness, 0 to 48")
			fs.IntVar(&l.BorderWidth, "border", l.BorderWidth, "the box's outline, 0 to 12")
			fs.StringVar(&l.BorderColor, "border-color", l.BorderColor, "#RRGGBB")
			fs.IntVar(&l.Shadow, "shadow", l.Shadow, "the box's drop shadow, 0 to 100")
			fs.StringVar(&l.ShadowColor, "shadow-color", l.ShadowColor, "#RRGGBB")
			fs.StringVar(&l.Font, "font", l.Font, strings.Join(overlays.Fonts, ", "))
			fs.StringVar(&l.FontName, "font-name", l.FontName, "a font installed on this computer, with -font custom")
			fs.IntVar(&l.TextSize, "size", l.TextSize, "text size, 50 to 200 percent")
			fs.StringVar(&l.TitleColor, "title-color", l.TitleColor, "#RRGGBB")
			fs.IntVar(&l.TextOutline, "outline", l.TextOutline, "around the letters, 0 to 6")
			fs.StringVar(&l.OutlineColor, "outline-color", l.OutlineColor, "#RRGGBB")
			fs.IntVar(&l.TextShadow, "text-shadow", l.TextShadow, "the words' drop shadow, 0 to 100")
			fs.StringVar(&l.StartSound, "start-sound", l.StartSound, "giveaway and wheel overlays: a library sound as a roll or spin starts (none for no sound)")
			fs.IntVar(&l.StartVolume, "start-volume", l.StartVolume, "its volume, 0 to 100")
			fs.StringVar(&l.EndSound, "end-sound", l.EndSound, "giveaway and wheel overlays: a library sound as it lands (none for no sound)")
			fs.IntVar(&l.EndVolume, "end-volume", l.EndVolume, "its volume, 0 to 100")
			fs.StringVar(&l.CheerSound, "cheer-sound", l.CheerSound, "giveaway and wheel overlays: a library sound when the result shows (none for no sound)")
			fs.StringVar(&l.SongFile, "song-file", l.SongFile, "Now playing overlays: the full path of the .txt file your music player writes the song to (none for none)")
			fs.StringVar(&l.ArtFile, "art-file", l.ArtFile, "Now playing overlays: the full path of the .jpg or .png your music player writes the cover to (none for none)")
			fs.StringVar(&l.PausedText, "paused-text", l.PausedText, "Now playing overlays: what your music player writes when paused (none for none)")
			fs.BoolVar(&l.SongHides, "song-hides", l.SongHides, "Now playing overlays: show each song for -seconds, then hide")
			fs.StringVar(&l.TimerMode, "mode", l.TimerMode, "clock and timer overlays: clock, countdown or stopwatch")
			fs.StringVar(&l.TimerLabel, "label", l.TimerLabel, "clock and timer overlays: a label above it, like Starting soon (none for none)")
			fs.StringVar(&l.TimerEndText, "end-text", l.TimerEndText, "countdowns: what it says at zero, like Starting now! (none for 0:00)")
			length := fs.Duration("length", time.Duration(l.TimerLength)*time.Second, "countdowns: how long, like 5m or 1h30m")
			fs.BoolVar(&l.Clock24, "24h", l.Clock24, "clocks: a 24-hour clock")
			fs.BoolVar(&l.ClockSeconds, "clock-seconds", l.ClockSeconds, "clocks: show seconds")
			fs.StringVar(&l.ClockDate, "date", l.ClockDate, "clocks: the date under the time: short, long or none")
			fs.BoolVar(&l.TimerAutoStart, "auto-start", l.TimerAutoStart, "countdowns and stopwatches: start afresh whenever OBS loads the overlay")
			fs.IntVar(&l.CheerVolume, "cheer-volume", l.CheerVolume, "its volume, 0 to 100")
			lookName := strings.TrimPrefix(l.Theme, "builtin:")
			var sets setFlags
			if lookName == "" {
				lookName = "classic"
			}
			fs.StringVar(&lookName, "look", lookName, "wheel overlays: classic, workshop, neon-arcade, selector-board, or an installed look's id; clock and timer overlays: classic or an installed look's id")
			fs.Var(&sets, "set", "an overlay with a look: one of its settings, KEY=VALUE (repeatable; see packages)")
			lookTitle := fs.String("look-title", "", "wheel overlays with a look: its title (empty for the wheel's own)")
			lookShowTitle := fs.Bool("look-show-title", true, "wheel overlays with a look: show the title")
			accent := fs.String("accent", "", "wheel overlays with a look: its accent colour, #RRGGBB")
			lookSounds := fs.Bool("look-sounds", true, "wheel overlays with a look: play the look's own sounds where no sound is picked")
			if err := fs.Parse(args[2:]); err != nil || fs.NArg() > 0 {
				return usage()
			}
			theme := ""
			if lookName != "classic" {
				theme = "builtin:" + lookName
				if _, ok := overlays.ThemeByID(theme); !ok {
					theme = lookName // an installed look, by its id
				}
			}
			var chosen *overlays.Theme
			for i := range list.Themes {
				if list.Themes[i].ID == theme {
					chosen = &list.Themes[i]
				}
			}
			settings := map[string]any{}
			if theme == l.Theme {
				for k, v := range l.ThemeSettings {
					settings[k] = v
				}
			}
			fs.Visit(func(f *flag.Flag) {
				switch f.Name {
				case "look-title":
					settings["title"] = *lookTitle
				case "look-show-title":
					settings["show_title"] = *lookShowTitle
				case "accent":
					settings["accent"] = *accent
				case "look-sounds":
					settings["sounds"] = *lookSounds
				}
			})
			for _, kv := range sets {
				k, v, _ := strings.Cut(kv, "=")
				val, err := settingValue(chosen, k, v)
				if err != nil {
					fmt.Fprintln(stderr, err)
					return 2
				}
				settings[k] = val
			}
			l.Theme, l.ThemeSettings = theme, settings
			if len(settings) == 0 {
				l.ThemeSettings = nil
			}
			if l.Background2 == "none" {
				l.Background2 = ""
			}
			if l.StartSound == "none" {
				l.StartSound = ""
			}
			if l.EndSound == "none" {
				l.EndSound = ""
			}
			if l.CheerSound == "none" {
				l.CheerSound = ""
			}
			l.TimerLength = int(length.Round(time.Second) / time.Second)
			for _, p := range []*string{&l.SongFile, &l.ArtFile, &l.PausedText, &l.TimerLabel, &l.TimerEndText, &l.ClockDate} {
				if *p == "none" {
					*p = ""
				}
			}
			if l.FontName != "" && l.Font != "custom" {
				l.Font = "custom" // naming a font means using it
			}
			if code := apiExit(c.Do(ctx, "PUT", path(o.Name), map[string]any{"look": l}, &o), stderr); code != 0 {
				return code
			}
			if o.Kind == overlays.KindWheel || o.Kind == overlays.KindTimer {
				name := "Classic"
				for _, t := range list.Themes {
					if t.ID == o.Look.Theme {
						name = t.Name
					}
				}
				fmt.Fprintf(stdout, "%s: the %s look.\n", o.Name, name)
				return 0
			}
			fmt.Fprintf(stdout, "%s: %s, %s, %d seconds.\n", o.Name, o.Look.Position, o.Look.Animation, o.Look.Seconds)
			return 0
		case "timer":
			return overlayTimerCmd(ctx, c, path, args[1:], stdout, stderr, usage)
		case "test":
			if len(args) != 2 {
				return usage()
			}
			if code := apiExit(c.Do(ctx, "POST", path(args[1])+"/test", nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintln(stdout, "A sample is showing in OBS.")
			return 0
		case "delete":
			if len(args) != 2 {
				return usage()
			}
			if code := apiExit(c.Do(ctx, "DELETE", path(args[1]), nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Deleted %s. Its link no longer works.\n", args[1])
			return 0
		}
	}
	fs := flag.NewFlagSet("overlays", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return usage()
	}
	var list overlayList
	if code := apiExit(c.Do(ctx, "GET", "/v1/overlays", nil, &list), stderr); code != 0 {
		return code
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return 0
	}
	if len(list.Overlays) == 0 {
		fmt.Fprintln(stdout, "No overlays yet. Add one with: streamgremlin overlays add alerts")
	}
	for _, o := range list.Overlays {
		open := "not open in OBS"
		if o.Pages > 0 {
			open = fmt.Sprintf("open in %d page(s)", o.Pages)
		}
		fmt.Fprintf(stdout, "%-24s %-8s %-13s %s\n", o.Name, o.Kind, o.Look.Position, open)
		if o.Kind == overlays.KindTimer && o.Look.TimerMode != "" && o.Look.TimerMode != overlays.TimerClock {
			switch t := o.Timer; {
			case t == nil:
				fmt.Fprintf(stdout, "%-24s %s, at its start\n", "", o.Look.TimerMode)
			case t.Ended:
				fmt.Fprintf(stdout, "%-24s %s, ended\n", "", o.Look.TimerMode)
			case t.Running:
				fmt.Fprintf(stdout, "%-24s %s, running\n", "", o.Look.TimerMode)
			default:
				fmt.Fprintf(stdout, "%-24s %s, paused at %s\n", "", o.Look.TimerMode, (time.Duration(t.BaseMS) * time.Millisecond).Round(time.Second))
			}
		}
		if o.Song != nil && o.Song.Open {
			switch {
			case o.Song.Problem != "":
				fmt.Fprintf(stdout, "%-24s %s\n", "", o.Song.Problem)
			case o.Song.Song.Playing:
				fmt.Fprintf(stdout, "%-24s showing %s\n", "", strings.TrimSuffix(o.Song.Song.Title+" by "+o.Song.Song.Artist, " by "))
			default:
				fmt.Fprintf(stdout, "%-24s nothing playing\n", "")
			}
		}
	}
	if list.Server == "" {
		fmt.Fprintln(stdout, "\nThe overlay server is off.")
	} else if list.ServerNote != "" {
		fmt.Fprintln(stdout, "\n"+list.ServerNote)
	}
	return 0
}

// setFlags collects repeated -set KEY=VALUE flags.
type setFlags []string

func (f *setFlags) String() string { return strings.Join(*f, " ") }
func (f *setFlags) Set(v string) error {
	if !strings.Contains(v, "=") {
		return fmt.Errorf("a setting is KEY=VALUE")
	}
	*f = append(*f, v)
	return nil
}

// settingValue reads a -set value as its setting's kind wants it; the
// engine checks it.
func settingValue(t *overlays.Theme, key, v string) (any, error) {
	if t == nil {
		return nil, fmt.Errorf("-set is for a look other than Classic")
	}
	for _, s := range t.Settings {
		if s.Key != key {
			continue
		}
		switch s.Kind {
		case overlays.SettingSwitch:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("%s is on or off: true or false", key)
			}
			return b, nil
		case overlays.SettingNumber:
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("%s is a whole number", key)
			}
			return n, nil
		}
		return v, nil
	}
	var keys []string
	for _, s := range t.Settings {
		keys = append(keys, s.Key)
	}
	return nil, fmt.Errorf("%s has no setting %q; its settings: %s", t.Name, key, strings.Join(keys, ", "))
}

// overlayTimerCmd starts, pauses, resets or adds time to a countdown or
// stopwatch overlay (D-092).
func overlayTimerCmd(ctx context.Context, c *client.Client, path func(string) string, args []string, stdout, stderr io.Writer, usage func() int) int {
	if len(args) < 2 {
		return usage()
	}
	name, action := args[0], args[1]
	fs := flag.NewFlagSet("overlays timer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	minutes := fs.Float64("minutes", 0, "start: count down for this long; add: this much longer")
	until := fs.String("until", "", "start: count down until this time, like 19:00")
	if fs.Parse(args[2:]) != nil || fs.NArg() > 0 {
		return usage()
	}
	op := overlays.TimerOp{Action: action, Seconds: int(math.Round(*minutes * 60)), Until: *until}
	var st overlays.TimerState
	if code := apiExit(c.Do(ctx, "POST", path(name)+"/timer", op, &st), stderr); code != 0 {
		return code
	}
	left := time.Duration(st.BaseMS) * time.Millisecond
	switch {
	case st.Running:
		fmt.Fprintf(stdout, "%s is running, from %s.\n", name, left.Round(time.Second))
	default:
		fmt.Fprintf(stdout, "%s is stopped at %s.\n", name, left.Round(time.Second))
	}
	return 0
}
