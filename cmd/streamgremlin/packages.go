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
  streamgremlin packages install FILE [-yes] [-publisher NAME]
                                              install a ZIP, or update to it; without -yes, only
                                              says what it is. Replacing another publisher's
                                              also needs -publisher with the new one's name
  streamgremlin packages rollback ID [-yes]   back to the version before the installed one
  streamgremlin packages remove ID [-yes]     remove one; overlays using it go back to Classic
  streamgremlin packages history [-json]      installs, updates, roll backs, removals and refusals

Publishers are as the package states them: StreamGremlin can't verify
them. An installed look runs its own code in OBS and the app's previews,
in a sandbox: it can't reach the internet, your accounts or StreamGremlin's
settings. It's told the result StreamGremlin drew, but what it draws is up
to whoever made it: the real result is always in chat.`

type packageView struct {
	packages.Package
	UsedBy []string `json:"used_by"`
}

// versionAnswer is what an install, update or roll back did.
type versionAnswer struct {
	packages.Package
	From     string `json:"from"`
	Overlays []struct {
		Overlay string   `json:"overlay"`
		Reset   []string `json:"reset"`
	} `json:"overlays"`
}

// moved says which overlays moved to a new version, and what they had reset.
func (a versionAnswer) moved(w io.Writer) {
	var names []string
	for _, o := range a.Overlays {
		names = append(names, o.Overlay)
	}
	if len(names) > 0 {
		fmt.Fprintf(w, " Using it: %s.", strings.Join(names, ", "))
	}
	for _, o := range a.Overlays {
		if len(o.Reset) > 0 {
			fmt.Fprintf(w, " On %s, %s went back to the default.", o.Overlay, strings.Join(o.Reset, ", "))
		}
	}
	fmt.Fprintln(w)
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
	publisher := fs.String("publisher", "", "the new publisher's name, exactly, to replace another publisher's add-on")
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
			kept := ""
			if p.RollbackTo != "" {
				kept = "; can roll back to " + p.RollbackTo
			}
			fmt.Fprintf(stdout, "%s  %s %s, wheel look, published by %s (not verified); %s%s\n", p.ID, p.Name, p.Version, p.Publisher, used, kept)
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
		if pv.Update && pv.Installed != nil {
			what := "An update"
			if pv.Older {
				what = "An older version"
			}
			fmt.Fprintf(stdout, "%s: %s is installed, and is kept so you can roll back to it. Overlays using it switch together.\n",
				what, pv.Installed.Version)
		}
		if pv.PublisherChange && pv.Installed != nil {
			fmt.Fprintf(stdout, "Another publisher: the installed one says %s, this one says %s. Neither is verified: someone else may have used the same id.\n",
				pv.Installed.Publisher, pv.Publisher)
		}
		if pv.Description != "" {
			fmt.Fprintln(stdout, pv.Description)
		}
		fmt.Fprintf(stdout, "%d files, %d KB.", pv.Files, max(1, pv.Size/1000))
		if pv.Sounds {
			fmt.Fprint(stdout, " It has its own sounds.")
		}
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "This look runs its own code in OBS and the app's previews. It can't reach the internet, your accounts or "+
			"StreamGremlin's settings. It's told the result StreamGremlin drew, but what it draws is up to whoever made it: the real result is "+
			"always in chat. StreamGremlin can't check how it draws the odds.")
		fmt.Fprintln(stdout, `In OBS, it only loads while the wheel's browser source has "Page permissions" at "No access to OBS"; `+
			"otherwise Classic shows instead.")
		if pv.Same {
			fmt.Fprintln(stdout, "It's installed already.")
			return 0
		}
		confirm := ""
		if pv.PublisherChange {
			confirm = fmt.Sprintf(" -publisher %q", pv.Publisher)
		}
		if !*yes {
			fmt.Fprintf(stdout, "Run again with -yes%s to install it.\n", confirm)
			return 0
		}
		if pv.PublisherChange && *publisher != pv.Publisher {
			fmt.Fprintf(stderr, "To replace %s's add-on, confirm the new publisher by name: -publisher %q\n", pv.Installed.Publisher, pv.Publisher)
			return 1
		}
		q := url.Values{"digest": {pv.Digest}}
		if pv.PublisherChange {
			q.Set("publisher", *publisher)
		}
		var a versionAnswer
		if code := apiExit(c.DoRaw(ctx, "POST", "/v1/packages?"+q.Encode(), data, &a), stderr); code != 0 {
			return code
		}
		if a.From != "" {
			fmt.Fprintf(stdout, "Updated %s from %s to %s.", a.Name, a.From, a.Version)
			a.moved(stdout)
			return 0
		}
		fmt.Fprintf(stdout, "Installed %s %s. Pick it for a wheel overlay: streamgremlin overlays look NAME -look %s\n", a.Name, a.Version, a.ID)
		return 0
	case sub == "rollback" && len(pos) == 1:
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
		switch {
		case found == nil:
			fmt.Fprintf(stderr, "%s isn't installed\n", pos[0])
			return 1
		case found.RollbackTo == "":
			fmt.Fprintf(stderr, "There's no version of %s before %s to roll back to\n", found.Name, found.Version)
			return 1
		case !*yes:
			fmt.Fprintf(stdout, "Rolling %s back from %s to %s keeps %s, so you can update to it again.", found.Name, found.Version, found.RollbackTo, found.Version)
			if len(found.UsedBy) > 0 {
				fmt.Fprintf(stdout, " These overlays switch back with it: %s.", strings.Join(found.UsedBy, ", "))
			}
			fmt.Fprintln(stdout, "\nRun again with -yes to roll back.")
			return 0
		}
		var a versionAnswer
		if code := apiExit(c.Do(ctx, "POST", "/v1/packages/"+url.PathEscape(pos[0])+"/rollback", nil, &a), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Rolled %s back from %s to %s.", a.Name, a.From, a.Version)
		a.moved(stdout)
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
