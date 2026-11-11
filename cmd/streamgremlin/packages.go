package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/packages"
)

const packagesUsage = `usage:
  streamgremlin packages [-json]              installed add-ons: wheel looks (themes)
  streamgremlin packages install FILE [-yes]  install a ZIP; without -yes, only says what it is
  streamgremlin packages remove ID [-yes]     remove one; overlays using it go back to Classic
  streamgremlin packages history [-json]      installs, removals and refusals, newest first

Publishers are as the package states them: StreamGremlin can't verify
them. An installed look runs its own code in OBS and the app's previews,
in a sandbox: it can't reach the internet, your accounts or StreamGremlin's
settings, and only shows the result StreamGremlin drew.`

type packageView struct {
	packages.Package
	UsedBy []string `json:"used_by"`
}

func packagesCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, packagesUsage)
		return 0
	}
	usage := func() int { fmt.Fprintln(stderr, packagesUsage); return 2 }
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("packages "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	yes := fs.Bool("yes", false, "do it, without only saying what would happen")
	// Flags may come after the file or id.
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return usage()
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch {
	case sub == "" && len(pos) == 0:
		var out struct {
			Packages []packageView `json:"packages"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/packages", nil, &out), stderr); code != 0 {
			return code
		}
		if *asJSON {
			return printJSON(stdout, out.Packages)
		}
		if len(out.Packages) == 0 {
			fmt.Fprintln(stdout, "No add-ons installed. Install a wheel look with: streamgremlin packages install FILE.zip")
			return 0
		}
		for _, p := range out.Packages {
			used := "not used yet"
			if len(p.UsedBy) > 0 {
				used = "used by " + strings.Join(p.UsedBy, ", ")
			}
			fmt.Fprintf(stdout, "%s  %s %s, wheel look, published by %s (not verified); %s\n", p.ID, p.Name, p.Version, p.Publisher, used)
		}
		return 0
	case sub == "install" && len(pos) == 1:
		fi, err := os.Stat(pos[0])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if fi.Size() > packages.MaxZipBytes {
			fmt.Fprintf(stderr, "%s is over %d MB\n", pos[0], packages.MaxZipBytes>>20)
			return 1
		}
		data, err := os.ReadFile(pos[0])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		var pv packages.Preview
		if code := apiExit(c.DoRaw(ctx, "POST", "/v1/packages/check", data, &pv), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "%s %s: a wheel look, published by %s (not verified).\n", pv.Name, pv.Version, pv.Publisher)
		if pv.Description != "" {
			fmt.Fprintln(stdout, pv.Description)
		}
		fmt.Fprintf(stdout, "%d files, %d KB.", pv.Files, max(1, pv.Size/1000))
		if pv.Sounds {
			fmt.Fprint(stdout, " It has its own sounds.")
		}
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "This look runs its own code in OBS and the app's previews. It can't reach the internet, your accounts or "+
			"StreamGremlin's settings, and it only shows the result StreamGremlin drew. StreamGremlin can't check how it draws the odds.")
		if pv.Same {
			fmt.Fprintln(stdout, "It's installed already.")
			return 0
		}
		if !*yes {
			fmt.Fprintln(stdout, "Run again with -yes to install it.")
			return 0
		}
		var p packages.Package
		if code := apiExit(c.DoRaw(ctx, "POST", "/v1/packages?"+url.Values{"digest": {pv.Digest}}.Encode(), data, &p), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Installed %s %s. Pick it for a wheel overlay: streamgremlin overlays look NAME -look %s\n", p.Name, p.Version, p.ID)
		return 0
	case sub == "remove" && len(pos) == 1:
		var out struct {
			Packages []packageView `json:"packages"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/packages", nil, &out), stderr); code != 0 {
			return code
		}
		var found *packageView
		for i := range out.Packages {
			if out.Packages[i].ID == pos[0] {
				found = &out.Packages[i]
			}
		}
		if found == nil {
			fmt.Fprintf(stderr, "%s isn't installed\n", pos[0])
			return 1
		}
		if !*yes {
			fmt.Fprintf(stdout, "Removing %s %s deletes its files.", found.Name, found.Version)
			if len(found.UsedBy) > 0 {
				fmt.Fprintf(stdout, " These overlays go back to the Classic look: %s.", strings.Join(found.UsedBy, ", "))
			}
			fmt.Fprintln(stdout, "\nRun again with -yes to remove it.")
			return 0
		}
		var res struct {
			Overlays []string `json:"overlays"`
		}
		if code := apiExit(c.Do(ctx, "DELETE", "/v1/packages/"+url.PathEscape(pos[0]), nil, &res), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Removed %s.", found.Name)
		if len(res.Overlays) > 0 {
			fmt.Fprintf(stdout, " Back to Classic: %s.", strings.Join(res.Overlays, ", "))
		}
		fmt.Fprintln(stdout)
		return 0
	case sub == "history" && len(pos) == 0:
		var out struct {
			Audit []packages.AuditEntry `json:"audit"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/packages/audit", nil, &out), stderr); code != 0 {
			return code
		}
		if *asJSON {
			return printJSON(stdout, out.Audit)
		}
		if len(out.Audit) == 0 {
			fmt.Fprintln(stdout, "Nothing yet.")
		}
		for _, e := range out.Audit {
			fmt.Fprintf(stdout, "%s  %-8s %s %s  %s\n", e.At.Local().Format("2006-01-02 15:04"), e.Action, e.PackageID, e.Version, e.Detail)
		}
		return 0
	}
	return usage()
}

func printJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return 0
}
