package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
)

// banLists makes two ban lists for the picture: a finished one, and a big
// shared list part way through being looked up at Twitch's pace. Their
// names are made up and never shown.
func banLists(ctx context.Context, c *client.Client, tw *mocktwitch.Server) error {
	dir, err := os.MkdirTemp("", "sg-shots-lists-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// file writes n made-up names; most are accounts, some don't exist,
	// and some are banned already.
	file := func(name, prefix string, n int, extra ...string) (string, error) {
		lines := append([]string{}, extra...)
		for i := range n {
			login := fmt.Sprintf("%s%06d", prefix, i)
			lines = append(lines, login)
			if i%13 == 0 {
				continue // no such account
			}
			tw.UserID(login)
			if i%29 == 0 {
				tw.SetBanned(channel, login, true, time.Time{}, "spam", streamer)
			}
		}
		p := filepath.Join(dir, name)
		return p, os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	}
	importList := func(name, path string) (api.BanListView, error) {
		var l api.BanListView
		if err := c.Do(ctx, "POST", "/v1/ban-lists", map[string]string{"name": name, "path": path}, &l); err != nil {
			return l, fmt.Errorf("import %s: %w", name, err)
		}
		// The channel is live in the pictures; let the lists run anyway.
		return l, c.Do(ctx, "PATCH", "/v1/ban-lists/"+l.ID, map[string]bool{"pause_live": false}, nil)
	}
	waitFor := func(id string, ok func(api.BanListView) bool) error {
		for deadline := time.Now().Add(4 * time.Minute); time.Now().Before(deadline); {
			var out struct {
				Lists []api.BanListView `json:"lists"`
			}
			if err := c.Do(ctx, "GET", "/v1/ban-lists", nil, &out); err != nil {
				return err
			}
			for _, l := range out.Lists {
				if l.ID == id && ok(l) {
					return nil
				}
			}
			if pause(ctx, 300); ctx.Err() != nil {
				return ctx.Err()
			}
		}
		return errors.New("a ban list didn't get there in time")
	}

	// A finished list from an earlier raid.
	p, err := file("spam-raid-nov-3.txt", "spamwave", 1850)
	if err != nil {
		return err
	}
	done, err := importList("Spam raid, 3 November", p)
	if err != nil {
		return err
	}
	if err := waitFor(done.ID, func(l api.BanListView) bool { return l.Phase == "ready" }); err != nil {
		return err
	}
	if err := c.Do(ctx, "POST", "/v1/ban-lists/"+done.ID+"/confirm", map[string]any{}, nil); err != nil {
		return err
	}
	if err := waitFor(done.ID, func(l api.BanListView) bool { return l.Phase == "done" }); err != nil {
		return err
	}

	// From here Twitch's pace is the real one: about 800 requests a
	// minute, of which StreamGremlin leaves 200 spare for chat.
	tw.SetRateLimit(400, 20*time.Second)
	// A big shared list, with two of your mods and two viewers with
	// points in it, being looked up.
	p, err = file("hate-raid-accounts.txt", "raidacct", 250000, "mossytoad", "kestrelhelper", "lunarlynx", "byte_badger")
	if err != nil {
		return err
	}
	big, err := importList("Shared hate-raid list", p)
	if err != nil {
		return err
	}
	// A minute and more in, so the pace shown is Twitch's, not a burst.
	return waitFor(big.ID, func(l api.BanListView) bool { return l.Phase == "checking" && l.Progress.Done >= 25000 })
}
