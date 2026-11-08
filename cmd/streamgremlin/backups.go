package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/backup"
)

const backupsUsage = `usage:
  streamgremlin backups                   list them, newest first
  streamgremlin backups create            back up now
  streamgremlin backups verify NAME       check one fully
  streamgremlin backups restore NAME      restore it when StreamGremlin next starts
  streamgremlin backups cancel-restore    don't, after all
  streamgremlin backups delete NAME       delete one

Backups hold your workflows, runs, events, variables and settings, never
sign-ins or passwords: those stay in your keyring.`

func backupsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h") {
		fmt.Fprintln(stdout, backupsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	esc := url.PathEscape
	switch {
	case len(args) == 0:
		var out api.Backups
		if code := apiExit(c.Do(ctx, "GET", "/v1/backups", nil, &out), stderr); code != 0 {
			return code
		}
		if len(out.Backups) == 0 {
			fmt.Fprintln(stdout, "No backups yet. Make one with `streamgremlin backups create`.")
		}
		for _, b := range out.Backups {
			note := ""
			if b.Name == out.Pending {
				note = "  (restored at the next start)"
			}
			if b.Problem != "" {
				note += "  ! " + b.Problem
			}
			fmt.Fprintf(stdout, "%-56s %-13s %s  %6.1f MB%s\n", b.Name, b.Kind, b.CreatedAt.Local().Format("2006-01-02 15:04"), float64(b.Size)/1e6, note)
		}
		if r := out.LastRestore; r != nil {
			if r.OK {
				fmt.Fprintf(stdout, "\nLast restore: %s, from %s.\n", r.At.Local().Format("2006-01-02 15:04"), r.From)
			} else {
				fmt.Fprintf(stdout, "\nThe last restore failed (%s), and nothing was changed: %s\n", r.From, r.Error)
			}
		}
		return 0
	case len(args) == 1 && args[0] == "create":
		var b backup.Info
		if code := apiExit(c.Do(ctx, "POST", "/v1/backups", nil, &b), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Backed up to %s\n", b.Name)
		return 0
	case len(args) == 2 && args[0] == "verify":
		if code := apiExit(c.Do(ctx, "POST", "/v1/backups/"+esc(args[1])+"/verify", nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "It's good: every file matches, and this version can restore it.")
		return 0
	case len(args) == 2 && args[0] == "restore":
		if code := apiExit(c.Do(ctx, "POST", "/v1/backups/"+esc(args[1])+"/restore", nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "It checks out. Restart StreamGremlin to restore it: what it replaces is backed up first.")
		return 0
	case len(args) == 1 && args[0] == "cancel-restore":
		var out api.Backups
		if code := apiExit(c.Do(ctx, "GET", "/v1/backups", nil, &out), stderr); code != 0 {
			return code
		}
		if out.Pending == "" {
			fmt.Fprintln(stdout, "No restore is waiting.")
			return 0
		}
		if code := apiExit(c.Do(ctx, "DELETE", "/v1/backups/"+esc(out.Pending)+"/restore", nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "Cancelled: nothing will be restored.")
		return 0
	case len(args) == 2 && args[0] == "delete":
		if code := apiExit(c.Do(ctx, "DELETE", "/v1/backups/"+esc(args[1]), nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Deleted %s\n", args[1])
		return 0
	}
	fmt.Fprintln(stderr, backupsUsage)
	return 2
}
