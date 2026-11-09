package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/giveaways"
)

const giveawaysUsage = `usage:
  streamgremlin giveaways                     the running giveaway and the recent ones
  streamgremlin giveaways open [flags] TITLE  open one; flags:
      -command join    the entry command
      -winners 1       how many winners (1 to 20)
      -minutes 0       close entries by themselves after this long
      -claim 0         seconds a winner has to type !claim (15 to 600; 0: no claiming)
      -subs            subscribers only
      -practice        take only test chat, and send nothing
  streamgremlin giveaways close               stop taking entries
  streamgremlin giveaways draw [-n N]         draw winners (all still wanted)
  streamgremlin giveaways set-aside DRAW      set a winner aside (by draw number) and draw again
  streamgremlin giveaways end | cancel
  streamgremlin giveaways show ID [-entries]  everything recorded about one
  streamgremlin giveaways verify ID           check its entry list and draws

One entry each, and every entry has the same chance. Winners are drawn with
a secure random source, and each draw is recorded so it can be checked.`

func giveawaysCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, giveawaysUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	usage := func() int { fmt.Fprintln(stderr, giveawaysUsage); return 2 }
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	current := func() (*giveaways.Detail, int) {
		var v api.GiveawaysView
		if code := apiExit(c.Do(ctx, "GET", "/v1/giveaways", nil, &v), stderr); code != 0 {
			return nil, code
		}
		if v.Current == nil {
			fmt.Fprintln(stderr, "No giveaway is running.")
			return nil, 2
		}
		return v.Current, 0
	}
	act := func(path string, body any, out any) int {
		g, code := current()
		if code != 0 {
			return code
		}
		return apiExit(c.Do(ctx, "POST", "/v1/giveaways/"+g.ID+"/"+path, body, out), stderr)
	}
	printDraws := func(draws []giveaways.Draw) {
		for _, d := range draws {
			fmt.Fprintf(stdout, "Draw %d: %s (entry %d, number %d of the %d not yet drawn)\n", d.N, d.Winner.DisplayName, d.EntrySeq,
				d.PickIndex+1, d.Candidates)
		}
	}
	switch sub {
	case "":
		var v api.GiveawaysView
		if code := apiExit(c.Do(ctx, "GET", "/v1/giveaways", nil, &v), stderr); code != 0 {
			return code
		}
		if v.Current == nil {
			fmt.Fprintln(stdout, "No giveaway is running.")
		} else {
			showGiveaway(stdout, *v.Current, false)
		}
		for _, g := range v.Recent {
			if v.Current != nil && g.ID == v.Current.ID {
				continue
			}
			fmt.Fprintf(stdout, "%s  %s (%s, %s)  %s\n", g.OpenedAt.Local().Format("Jan 2 15:04"), g.Title, g.State,
				plural(g.EntryCount, "entry", "entries"), g.ID)
		}
		return 0
	case "open":
		fs := flag.NewFlagSet("giveaways open", flag.ContinueOnError)
		fs.SetOutput(stderr)
		var st giveaways.Settings
		fs.StringVar(&st.Command, "command", "", "the entry command")
		fs.IntVar(&st.Winners, "winners", 1, "how many winners")
		fs.IntVar(&st.Minutes, "minutes", 0, "close entries after this many minutes")
		fs.IntVar(&st.ClaimSeconds, "claim", 0, "seconds to claim")
		fs.BoolVar(&st.SubscribersOnly, "subs", false, "subscribers only")
		fs.BoolVar(&st.Practice, "practice", false, "test chat only, and send nothing")
		if err := fs.Parse(args); err != nil || fs.NArg() == 0 {
			return usage()
		}
		st.Title = strings.Join(fs.Args(), " ")
		var g giveaways.Giveaway
		if code := apiExit(c.Do(ctx, "POST", "/v1/giveaways", st, &g), stderr); code != 0 {
			return code
		}
		practice := ""
		if g.Practice {
			practice = " Practice: only test chat enters, and nothing is sent."
		}
		fmt.Fprintf(stdout, "Giveaway open: %s. Viewers type !%s to enter.%s\n", g.Title, g.Command, practice)
		return 0
	case "close":
		var g giveaways.Giveaway
		if code := act("close", nil, &g); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Entries closed: %s. Fingerprint %s\n", plural(g.EntryCount, "entry", "entries"), g.Fingerprint)
		return 0
	case "draw":
		fs := flag.NewFlagSet("giveaways draw", flag.ContinueOnError)
		fs.SetOutput(stderr)
		n := fs.Int("n", 0, "how many (all still wanted)")
		if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
			return usage()
		}
		var out struct {
			Draws []giveaways.Draw `json:"draws"`
		}
		if code := act("draw", map[string]int{"count": *n}, &out); code != 0 {
			return code
		}
		printDraws(out.Draws)
		return 0
	case "set-aside":
		if len(args) != 1 {
			return usage()
		}
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return usage()
		}
		g, code := current()
		if code != 0 {
			return code
		}
		id := ""
		for _, d := range g.Draws {
			if d.N == n {
				id = d.ID
			}
		}
		if id == "" {
			fmt.Fprintf(stderr, "There's no draw %d.\n", n)
			return 2
		}
		var out struct {
			Draws []giveaways.Draw `json:"draws"`
		}
		if code := apiExit(c.Do(ctx, "POST", "/v1/giveaways/"+g.ID+"/set-aside", map[string]string{"draw_id": id}, &out), stderr); code != 0 {
			return code
		}
		printDraws(out.Draws)
		return 0
	case "end", "cancel":
		if len(args) != 0 {
			return usage()
		}
		var g giveaways.Giveaway
		if code := act(sub, nil, &g); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "%s: %s.\n", g.Title, g.State)
		return 0
	case "show", "verify":
		fs := flag.NewFlagSet("giveaways "+sub, flag.ContinueOnError)
		fs.SetOutput(stderr)
		entries := fs.Bool("entries", false, "list every entry")
		if len(args) == 0 {
			return usage()
		}
		id := args[0]
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
			return usage()
		}
		d, code := getGiveaway(ctx, c, id, stderr)
		if code != 0 {
			return code
		}
		if sub == "show" {
			showGiveaway(stdout, d, *entries)
			return 0
		}
		return verifyGiveaway(stdout, d)
	}
	return usage()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func getGiveaway(ctx context.Context, c *client.Client, id string, stderr io.Writer) (giveaways.Detail, int) {
	var d giveaways.Detail
	return d, apiExit(c.Do(ctx, "GET", "/v1/giveaways/"+id, nil, &d), stderr)
}

var outcomeWords = map[string]string{
	giveaways.Won: "won", giveaways.PendingClaim: "can claim until %s", giveaways.Claimed: "claimed",
	giveaways.Forfeited: "didn't claim in time", giveaways.SetAside: "set aside",
}

func showGiveaway(w io.Writer, d giveaways.Detail, entries bool) {
	practice := ""
	if d.Practice {
		practice = ", practice"
	}
	fmt.Fprintf(w, "%s (%s%s): !%s, %s, %s\n", d.Title, d.State, practice, d.Command, plural(d.Winners, "winner", "winners"),
		plural(len(d.Entries), "entry", "entries"))
	if d.Fingerprint != "" {
		fmt.Fprintf(w, "Entry list fingerprint (SHA-256): %s\n", d.Fingerprint)
	}
	for _, dr := range d.Draws {
		words := outcomeWords[dr.Outcome]
		if dr.ClaimUntil != nil {
			words = fmt.Sprintf(words, dr.ClaimUntil.Local().Format("15:04:05"))
		}
		fmt.Fprintf(w, "Draw %d: %s, %s (entry %d, number %d of the %d not yet drawn)\n", dr.N, dr.Winner.DisplayName, words,
			dr.EntrySeq, dr.PickIndex+1, dr.Candidates)
	}
	if entries {
		for _, e := range d.Entries {
			fmt.Fprintf(w, "%d\t%s:%s\t%s\n", e.Seq, e.Platform, e.UserID, e.DisplayName)
		}
	}
}

func verifyGiveaway(w io.Writer, d giveaways.Detail) int {
	if problems := giveaways.Verify(d); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(w, "✗", p)
		}
		return 1
	}
	fmt.Fprintf(w, "✓ The entry list matches its fingerprint (%s entries).\n", strconv.Itoa(len(d.Entries)))
	fmt.Fprintf(w, "✓ Each of the %d draws picked an entry that hadn't been drawn, at the recorded place.\n", len(d.Draws))
	fmt.Fprintln(w, "The random picks themselves can't be replayed: they come from your system's secure random source.")
	return 0
}
