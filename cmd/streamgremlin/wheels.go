package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/wheels"
)

const wheelsUsage = `usage:
  streamgremlin wheels                       your wheels with their odds, and recent spins
  streamgremlin wheels add NAME -title T -choice LABEL[:WEIGHT[:#COLOR]] -choice … [-seconds 6]
                                             add a wheel; a weight of 2 is twice as likely as 1
  streamgremlin wheels set NAME -title T -choice … [-seconds 6]
                                             replace its title and choices
  streamgremlin wheels delete NAME           delete it; its spins are kept
  streamgremlin wheels spin NAME             spin it, and wait until it stops
  streamgremlin wheels verify SPIN           check a spin's record

Spins are drawn by StreamGremlin with a secure random source and recorded.
A wheel overlay in OBS spins to the result, and draws each wedge at its
real size.`

// choiceFlags collects -choice LABEL[:WEIGHT[:#COLOR]].
type choiceFlags []wheels.Choice

func (c *choiceFlags) String() string { return fmt.Sprint(len(*c)) }

func (c *choiceFlags) Set(v string) error {
	parts := strings.Split(v, ":")
	ch := wheels.Choice{Label: parts[0], Weight: 1}
	if len(parts) > 1 && parts[1] != "" {
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return fmt.Errorf("%q: the weight is a whole number", v)
		}
		ch.Weight = n
	}
	if len(parts) > 2 {
		ch.Color = parts[2]
	}
	if len(parts) > 3 {
		return fmt.Errorf("%q: use LABEL:WEIGHT:#COLOR", v)
	}
	*c = append(*c, ch)
	return nil
}

func wheelsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, wheelsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	usage := func() int { fmt.Fprintln(stderr, wheelsUsage); return 2 }
	path := func(name string) string { return "/v1/wheels/" + url.PathEscape(name) }
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	settings := func(name string) (wheels.Settings, bool) {
		fs := flag.NewFlagSet("wheels "+name, flag.ContinueOnError)
		fs.SetOutput(stderr)
		var st wheels.Settings
		var choices choiceFlags
		fs.StringVar(&st.Title, "title", "", "the wheel's title")
		fs.Var(&choices, "choice", "LABEL[:WEIGHT[:#COLOR]], once for each choice")
		fs.IntVar(&st.SpinSeconds, "seconds", 0, "how long a spin takes (2 to 20)")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
			return st, false
		}
		st.Choices = choices
		return st, true
	}
	switch sub {
	case "":
		var v api.WheelsView
		if code := apiExit(c.Do(ctx, "GET", "/v1/wheels", nil, &v), stderr); code != 0 {
			return code
		}
		if len(v.Wheels) == 0 {
			fmt.Fprintln(stdout, "No wheels yet.")
		}
		for _, w := range v.Wheels {
			fmt.Fprintf(stdout, "%s (%s): %s\n", w.Title, w.Name, strings.TrimPrefix(wheels.OddsWords(w.Wheel), w.Title+": "))
		}
		if v.Pages == 0 {
			fmt.Fprintln(stdout, "No wheel overlay is open in OBS, so spins stop at once.")
		}
		for _, sp := range v.Spins {
			result := "spinning…"
			if sp.SettledAt != nil {
				result = sp.Label
			}
			fmt.Fprintf(stdout, "%s  %s: %s  %s\n", sp.SpunAt.Local().Format("Jan 2 15:04"), sp.Title, result, sp.ID)
		}
		return 0
	case "add", "set":
		if len(args) < 1 {
			return usage()
		}
		st, ok := settings(sub)
		if !ok {
			return usage()
		}
		var w api.WheelView
		var err error
		if sub == "add" {
			err = c.Do(ctx, "POST", "/v1/wheels", map[string]any{"name": args[0], "title": st.Title, "choices": st.Choices,
				"spin_seconds": st.SpinSeconds}, &w)
		} else {
			err = c.Do(ctx, "PUT", path(args[0]), st, &w)
		}
		if code := apiExit(err, stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "%s (%s, version %d): %s\n", w.Title, w.Name, w.Version, strings.TrimPrefix(wheels.OddsWords(w.Wheel), w.Title+": "))
		return 0
	case "delete":
		if len(args) != 1 {
			return usage()
		}
		if code := apiExit(c.Do(ctx, "DELETE", path(args[0]), nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Deleted %s. Its spins are kept.\n", args[0])
		return 0
	case "spin":
		if len(args) != 1 {
			return usage()
		}
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		var sp wheels.Spin
		if code := apiExit(c.Do(ctx, "POST", path(args[0])+"/spin", map[string]string{"request_id": "cli-" + hex.EncodeToString(b)}, &sp),
			stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "%s is spinning…\n", sp.Title)
		for sp.SettledAt == nil {
			select {
			case <-ctx.Done():
				fmt.Fprintln(stderr, "It hasn't stopped yet. See it later with: streamgremlin wheels verify", sp.ID)
				return 1
			case <-time.After(250 * time.Millisecond):
			}
			if code := apiExit(c.Do(ctx, "GET", "/v1/spins/"+url.PathEscape(sp.ID), nil, &sp), stderr); code != 0 {
				return code
			}
		}
		fmt.Fprintf(stdout, "It landed on %s (number %d of %d). Spin %s\n", sp.Label, sp.Draw+1, sp.Total, sp.ID)
		return 0
	case "verify":
		if len(args) != 1 {
			return usage()
		}
		var sp wheels.Spin
		if code := apiExit(c.Do(ctx, "GET", "/v1/spins/"+url.PathEscape(args[0]), nil, &sp), stderr); code != 0 {
			return code
		}
		if bad := wheels.Verify(sp); len(bad) > 0 {
			for _, p := range bad {
				fmt.Fprintln(stdout, "✗", p)
			}
			return 1
		}
		fmt.Fprintf(stdout, "✓ Number %d of %d lands on %s, as recorded (%s, version %d).\n", sp.Draw+1, sp.Total, sp.Label, sp.Title, sp.WheelVersion)
		fmt.Fprintln(stdout, "The random number itself can't be replayed: it comes from your system's secure random source.")
		return 0
	}
	return usage()
}
