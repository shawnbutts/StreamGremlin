package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/followbots"
)

const followBotsUsage = `usage:
  streamgremlin followbots [-hours 24] [-days 7] [-burst 5] [-minutes 10]
        Twitch follows that look like follow bots: accounts younger than -days when
        they followed, that never chatted; "in a burst" when -burst came within -minutes
  streamgremlin followbots block|ban [-all] [-yes] [same options]
        block (or ban) the ones in a burst (-all: every one listed), as a list you can
        undo with streamgremlin bans undo; without -yes, only says what it would do`

func followBotsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 && (args[0] == "block" || args[0] == "ban" || args[0] == "help") {
		sub, args = args[0], args[1:]
	}
	if sub == "help" {
		fmt.Fprintln(stdout, followBotsUsage)
		return 0
	}
	o := followbots.Defaults()
	fs := flag.NewFlagSet("followbots", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&o.Hours, "hours", o.Hours, "follows from the last this many hours")
	fs.IntVar(&o.Days, "days", o.Days, "accounts younger than this when they followed")
	fs.IntVar(&o.Burst, "burst", o.Burst, "this many within -minutes is a burst")
	fs.IntVar(&o.Minutes, "minutes", o.Minutes, "the burst's minutes")
	all := fs.Bool("all", false, "every one listed, not only those in a burst")
	yes := fs.Bool("yes", false, "do it")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, followBotsUsage)
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var rv followbots.Review
	path := fmt.Sprintf("/v1/follow-bots?hours=%d&days=%d&burst=%d&minutes=%d", o.Hours, o.Days, o.Burst, o.Minutes)
	if code := apiExit(c.Do(ctx, "GET", path, nil, &rv), stderr); code != 0 {
		return code
	}
	var names []string
	for _, s := range rv.Suspects {
		if sub == "" {
			burst := ""
			if s.InBurst {
				burst = ", in a burst"
			}
			fmt.Fprintf(stdout, "%-26s %d days old when they followed %s%s\n", s.Login, s.AgeDays, s.FollowedAt.Local().Format("Jan 2 15:04"), burst)
		}
		if s.InBurst || *all {
			names = append(names, s.Login)
		}
	}
	if sub == "" {
		fmt.Fprintf(stdout, "%d of %d follows look like bots", len(rv.Suspects), rv.Follows)
		if rv.Unknown > 0 {
			fmt.Fprintf(stdout, " (%d couldn't be checked)", rv.Unknown)
		}
		fmt.Fprintln(stdout, ".")
		return 0
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "Nothing to "+sub+": none in a burst (add -all for every one listed).")
		return 0
	}
	if !*yes {
		fmt.Fprintf(stdout, "This would %s %d accounts: %v. Add -yes to do it.\n", sub, len(names), names)
		return 0
	}
	name := "Follow bots " + time.Now().Format("Jan 2 15:04")
	var l api.BanListView
	if code := apiExit(c.Do(ctx, "POST", "/v1/ban-lists", api.BanListImport{Name: name, Names: names, Start: true, Block: sub == "block"}, &l), stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "%s %d as the list %q: `streamgremlin bans` shows how it's going.\n", map[string]string{"block": "Blocking", "ban": "Banning"}[sub],
		len(names), name)
	return 0
}
