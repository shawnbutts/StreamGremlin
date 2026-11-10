package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
)

const overlaysUsage = `usage:
  streamgremlin overlays [-json]          list your overlays (links aren't listed)
  streamgremlin overlays add [-giveaway] NAME
                                          add an alert overlay (or a giveaway overlay, which shows
                                          the running giveaway and rolls to each winner), and print its link
  streamgremlin overlays link NAME        print its link, to paste into OBS
  streamgremlin overlays new-link NAME    replace its link; the old one stops working
  streamgremlin overlays look NAME [flags]   change its look; see overlays look -h. For example:
                                          -position bottom-left -animation unfold -seconds 8
                                          -background #6a00ff -gradient #ff0080 -corners 24 -border 2
                                          -font-name "Comic Neue" -size 120 -outline 2 -text-shadow 50
  streamgremlin overlays test NAME        show a sample alert (or giveaway and draw) in OBS
  streamgremlin overlays delete NAME      delete it; its link stops working

Each overlay is one browser source in OBS. Keep links private: anyone with
one can see that overlay. Positions: top-left, top, top-right, center,
bottom-left, bottom, bottom-right.`

type overlayList struct {
	Overlays []struct {
		overlays.Overlay
		Pages    int `json:"pages"`
		Previews int `json:"previews"`
	} `json:"overlays"`
	Server     string `json:"server"`
	ServerNote string `json:"server_note"`
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
			if fs.Parse(args[1:]) != nil || fs.NArg() != 1 {
				return usage()
			}
			kind := overlays.KindAlert
			if *giveaway {
				kind = overlays.KindGiveaway
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
			if err := fs.Parse(args[2:]); err != nil || fs.NArg() > 0 {
				return usage()
			}
			if l.Background2 == "none" {
				l.Background2 = ""
			}
			if l.FontName != "" && l.Font != "custom" {
				l.Font = "custom" // naming a font means using it
			}
			if code := apiExit(c.Do(ctx, "PUT", path(o.Name), map[string]any{"look": l}, &o), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "%s: %s, %s, %d seconds.\n", o.Name, o.Look.Position, o.Look.Animation, o.Look.Seconds)
			return 0
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
	}
	if list.Server == "" {
		fmt.Fprintln(stdout, "\nThe overlay server is off.")
	} else if list.ServerNote != "" {
		fmt.Fprintln(stdout, "\n"+list.ServerNote)
	}
	return 0
}
