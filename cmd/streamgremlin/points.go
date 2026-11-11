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
	"code.streamgremlin.com/streamgremlin/internal/gives"
	"code.streamgremlin.com/streamgremlin/internal/points"
)

const viewersUsage = `usage:
  streamgremlin viewers [NAME]            viewers whose name starts with NAME (all when empty), with their points
  streamgremlin viewers show ID|@NAME     one viewer, with their points history
  streamgremlin viewers card ID|@NAME     what Twitch says about them now (follow, subscription, VIP, moderator,
                                          banned; unknown when Twitch didn't say), and their recent chat
  streamgremlin viewers export ID|@NAME   everything kept about them, as JSON
  streamgremlin viewers forget ID|@NAME   blank their account and names; their points history stays, unnamed
  streamgremlin viewers export-all        everything kept about every viewer, as JSON (forgotten viewers unnamed)

Viewers are recorded from your real chat and events, never test events.`

const pointsUsage = `usage:
  streamgremlin points                    how many viewers hold how many, and the top 25
  streamgremlin points give @NAME N [-reason R]
  streamgremlin points take @NAME N [-reason R]
  streamgremlin points undo TRANSACTION   undo a change, once
  streamgremlin points rename NAME        call your points something else, like Gold
  streamgremlin points audit              check every change balances and every balance matches its history
  streamgremlin points give-all chat|subs|recent=MINUTES N [-reason R] [-yes]
        give everyone in a group N points each: everyone in chat (Twitch's list), subscribers (Twitch's
        list), or everyone who chatted in the last MINUTES. Without -yes, only says who'd get them.
  streamgremlin points gives              the latest gives to groups
  streamgremlin points undo-give ID       take a give back from everyone (never below zero)
  streamgremlin points earning            how viewers earn points
  streamgremlin points earning on|off
  streamgremlin points earning set NAME=N ...
        chat=N every=SECONDS live-only=yes|no follow=N tier1=N tier2=N tier3=N gift=N bits=N
        time=yes|no time-every=MINUTES time-active=N time-window=MINUTES time-quiet=N
        (gift is per sub gifted, bits per 100 bits; 0 turns a rule off. Time in chat pays, every
        time-every minutes while you're live, time-active to viewers who chatted in the last
        time-window minutes, and time-quiet to others on Twitch's chatter list)

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
	if len(args) == 1 && args[0] == "export-all" {
		var raw json.RawMessage
		if code := apiExit(c.Do(ctx, "GET", "/v1/exports/viewers", nil, &raw), stderr); code != 0 {
			return code
		}
		var pretty map[string]any
		_ = json.Unmarshal(raw, &pretty)
		b, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(args) == 2 && args[0] == "card" {
		v, code := findViewer(ctx, c, args[1], stderr)
		if code != 0 {
			return code
		}
		var card api.ChatterCard
		if code := apiExit(c.Do(ctx, "GET", "/v1/chatters/"+url.PathEscape(v.Platform)+"/"+url.PathEscape(v.UserID), nil, &card), stderr); code != 0 {
			return code
		}
		printCard(stdout, v, card)
		return 0
	}
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
	case "earning":
		return earningCmd(ctx, c, args, stdout, stderr, usage)
	case "give-all":
		return giveAllCmd(ctx, c, args, stdout, stderr, usage)
	case "gives":
		var out struct {
			Gives []gives.Give `json:"gives"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/points/gives", nil, &out), stderr); code != 0 {
			return code
		}
		if len(out.Gives) == 0 {
			fmt.Fprintln(stdout, "No gives to groups yet.")
		}
		for _, g := range out.Gives {
			state := ""
			if g.UndoneAt != nil {
				state = " (undone)"
			} else if g.PaidAt == nil {
				state = " (not finished: give again with the same request to finish)"
			}
			fmt.Fprintf(stdout, "%s  %s: %d each to %s (%s)%s, by %s%s\n", g.CreatedAt.Local().Format("Jan 2 15:04"), g.ID, g.Amount,
				plural(g.Viewers, "viewer", "viewers"), g.Words, memo(g.Memo), g.By, state)
		}
		return 0
	case "undo-give":
		if len(args) != 1 {
			return usage()
		}
		var res gives.UndoResult
		if code := apiExit(c.Do(ctx, "POST", "/v1/points/gives/"+url.PathEscape(args[0])+"/undo", nil, &res), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Took it back from %s.\n", plural(res.Undone, "viewer", "viewers"))
		if len(res.Short) > 0 {
			fmt.Fprintf(stdout, "%s spent some already and keep theirs (nobody goes below zero): %s. Undo again later to try them again.\n",
				plural(len(res.Short), "viewer", "viewers"), strings.Join(res.Short, ", "))
			return 1
		}
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

func earningCmd(ctx context.Context, c interface {
	Do(context.Context, string, string, any, any) error
}, args []string, stdout, stderr io.Writer, usage func() int) int {
	var v api.EarningView
	if code := apiExit(c.Do(ctx, "GET", "/v1/points/earning", nil, &v), stderr); code != 0 {
		return code
	}
	r := v.Rules
	if len(args) > 0 {
		switch args[0] {
		case "on", "off":
			if len(args) != 1 {
				return usage()
			}
			r.On = args[0] == "on"
		case "set":
			if len(args) < 2 {
				return usage()
			}
			for _, a := range args[1:] {
				k, val, ok := strings.Cut(a, "=")
				if !ok {
					return usage()
				}
				if k == "live-only" || k == "time" {
					if val != "yes" && val != "no" {
						fmt.Fprintf(stderr, "%s is yes or no.\n", k)
						return 2
					}
					*map[string]*bool{"live-only": &r.Chat.LiveOnly, "time": &r.Time.On}[k] = val == "yes"
					continue
				}
				n, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					fmt.Fprintf(stderr, "%s is a whole number.\n", k)
					return 2
				}
				dst := map[string]*int64{"chat": &r.Chat.Amount, "follow": &r.Follow, "tier1": &r.Sub.Tier1, "tier2": &r.Sub.Tier2,
					"tier3": &r.Sub.Tier3, "gift": &r.Gift, "bits": &r.Bits, "time-active": &r.Time.Active, "time-quiet": &r.Time.Quiet}[k]
				switch {
				case k == "every":
					r.Chat.EverySeconds = int(n)
				case k == "time-every":
					r.Time.EveryMinutes = int(n)
				case k == "time-window":
					r.Time.ActiveMinutes = int(n)
				case dst != nil:
					*dst = n
				default:
					fmt.Fprintf(stderr, "There's no rule called %s.\n", k)
					return 2
				}
			}
		default:
			return usage()
		}
		if code := apiExit(c.Do(ctx, "PUT", "/v1/points/earning", r, &v), stderr); code != 0 {
			return code
		}
		r = v.Rules
	}
	state := "off"
	if r.On {
		state = "on"
	}
	fmt.Fprintf(stdout, "Earning is %s. You're %s, as far as StreamGremlin knows.\n", state, v.Live)
	only := ""
	if r.Chat.LiveOnly {
		only = ", only while live"
	}
	fmt.Fprintf(stdout, "  chatting       %d, at most every %ds%s\n", r.Chat.Amount, r.Chat.EverySeconds, only)
	if r.Time.On {
		fmt.Fprintf(stdout, "  time in chat   every %d minutes while live: %d if they chatted in the last %d minutes, %d if they're in chat but quiet\n",
			r.Time.EveryMinutes, r.Time.Active, r.Time.ActiveMinutes, r.Time.Quiet)
	} else {
		fmt.Fprintln(stdout, "  time in chat   off")
	}
	if t := v.LastTick; t != nil {
		note := ""
		if t.Note != "" {
			note = " (" + t.Note + ")"
		}
		fmt.Fprintf(stdout, "    last paid at %s: %d chatting, %d quiet%s\n", t.At.Local().Format("15:04"), t.Active, t.Quiet, note)
	}
	fmt.Fprintf(stdout, "  a first follow %d, once per viewer\n", r.Follow)
	fmt.Fprintf(stdout, "  a sub          %d tier 1, %d tier 2, %d tier 3 (resubs too; gifted subs earn for the gifter)\n", r.Sub.Tier1, r.Sub.Tier2, r.Sub.Tier3)
	fmt.Fprintf(stdout, "  a gifted sub   %d each, to the gifter\n", r.Gift)
	fmt.Fprintf(stdout, "  100 bits       %d\n", r.Bits)
	return 0
}

func giveAllCmd(ctx context.Context, c interface {
	Do(context.Context, string, string, any, any) error
}, args []string, stdout, stderr io.Writer, usage func() int) int {
	if len(args) < 2 {
		return usage()
	}
	who := gives.Who{Audience: args[0]}
	if m, ok := strings.CutPrefix(args[0], "recent="); ok {
		n, err := strconv.Atoi(m)
		if err != nil {
			fmt.Fprintln(stderr, "recent=MINUTES, like recent=15.")
			return 2
		}
		who = gives.Who{Audience: gives.Recent, Minutes: n}
	} else if args[0] == gives.Recent {
		fmt.Fprintln(stderr, "Say how recent: recent=MINUTES, like recent=15.")
		return 2
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || n <= 0 {
		fmt.Fprintln(stderr, "How many each is a whole number above 0.")
		return 2
	}
	fs := flag.NewFlagSet("points give-all", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reason := fs.String("reason", "", "why")
	yes := fs.Bool("yes", false, "give, rather than only saying who'd get them")
	if fs.Parse(args[2:]) != nil || fs.NArg() > 0 {
		return usage()
	}
	if !*yes {
		var g gives.Group
		if code := apiExit(c.Do(ctx, "POST", "/v1/points/gives/preview", who, &g), stderr); code != 0 {
			return code
		}
		if g.Viewers == 0 {
			fmt.Fprintln(stdout, g.Note)
			return 1
		}
		more := ""
		if g.Viewers > len(g.Sample) {
			more = fmt.Sprintf(" and %d more", g.Viewers-len(g.Sample))
		}
		fmt.Fprintf(stdout, "%s (%s): %s%s would get %d each, %d in all.\nRun again with -yes to give.\n", strings.ToUpper(who.Words()[:1])+who.Words()[1:],
			plural(g.Viewers, "viewer", "viewers"), strings.Join(g.Sample, ", "), more, n, n*int64(g.Viewers))
		return 0
	}
	var g gives.Give
	if code := apiExit(c.Do(ctx, "POST", "/v1/points/gives", gives.Request{Who: who, Amount: n, Reason: *reason, RequestID: requestID()}, &g),
		stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "Gave %s %d each, %d in all. (%s)\n", plural(g.Viewers, "viewer", "viewers"), g.Amount, g.Total, g.ID)
	return 0
}

// printCard prints a viewer card: each Twitch fact, or why it's unknown.
func printCard(w io.Writer, v api.ViewerView, c api.ChatterCard) {
	fmt.Fprintf(w, "%s (@%s)\n", v.DisplayName, v.Login)
	if t := c.Twitch; t != nil {
		why := map[string]string{}
		for _, u := range t.Unknown {
			why[u.Fact] = "unknown: " + u.Why
		}
		yes := func(b bool) string {
			if b {
				return "yes"
			}
			return "no"
		}
		fact := func(label, f string, known bool, value func() string) {
			if !known {
				if why[f] == "" {
					why[f] = "unknown"
				}
				fmt.Fprintf(w, "  %-11s %s\n", label+":", why[f])
				return
			}
			fmt.Fprintf(w, "  %-11s %s\n", label+":", value())
		}
		fact("Account", "account", why["account"] == "", func() string {
			out := "known to Twitch"
			if t.CreatedAt != nil {
				out = "made " + t.CreatedAt.Local().Format("Jan 2 2006")
			}
			if t.BroadcasterType != "" {
				out += ", Twitch " + t.BroadcasterType
			}
			return out
		})
		fact("Follows", "follow", t.Follow != nil, func() string {
			if !t.Follow.Follows {
				return "no"
			}
			return "since " + t.Follow.FollowedAt.Local().Format("Jan 2 2006")
		})
		fact("Subscribed", "subscription", t.Subscription != nil, func() string {
			s := t.Subscription
			if !s.Subscribed {
				return "no"
			}
			out := fmt.Sprintf("tier %d", s.Tier)
			switch {
			case s.Anonymous:
				out += ", an anonymous gift"
			case s.Gifter != "":
				out += ", a gift from " + s.Gifter
			case s.Gift:
				out += ", a gift"
			}
			return out
		})
		fact("VIP", "vip", t.VIP != nil, func() string { return yes(*t.VIP) })
		fact("Moderator", "moderator", t.Moderator != nil, func() string { return yes(*t.Moderator) })
		fact("Banned", "banned", t.Ban != nil, func() string {
			b := t.Ban
			if !b.Banned {
				return "no"
			}
			out := "banned"
			if b.Until != nil {
				out = "timed out until " + b.Until.Local().Format("15:04")
			}
			if b.Reason != "" {
				out += ": " + b.Reason
			}
			return out
		})
	}
	if c.Viewer != nil {
		fmt.Fprintf(w, "  StreamGremlin: %d %s, %d chat messages, first seen %s\n", c.Viewer.Balance, c.Currency, c.Viewer.Messages,
			c.Viewer.FirstSeen.Local().Format("Jan 2 2006"))
	}
	for _, m := range c.Messages {
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(m.Payload, &p)
		fmt.Fprintf(w, "  %s  %s\n", m.OccurredAt.Local().Format("Jan 2 15:04"), p.Text)
	}
}
