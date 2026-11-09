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

	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

const rewardsUsage = `usage:
  streamgremlin rewards [-json]                  your channel point rewards
  streamgremlin rewards add -cost N [-prompt P] [-input] [-cooldown S] [-max-per-stream N]
                            [-max-per-user N] [-skip-queue] [-color #RRGGBB] [-off] TITLE
  streamgremlin rewards set TITLE [-cost N] [-state on|off|pause|resume] [-prompt P] [-cooldown S]
  streamgremlin rewards queue TITLE              redemptions waiting
  streamgremlin rewards done TITLE ID            mark a redemption done
  streamgremlin rewards refund TITLE ID          refund it: the viewer gets the points back
  streamgremlin rewards delete TITLE

Channel points need an Affiliate or Partner channel. StreamGremlin can change
only the rewards it made; rewards made on Twitch can still start workflows.`

type rewardList struct {
	State     string          `json:"state"`
	Detail    string          `json:"detail"`
	Rewards   []twitch.Reward `json:"rewards"`
	CanManage bool            `json:"can_manage"`
}

func rewardsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, rewardsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	usage := func() int { fmt.Fprintln(stderr, rewardsUsage); return 2 }
	load := func() (rewardList, int) {
		var l rewardList
		if code := apiExit(c.Do(ctx, "GET", "/v1/rewards", nil, &l), stderr); code != 0 {
			return l, code
		}
		if l.State != "ok" {
			fmt.Fprintln(stderr, l.Detail)
			return l, 1
		}
		return l, 0
	}
	find := func(title string) (twitch.Reward, int) {
		l, code := load()
		if code != 0 {
			return twitch.Reward{}, code
		}
		for _, r := range l.Rewards {
			if strings.EqualFold(r.Title, title) || r.ID == title {
				return r, 0
			}
		}
		fmt.Fprintf(stderr, "There's no reward %q.\n", title)
		return twitch.Reward{}, 1
	}
	path := func(id string) string { return "/v1/rewards/" + url.PathEscape(id) }
	changeFlags := func(name string, ch *twitch.RewardChange) *flag.FlagSet {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		fs.SetOutput(stderr)
		fs.Func("cost", "points", func(v string) error { var n int; _, err := fmt.Sscan(v, &n); ch.Cost = &n; return err })
		fs.Func("prompt", "what it says to viewers", func(v string) error { ch.Prompt = &v; return nil })
		fs.Func("cooldown", "seconds between redemptions (0 for none)", func(v string) error { var n int; _, err := fmt.Sscan(v, &n); ch.CooldownSeconds = &n; return err })
		fs.Func("max-per-stream", "redemptions per stream (0 for no limit)", func(v string) error { var n int; _, err := fmt.Sscan(v, &n); ch.MaxPerStream = &n; return err })
		fs.Func("max-per-user", "per viewer per stream (0 for no limit)", func(v string) error { var n int; _, err := fmt.Sscan(v, &n); ch.MaxPerUserPerStream = &n; return err })
		fs.Func("color", "#RRGGBB", func(v string) error { ch.BackgroundColor = &v; return nil })
		return fs
	}
	if len(args) > 0 {
		switch args[0] {
		case "add":
			var ch twitch.RewardChange
			fs := changeFlags("rewards add", &ch)
			input := fs.Bool("input", false, "viewers type something with it")
			skip := fs.Bool("skip-queue", false, "done at once, never in the queue (can't be refunded)")
			off := fs.Bool("off", false, "make it turned off")
			if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 1 || ch.Cost == nil {
				return usage()
			}
			title, on := fs.Arg(0), !*off
			ch.Title, ch.InputRequired, ch.SkipQueue, ch.Enabled = &title, input, skip, &on
			var r twitch.Reward
			if code := apiExit(c.Do(ctx, "POST", "/v1/rewards", ch, &r), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Made %q (%d points).\n", r.Title, r.Cost)
			return 0
		case "set":
			if len(args) < 2 {
				return usage()
			}
			var ch twitch.RewardChange
			fs := changeFlags("rewards set", &ch)
			state := fs.String("state", "", "on, off, pause or resume")
			if err := fs.Parse(args[2:]); err != nil || fs.NArg() > 0 {
				return usage()
			}
			yes, no := true, false
			switch *state {
			case "on":
				ch.Enabled = &yes
			case "off":
				ch.Enabled = &no
			case "pause":
				ch.Paused = &yes
			case "resume":
				ch.Paused = &no
			case "":
			default:
				return usage()
			}
			r, code := find(args[1])
			if code != 0 {
				return code
			}
			if code := apiExit(c.Do(ctx, "PUT", path(r.ID), ch, &r), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "%s: %d points, %s.\n", r.Title, r.Cost, rewardState(r))
			return 0
		case "delete":
			if len(args) != 2 {
				return usage()
			}
			r, code := find(args[1])
			if code != 0 {
				return code
			}
			if code := apiExit(c.Do(ctx, "DELETE", path(r.ID), nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Deleted %q.\n", r.Title)
			return 0
		case "queue":
			if len(args) != 2 {
				return usage()
			}
			r, code := find(args[1])
			if code != 0 {
				return code
			}
			var out struct {
				Redemptions []twitch.Redemption `json:"redemptions"`
			}
			if code := apiExit(c.Do(ctx, "GET", path(r.ID)+"/redemptions", nil, &out), stderr); code != 0 {
				return code
			}
			if len(out.Redemptions) == 0 {
				fmt.Fprintf(stdout, "Nothing waiting for %q.\n", r.Title)
			}
			for _, d := range out.Redemptions {
				fmt.Fprintf(stdout, "%s  %-20s %s  %s\n", d.ID, d.UserName, d.RedeemedAt.Local().Format("15:04"), d.Input)
			}
			return 0
		case "done", "refund":
			if len(args) != 3 {
				return usage()
			}
			r, code := find(args[1])
			if code != 0 {
				return code
			}
			var out struct {
				Redemption twitch.Redemption `json:"redemption"`
				Repeated   bool              `json:"repeated"`
			}
			if code := apiExit(c.Do(ctx, "POST", path(r.ID)+"/redemptions/"+url.PathEscape(args[2]), map[string]string{"action": args[0]}, &out), stderr); code != 0 {
				return code
			}
			if args[0] == "done" {
				fmt.Fprintf(stdout, "Marked %s's %q done.\n", out.Redemption.UserName, r.Title)
			} else {
				fmt.Fprintf(stdout, "Refunded %s's %d points for %q.\n", out.Redemption.UserName, out.Redemption.Cost, r.Title)
			}
			return 0
		}
	}
	fs := flag.NewFlagSet("rewards", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return usage()
	}
	var l rewardList
	if code := apiExit(c.Do(ctx, "GET", "/v1/rewards", nil, &l), stderr); code != 0 {
		return code
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(l)
		return 0
	}
	if l.State != "ok" {
		fmt.Fprintln(stdout, l.Detail)
		return 0
	}
	if len(l.Rewards) == 0 {
		fmt.Fprintln(stdout, "No rewards yet. Make one with: streamgremlin rewards add -cost 100 \"Hydrate\"")
	}
	for _, r := range l.Rewards {
		who := "made in StreamGremlin"
		if !r.Managed {
			who = "made on Twitch: trigger only"
		}
		fmt.Fprintf(stdout, "%-30s %7d points  %-8s %s\n", r.Title, r.Cost, rewardState(r), who)
	}
	if !l.CanManage {
		fmt.Fprintln(stdout, "\nTo make and change rewards, sign in to Twitch again and allow it.")
	}
	return 0
}

func rewardState(r twitch.Reward) string {
	switch {
	case !r.Enabled:
		return "off"
	case r.Paused:
		return "paused"
	}
	return "on"
}
