package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/client"
)

const bansUsage = `usage:
  streamgremlin bans                               your ban lists and how far each has got
  streamgremlin bans import NAME FILE [-reason R]  read a file of Twitch names or ids (or a newer version of a list)
  streamgremlin bans copy NAME                     a new list of everyone banned on Twitch now
  streamgremlin bans show NAME [STATE]             a list's names: waiting, banned, already, not_found, kept...
  streamgremlin bans confirm NAME [-unban-removed] [-reban-by-hand]
                                                   start banning a checked list
  streamgremlin bans pause|resume NAME
  streamgremlin bans compare NAME                  check its bans are still on Twitch
  streamgremlin bans undo NAME [-yes]              unban everything the list banned
  streamgremlin bans save NAME FILE [STATE]        save its names to a file, one a line
  streamgremlin bans forget NAME [-yes]            forget the list (nobody is unbanned)

Nothing is banned until a list is confirmed. Bans go one at a time at
Twitch's pace, in the background, and carry on after a restart.`

func bansCmd(dir string, args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	if sub == "help" || sub == "-h" || sub == "--help" {
		fmt.Fprintln(stdout, bansUsage)
		return 0
	}
	fs := flag.NewFlagSet("bans "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	reason := fs.String("reason", "", "the reason Twitch shows with each ban")
	yes := fs.Bool("yes", false, "do it, rather than only saying what it would do")
	unbanRemoved := fs.Bool("unban-removed", false, "also unban names this list banned that aren't in the newer file")
	rebanByHand := fs.Bool("reban-by-hand", false, "ban again names unbanned on Twitch by hand")
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return 2
		}
		args = fs.Args()
		if len(args) > 0 {
			pos, args = append(pos, args[0]), args[1:]
		}
	}
	usage := func() int { fmt.Fprintln(stderr, bansUsage); return 2 }
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	lists := func() ([]api.BanListView, int) {
		var out struct {
			Lists []api.BanListView `json:"lists"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/ban-lists", nil, &out), stderr); code != 0 {
			return nil, code
		}
		return out.Lists, 0
	}
	find := func(name string) (api.BanListView, int) {
		all, code := lists()
		if code != 0 {
			return api.BanListView{}, code
		}
		for _, l := range all {
			if strings.EqualFold(l.Name, name) {
				return l, 0
			}
		}
		fmt.Fprintf(stderr, "There's no ban list called %q: see `streamgremlin bans`.\n", name)
		return api.BanListView{}, 1
	}
	post := func(path string, body any, done string) int {
		var v api.BanListView
		if code := apiExit(c.Do(ctx, "POST", path, body, &v), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, done)
		printBanList(stdout, v)
		return 0
	}
	switch sub {
	case "list":
		all, code := lists()
		if code != 0 {
			return code
		}
		if len(all) == 0 {
			fmt.Fprintln(stdout, "No ban lists yet: `streamgremlin bans import NAME FILE`.")
		}
		for _, l := range all {
			printBanList(stdout, l)
		}
		return 0
	case "import":
		if len(pos) != 2 {
			return usage()
		}
		path, err := filepath.Abs(pos[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return post("/v1/ban-lists", api.BanListImport{Name: pos[0], Path: path, Reason: *reason},
			"Reading the file. Nothing is banned until you confirm: `streamgremlin bans confirm "+quoteArg(pos[0])+"`.")
	case "copy":
		if len(pos) != 1 {
			return usage()
		}
		return post("/v1/ban-lists", api.BanListImport{Name: pos[0], Copy: true}, "Copying Twitch's ban list.")
	case "show":
		if len(pos) < 1 || len(pos) > 2 {
			return usage()
		}
		l, code := find(pos[0])
		if code != 0 {
			return code
		}
		q := "?limit=500"
		if len(pos) == 2 {
			q += "&state=" + url.QueryEscape(pos[1])
		}
		var out struct {
			Entries []struct {
				Name, Login, State, Detail string
			} `json:"entries"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/ban-lists/"+l.ID+"/entries"+q, nil, &out), stderr); code != 0 {
			return code
		}
		for _, e := range out.Entries {
			name := e.Login
			if name == "" {
				name = e.Name
			}
			line := fmt.Sprintf("%-26s %s", name, e.State)
			if e.Detail != "" {
				line += " (" + e.Detail + ")"
			}
			fmt.Fprintln(stdout, line)
		}
		return 0
	case "confirm", "pause", "resume", "compare", "undo", "forget":
		if len(pos) != 1 {
			return usage()
		}
		l, code := find(pos[0])
		if code != 0 {
			return code
		}
		switch sub {
		case "confirm":
			return post("/v1/ban-lists/"+l.ID+"/confirm", map[string]bool{"unban_removed": *unbanRemoved, "reban_by_hand": *rebanByHand},
				fmt.Sprintf("Banning %d at Twitch's pace, in the background.", l.Counts["waiting"]))
		case "pause":
			return post("/v1/ban-lists/"+l.ID+"/pause", nil, "Paused.")
		case "resume":
			return post("/v1/ban-lists/"+l.ID+"/resume", nil, "Carrying on.")
		case "compare":
			return post("/v1/ban-lists/"+l.ID+"/compare", nil, "Checking its bans against Twitch.")
		case "undo":
			n := l.Counts["banned"] + l.Counts["removed"]
			un, did, doing := "unbans", "banned", "Unbanning"
			if l.Kind == "block" {
				un, did, doing = "unblocks", "blocked", "Unblocking"
			}
			if !*yes {
				fmt.Fprintf(stdout, "This %s the %d accounts %s %s, at Twitch's pace. Add -yes to undo it.\n", un, n, l.Name, did)
				return 0
			}
			return post("/v1/ban-lists/"+l.ID+"/undo", nil, fmt.Sprintf("%s %d.", doing, n))
		default:
			if !*yes {
				fmt.Fprintf(stdout, "This forgets %s. Nobody is unbanned. Add -yes to forget it.\n", l.Name)
				return 0
			}
			if code := apiExit(c.Do(ctx, "DELETE", "/v1/ban-lists/"+l.ID, nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Forgot %s.\n", l.Name)
			return 0
		}
	case "save":
		if len(pos) < 2 || len(pos) > 3 {
			return usage()
		}
		l, code := find(pos[0])
		if code != 0 {
			return code
		}
		return saveBanList(ctx, c, l, pos, stdout, stderr)
	}
	return usage()
}

func saveBanList(ctx context.Context, c *client.Client, l api.BanListView, pos []string, stdout, stderr io.Writer) int {
	q := ""
	if len(pos) == 3 {
		q = "?state=" + url.QueryEscape(pos[2])
	}
	f, err := os.OpenFile(pos[1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the streamer's own path
	if errors.Is(err, os.ErrExist) {
		fmt.Fprintf(stderr, "%s exists already: choose another name.\n", pos[1])
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if code := apiExit(c.Do(ctx, "GET", "/v1/ban-lists/"+l.ID+"/file"+q, nil, f), stderr); code != 0 {
		_ = f.Close()
		_ = os.Remove(pos[1])
		return code
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Saved %s to %s.\n", l.Name, pos[1])
	return 0
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, " '\"") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func printBanList(w io.Writer, l api.BanListView) {
	fmt.Fprintf(w, "%s: %s\n", l.Name, l.Words)
	var parts []string
	for _, st := range []string{"waiting", "banned", "already", "not_found", "kept", "refused", "by_hand", "removed", "unbanned", "new"} {
		if n := l.Counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", strings.ReplaceAll(st, "_", " "), n))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(w, "  %s\n", strings.Join(parts, ", "))
	}
	if p := l.Progress; p.Unit != "" && (p.Done > 0 || p.All > 0) {
		if p.All > 0 {
			fmt.Fprintf(w, "  %d of %d %s", p.Done, p.All, p.Unit)
		} else {
			fmt.Fprintf(w, "  %d %s so far", p.Done, p.Unit)
		}
		if p.PerMinute > 0 {
			fmt.Fprintf(w, ", %.0f a minute", p.PerMinute)
		}
		if p.Finish != nil {
			fmt.Fprintf(w, ", finished about %s", p.Finish.Local().Format("Mon 15:04"))
		}
		fmt.Fprintln(w)
	}
	if l.Progress.After != "" && !l.Paused {
		fmt.Fprintf(w, "  Waiting for %q to finish first.\n", l.Progress.After)
	}
	if l.Progress.WaitingLive {
		fmt.Fprintln(w, "  Waiting until you're offline on Twitch.")
	}
}
