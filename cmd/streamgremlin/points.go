package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/points"
)

const viewersUsage = `usage:
  streamgremlin viewers [NAME]            viewers whose name starts with NAME (all when empty), with their points
  streamgremlin viewers show ID|@NAME     one viewer, with their points history
  streamgremlin viewers export ID|@NAME   everything kept about them, as JSON
  streamgremlin viewers forget ID|@NAME   blank their account and names; their points history stays, unnamed

Viewers are recorded from your real chat and events, never test events.`

const pointsUsage = `usage:
  streamgremlin points                    how many viewers hold how many, and the top 25
  streamgremlin points give @NAME N [-reason R]
  streamgremlin points take @NAME N [-reason R]
  streamgremlin points undo TRANSACTION   undo a change, once
  streamgremlin points rename NAME        call your points something else, like Gold
  streamgremlin points audit              check every change balances and every balance matches its history

Nothing is ever changed or deleted, and nobody goes below zero.`

// findViewer resolves an id (v_…) or @name to a viewer.
func findViewer(ctx context.Context, c interface {
	Do(context.Context, string, string, any, any) error
}, who string, stderr io.Writer) (api.ViewerView, int) {
	var out struct {
		Viewers []api.ViewerView `json:"viewers"`
	}
	if strings.HasPrefix(who, "v_") {
		var d api.ViewerDetail
		if code := apiExit(c.Do(ctx, "GET", "/v1/viewers/"+url.PathEscape(who), nil, &d), stderr); code != 0 {
			return api.ViewerView{}, code
		}
		return d.ViewerView, 0
	}
	name := strings.ToLower(strings.TrimPrefix(who, "@"))
	if code := apiExit(c.Do(ctx, "GET", "/v1/viewers?q="+url.QueryEscape(name), nil, &out), stderr); code != 0 {
		return api.ViewerView{}, code
	}
	for _, v := range out.Viewers {
		if v.Login == name {
			return v, 0
		}
	}
	fmt.Fprintf(stderr, "StreamGremlin hasn't seen %s in chat.\n", who)
	return api.ViewerView{}, 1
}

func viewersCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, viewersUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if len(args) == 2 && (args[0] == "show" || args[0] == "export" || args[0] == "forget") {
		v, code := findViewer(ctx, c, args[1], stderr)
		if code != 0 {
			return code
		}
		path := "/v1/viewers/" + url.PathEscape(v.ID)
		switch args[0] {
		case "export":
			var raw json.RawMessage
			if code := apiExit(c.Do(ctx, "GET", path+"/export", nil, &raw), stderr); code != 0 {
				return code
			}
			var pretty map[string]any
			_ = json.Unmarshal(raw, &pretty)
			b, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Fprintln(stdout, string(b))
			return 0
		case "forget":
			if code := apiExit(c.Do(ctx, "POST", path+"/forget", nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Forgot %s. Their points history stays, unnamed.\n", v.DisplayName)
			return 0
		}
		var d api.ViewerDetail
		if code := apiExit(c.Do(ctx, "GET", path, nil, &d), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "%s (@%s, %s): %d points, %d chat messages; first seen %s, last seen %s\n", d.DisplayName, d.Login, d.ID, d.Balance,
			d.Messages, d.FirstSeen.Local().Format("Jan 2 2006"), d.LastSeen.Local().Format("Jan 2 15:04"))
		for _, t := range d.History {
			undone := ""
			if t.ReversedBy != "" {
				undone = " (undone)"
			}
			fmt.Fprintf(stdout, "  %s  %+d  %s%s  by %s  %s%s\n", t.CreatedAt.Local().Format("Jan 2 15:04"), t.Amount, t.Kind, memo(t.Memo), t.By, t.ID, undone)
		}
		return 0
	}
	if len(args) > 1 {
		fmt.Fprintln(stderr, viewersUsage)
		return 2
	}
	q := ""
	if len(args) == 1 {
		q = args[0]
	}
	var out struct {
		Viewers []api.ViewerView `json:"viewers"`
	}
	if code := apiExit(c.Do(ctx, "GET", "/v1/viewers?q="+url.QueryEscape(strings.TrimPrefix(q, "@")), nil, &out), stderr); code != 0 {
		return code
	}
	if len(out.Viewers) == 0 {
		fmt.Fprintln(stdout, "No viewers yet.")
	}
	for _, v := range out.Viewers {
		fmt.Fprintf(stdout, "%-24s %8d points  last seen %s  %s\n", v.DisplayName, v.Balance, v.LastSeen.Local().Format("Jan 2 15:04"), v.ID)
	}
	return 0
}

func memo(m string) string {
	if m == "" {
		return ""
	}
	return ": " + m
}

func pointsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, pointsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	usage := func() int { fmt.Fprintln(stderr, pointsUsage); return 2 }
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "":
		var v api.PointsView
		if code := apiExit(c.Do(ctx, "GET", "/v1/points", nil, &v), stderr); code != 0 {
			return code
		}
		verb := "hold"
		if v.Summary.Viewers == 1 {
			verb = "holds"
		}
		fmt.Fprintf(stdout, "%s %s %d %s.\n", plural(v.Summary.Viewers, "viewer", "viewers"), verb, v.Summary.Issued, v.Currency.Name)
		for i, t := range v.Top {
			fmt.Fprintf(stdout, "%2d. %-24s %d\n", i+1, t.DisplayName, t.Balance)
		}
		return 0
	case "give", "take":
		if len(args) < 2 {
			return usage()
		}
		fs := flag.NewFlagSet("points "+sub, flag.ContinueOnError)
		fs.SetOutput(stderr)
		reason := fs.String("reason", "", "why")
		if fs.Parse(args[2:]) != nil || fs.NArg() > 0 {
			return usage()
		}
		n, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || n <= 0 {
			fmt.Fprintln(stderr, "How many is a whole number above 0.")
			return 2
		}
		if sub == "take" {
			n = -n
		}
		v, code := findViewer(ctx, c, args[0], stderr)
		if code != 0 {
			return code
		}
		var res points.Result
		if code := apiExit(c.Do(ctx, "POST", "/v1/viewers/"+url.PathEscape(v.ID)+"/points",
			map[string]any{"amount": n, "reason": *reason, "request_id": requestID()}, &res), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "%s now has %d. (%s)\n", v.DisplayName, res.Balance, res.Transaction.ID)
		return 0
	case "undo":
		if len(args) != 1 {
			return usage()
		}
		var res points.Result
		if code := apiExit(c.Do(ctx, "POST", "/v1/points/transactions/"+url.PathEscape(args[0])+"/undo",
			map[string]string{"request_id": requestID()}, &res), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Undone. They now have %d.\n", res.Balance)
		return 0
	case "rename":
		if len(args) == 0 {
			return usage()
		}
		var cur points.Currency
		if code := apiExit(c.Do(ctx, "PUT", "/v1/points/currency", map[string]string{"name": strings.Join(args, " ")}, &cur), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Your points are now called %s.\n", cur.Name)
		return 0
	case "audit":
		var out struct {
			OK       bool     `json:"ok"`
			Problems []string `json:"problems"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/points/audit", nil, &out), stderr); code != 0 {
			return code
		}
		if out.OK {
			fmt.Fprintln(stdout, "✓ Every change balances, and every balance matches its history.")
			return 0
		}
		for _, p := range out.Problems {
			fmt.Fprintln(stdout, "✗", p)
		}
		return 1
	}
	return usage()
}
