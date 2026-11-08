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

	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/timers"
)

const timersUsage = `usage:
  streamgremlin timers [-json]        list timers and when each goes off next
  streamgremlin timers run ID         make one go off now; its workflows run for real
  streamgremlin timers on ID          turn one on (its count starts again)
  streamgremlin timers off ID         turn one off
  streamgremlin timers delete ID      delete one
  streamgremlin timers set ID [flags] create or change one:
      -name TEXT      its name (required for a new timer)
      -every 10m      how often (10s to 24h)
      -up-to 15m      go off at random between -every and this
      -cron "0 20 * * *"  on a schedule instead (minute hour day month weekday, local time)
      -lines N        wait for N chat messages since it last went off
      -live           only while OBS is streaming
      -once           turn itself off after going off once
      -off            save it turned off

A workflow starts from a timer with the trigger "A timer goes off"
(timers.timer.fired, where timer_id is the timer's ID).`

type timerList struct {
	Timers []timers.Status `json:"timers"`
	Live   *bool           `json:"live"`
}

// schedule says when a timer goes off, in words.
func schedule(t timers.Timer) string {
	if t.Cron != "" {
		return "on the schedule " + t.Cron
	}
	every := (time.Duration(t.EverySeconds) * time.Second).String()
	if t.UpToSeconds > 0 {
		return fmt.Sprintf("every %s to %s, at random", every, time.Duration(t.UpToSeconds)*time.Second)
	}
	return "every " + every
}

func timersCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, timersUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := func(id string) string { return "/v1/timers/" + url.PathEscape(id) }
	get := func(id string) (timers.Timer, bool, int) {
		var out timerList
		if code := apiExit(c.Do(ctx, "GET", "/v1/timers", nil, &out), stderr); code != 0 {
			return timers.Timer{}, false, code
		}
		for _, s := range out.Timers {
			if s.ID == id {
				return s.Timer, true, 0
			}
		}
		return timers.Timer{}, false, 0
	}
	if len(args) >= 2 {
		id := args[1]
		switch args[0] {
		case "run":
			var out struct{ Timer timers.Timer }
			if code := apiExit(c.Do(ctx, "POST", path(id)+"/fire", nil, &out), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "%s went off (%d times so far).\n", out.Timer.Name, out.Timer.Counter)
			return 0
		case "on", "off":
			t, found, code := get(id)
			if code != 0 {
				return code
			}
			if !found {
				fmt.Fprintf(stderr, "there's no timer %q\n", id)
				return 1
			}
			t.Enabled = args[0] == "on"
			if code := apiExit(c.Do(ctx, "PUT", path(id), t, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "%s is %s.\n", t.Name, args[0])
			return 0
		case "delete":
			if code := apiExit(c.Do(ctx, "DELETE", path(id), nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Deleted %s.\n", id)
			return 0
		case "set":
			return timerSet(ctx, c, id, args[2:], get, stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("timers", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, timersUsage)
		return 2
	}
	var out timerList
	if code := apiExit(c.Do(ctx, "GET", "/v1/timers", nil, &out), stderr); code != 0 {
		return code
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}
	if len(out.Timers) == 0 {
		fmt.Fprintln(stdout, "No timers. Make one in the app's Timers screen, or with: streamgremlin timers set ID -name NAME -every 15m")
		return 0
	}
	for _, t := range out.Timers {
		var notes []string
		if t.ChatLines > 0 {
			notes = append(notes, fmt.Sprintf("after %d chat lines", t.ChatLines))
		}
		if t.LiveOnly {
			notes = append(notes, "only while live")
		}
		if t.Once {
			notes = append(notes, "once")
		}
		next := ""
		switch {
		case t.Waiting == "off":
			next = "off"
		case t.Waiting == "never":
			next = "never: the schedule matches no date"
		case t.Waiting == "chat":
			next = fmt.Sprintf("waiting for chat: %d of %d lines", t.Lines, t.ChatLines)
		case t.NextAt != nil:
			next = "next " + t.NextAt.Local().Format("Mon 15:04:05")
		}
		extra := ""
		if len(notes) > 0 {
			extra = ", " + strings.Join(notes, ", ")
		}
		fmt.Fprintf(stdout, "%-20s %-24s %s%s; %s; gone off %d times\n", t.ID, t.Name, schedule(t.Timer), extra, next, t.Counter)
	}
	if out.Live != nil {
		if *out.Live {
			fmt.Fprintln(stdout, "\nOBS says you're live.")
		} else {
			fmt.Fprintln(stdout, "\nOBS says you're not live.")
		}
	}
	return 0
}

func timerSet(ctx context.Context, c *client.Client, id string, args []string, get func(string) (timers.Timer, bool, int), stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timers set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "")
	every := fs.Duration("every", 0, "")
	upTo := fs.Duration("up-to", 0, "")
	cron := fs.String("cron", "", "")
	lines := fs.Int("lines", -1, "")
	live := fs.Bool("live", false, "")
	once := fs.Bool("once", false, "")
	off := fs.Bool("off", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, timersUsage)
		return 2
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	t, found, code := get(id)
	if code != 0 {
		return code
	}
	if !found {
		t = timers.Timer{ID: id, Enabled: true, EverySeconds: 15 * 60}
	}
	if set["name"] {
		t.Name = *name
	}
	if set["every"] {
		t.EverySeconds, t.Cron = int(every.Seconds()), ""
		if !set["up-to"] {
			t.UpToSeconds = 0
		}
	}
	if set["up-to"] {
		t.UpToSeconds = int(upTo.Seconds())
	}
	if set["cron"] {
		t.Cron, t.EverySeconds, t.UpToSeconds = *cron, 0, 0
	}
	if set["lines"] {
		t.ChatLines = *lines
	}
	if set["live"] {
		t.LiveOnly = *live
	}
	if set["once"] {
		t.Once = *once
	}
	if set["off"] {
		t.Enabled = !*off
	}
	var out timers.Status
	if code := apiExit(c.Do(ctx, "PUT", "/v1/timers/"+url.PathEscape(id), t, &out), stderr); code != 0 {
		return code
	}
	state := "on"
	if !out.Enabled {
		state = "off"
	}
	fmt.Fprintf(stdout, "Saved %s (%s): %s, %s.\n", out.Name, out.ID, schedule(out.Timer), state)
	return 0
}
