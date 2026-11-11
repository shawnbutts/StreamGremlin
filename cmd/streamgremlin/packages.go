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

	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/packages"
)

const packagesUsage = `usage:
  streamgremlin packages [-json]              installed add-ons: wheel looks (themes) and Lua plugins
  streamgremlin packages install FILE [-yes] [-publisher NAME] [-allow PERMISSIONS]
                                              install a ZIP, or update to it; without -yes, only
                                              says what it is. Replacing another publisher's
                                              also needs -publisher with the new one's name; a
                                              plugin needs -allow with each permission it asks for
                                              (an update, only when it asks for more)
  streamgremlin packages on|off ID            turn a plugin on or off
  streamgremlin packages set ID KEY=VALUE...  change a plugin's settings (it starts again, fresh)
  streamgremlin packages activity ID [-json]  what a plugin has asked StreamGremlin to do
  streamgremlin packages rollback ID [-yes] [-allow PERMISSIONS]
                                              back to the version before the installed one; a
                                              plugin's version that asks for more needs -allow
  streamgremlin packages remove ID [-yes]     remove one; overlays using a look go back to Classic,
                                              workflows using a plugin's steps are turned off
  streamgremlin packages history [-json]      installs, updates, roll backs, removals and refusals

Publishers are as the package states them: StreamGremlin can't verify
them. An installed look runs its own code in OBS and the app's previews,
in a sandbox: it can't reach the internet, your accounts or StreamGremlin's
settings. It's told the result StreamGremlin drew, but what it draws is up
to whoever made it: the real result is always in chat.`

type packageView struct {
	packages.Package
	UsedBy    []string `json:"used_by"`
	Workflows []string `json:"workflows"`
}

func orNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// versionAnswer is what an install, update or roll back did.
type versionAnswer struct {
	packages.Package
	From     string `json:"from"`
	Overlays []struct {
		Overlay string   `json:"overlay"`
		Reset   []string `json:"reset"`
	} `json:"overlays"`
	Workflows []string `json:"workflows"`
	Reset     []string `json:"reset"`
}

// pluginMoved says what a plugin's new version turned off and reset, and
// whether it's running.
func (a versionAnswer) pluginMoved(w io.Writer) {
	if len(a.Workflows) > 0 {
		fmt.Fprintf(w, " These workflows used a step it doesn't have and are turned off (not deleted): %s.", strings.Join(a.Workflows, ", "))
	}
	if len(a.Reset) > 0 {
		fmt.Fprintf(w, " Back to the default: %s.", strings.Join(a.Reset, ", "))
	}
	switch {
	case a.Plugin.Error != "":
		fmt.Fprintf(w, " It's off: %s.", a.Plugin.Error)
	case a.Plugin.Enabled:
		fmt.Fprintf(w, " It started again, fresh, as %s.", a.Version)
	}
	fmt.Fprintln(w)
}

// describeChange says what moving a plugin to another version does.
func describeChange(w io.Writer, id string, ch *packages.PluginChange) {
	if ch == nil {
		return
	}
	if len(ch.NewPermissions) > 0 {
		fmt.Fprintln(w, "It asks for more permissions:")
		for _, x := range ch.NewPermissions {
			fmt.Fprintf(w, "  %s  %s\n", x.Name, x.What)
		}
	}
	if len(ch.RemovedSteps) > 0 {
		var steps []string
		for _, a := range ch.RemovedSteps {
			steps = append(steps, fmt.Sprintf("%s (%s.%s)", a.Label, id, a.Name))
		}
		fmt.Fprintf(w, "Steps it doesn't have: %s.", strings.Join(steps, ", "))
		if len(ch.Workflows) > 0 {
			fmt.Fprintf(w, " These workflows use them and are turned off (not deleted): %s.", strings.Join(ch.Workflows, ", "))
		}
		fmt.Fprintln(w)
	}
	if len(ch.Reset) > 0 {
		fmt.Fprintf(w, "Settings that go back to their default: %s.\n", strings.Join(ch.Reset, ", "))
	}
	fmt.Fprintln(w, "If it's on, it starts again, fresh: its memory starts over.")
}

// newPermissions are the names of those a plugin's other version asks for
// that aren't approved now.
func newPermissions(ch *packages.PluginChange) []string {
	var out []string
	if ch != nil {
		for _, x := range ch.NewPermissions {
			out = append(out, x.Name)
		}
	}
	return out
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
	allow := fs.String("allow", "", "a plugin's permissions, comma-separated: each one it asks for")
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
			if p.Plugin != nil {
				state := "off"
				switch {
				case p.Plugin.Enabled:
					state = "on"
				case p.Plugin.Error != "":
					state = "off (" + p.Plugin.Error + ")"
				}
				perms := []string{}
				for _, x := range p.Plugin.Permissions {
					perms = append(perms, x.Name)
				}
				used := "no workflows use it"
				if len(p.Workflows) > 0 {
					used = "used by " + strings.Join(p.Workflows, ", ")
				}
				kept := ""
				if p.RollbackTo != "" {
					kept = "; can roll back to " + p.RollbackTo
				}
				fmt.Fprintf(stdout, "%s  %s %s, Lua plugin, published by %s (not verified); %s; permissions: %s; %s%s\n", p.ID, p.Name, p.Version,
					p.Publisher, state, orNone(perms), used, kept)
				continue
			}
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
		kind := "a wheel look"
		if pv.Plugin != nil {
			kind = "a Lua plugin"
		}
		fmt.Fprintf(stdout, "%s %s: %s, published by %s (not verified).\n", pv.Name, pv.Version, kind, pv.Publisher)
		if pv.Update && pv.Installed != nil {
			what := "An update"
			if pv.Older {
				what = "An older version"
			}
			together := "Overlays using it switch together."
			if pv.Plugin != nil {
				together = "Its settings are kept where this version still takes them."
			}
			fmt.Fprintf(stdout, "%s: %s is installed, and is kept so you can roll back to it. %s\n", what, pv.Installed.Version, together)
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
		if pv.Plugin != nil {
			fmt.Fprintln(stdout, "This plugin runs its own Lua in a separate process. It can't reach your files, the internet or your accounts, "+
				"only what you allow below. It keeps its own memory while it's on, and it's installed off.")
			fmt.Fprintln(stdout, "Steps it adds to workflows:")
			for _, a := range pv.Plugin.Actions {
				fmt.Fprintf(stdout, "  %s.%s  %s: %s\n", pv.ID, a.Name, a.Label, a.Description)
			}
			if len(pv.Plugin.Events) > 0 {
				fmt.Fprintln(stdout, "Events it sends, to start your workflows:")
				for _, e := range pv.Plugin.Events {
					fmt.Fprintf(stdout, "  %s  %s: %s\n", e.Type, e.Label, e.Description)
				}
			}
			if len(pv.Plugin.Templates) > 0 {
				var titles []string
				for _, t := range pv.Plugin.Templates {
					titles = append(titles, t.Title)
				}
				fmt.Fprintf(stdout, "Templates it adds to the gallery: %s.\n", strings.Join(titles, ", "))
			}
			if len(pv.Plugin.Permissions) == 0 {
				fmt.Fprintln(stdout, "It asks for no permissions.")
			} else if pv.PluginUpdate != nil && len(pv.PluginUpdate.NewPermissions) == 0 {
				fmt.Fprintf(stdout, "It asks for the permissions you allowed already: %s.\n", strings.Join(pv.Plugin.PermissionNames(), ", "))
			} else {
				fmt.Fprintln(stdout, "It asks for these permissions:")
				for _, x := range pv.Plugin.Permissions {
					fmt.Fprintf(stdout, "  %s  %s\n", x.Name, x.What)
				}
			}
			describeChange(stdout, pv.ID, pv.PluginUpdate)
		} else {
			fmt.Fprintln(stdout, "This look runs its own code in OBS and the app's previews. It can't reach the internet, your accounts or "+
				"StreamGremlin's settings. It's told the result StreamGremlin drew, but what it draws is up to whoever made it: the real result is "+
				"always in chat. StreamGremlin can't check how it draws the odds.")
			fmt.Fprintln(stdout, `In OBS, it only loads while the wheel's browser source has "Page permissions" at "No access to OBS"; `+
				"otherwise Classic shows instead.")
		}
		if pv.Same {
			fmt.Fprintln(stdout, "It's installed already.")
			return 0
		}
		confirm := ""
		if pv.PublisherChange {
			confirm = fmt.Sprintf(" -publisher %q", pv.Publisher)
		}
		var asked []string
		mustAllow := false
		if pv.Plugin != nil {
			asked = pv.Plugin.PermissionNames()
			// An update asks only when it asks for more.
			mustAllow = len(asked) > 0 && (pv.PluginUpdate == nil || len(pv.PluginUpdate.NewPermissions) > 0)
			if mustAllow {
				confirm += " -allow " + strings.Join(asked, ",")
			}
		}
		if !*yes {
			fmt.Fprintf(stdout, "Run again with -yes%s to install it.\n", confirm)
			return 0
		}
		if pv.PublisherChange && *publisher != pv.Publisher {
			fmt.Fprintf(stderr, "To replace %s's add-on, confirm the new publisher by name: -publisher %q\n", pv.Installed.Publisher, pv.Publisher)
			return 1
		}
		if mustAllow && *allow != strings.Join(asked, ",") {
			fmt.Fprintf(stderr, "%s asks for %s: approve each with -allow %s\n", pv.Name, orNone(asked), strings.Join(asked, ","))
			return 1
		}
		q := url.Values{"digest": {pv.Digest}}
		if pv.PublisherChange {
			q.Set("publisher", *publisher)
		}
		if *allow != "" {
			q.Set("permissions", *allow)
		}
		var a versionAnswer
		if code := apiExit(c.DoRaw(ctx, "POST", "/v1/packages?"+q.Encode(), data, &a), stderr); code != 0 {
			return code
		}
		if a.From != "" {
			fmt.Fprintf(stdout, "Updated %s from %s to %s.", a.Name, a.From, a.Version)
			if a.Plugin != nil {
				a.pluginMoved(stdout)
			} else {
				a.moved(stdout)
			}
			return 0
		}
		if a.Plugin != nil {
			fmt.Fprintf(stdout, "Installed %s %s, off. Turn it on with: streamgremlin packages on %s\n", a.Name, a.Version, a.ID)
			return 0
		}
		fmt.Fprintf(stdout, "Installed %s %s. Pick it for a wheel overlay: streamgremlin overlays look NAME -look %s\n", a.Name, a.Version, a.ID)
		return 0
	case (sub == "on" || sub == "off") && len(pos) == 1:
		var p packages.Package
		if code := apiExit(c.Do(ctx, "PUT", "/v1/packages/"+url.PathEscape(pos[0])+"/enabled", map[string]bool{"enabled": sub == "on"}, &p), stderr); code != 0 {
			return code
		}
		if sub == "on" {
			fmt.Fprintf(stdout, "%s is on.\n", p.Name)
		} else {
			fmt.Fprintf(stdout, "%s is off. Its steps fail until it's on again; its workflows stay on.\n", p.Name)
		}
		return 0
	case sub == "set" && len(pos) >= 2:
		var cur packages.Package
		var list struct {
			Packages []packages.Package `json:"packages"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/packages", nil, &list), stderr); code != 0 {
			return code
		}
		for _, p := range list.Packages {
			if p.ID == pos[0] {
				cur = p
			}
		}
		if cur.Plugin == nil {
			fmt.Fprintf(stderr, "%s isn't an installed plugin\n", pos[0])
			return 1
		}
		values := map[string]any{}
		for _, kv := range pos[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				fmt.Fprintf(stderr, "%s: settings are KEY=VALUE\n", kv)
				return 2
			}
			val, err := settingValue(&overlays.Theme{Name: cur.Name, Settings: cur.Settings}, k, v)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
			values[k] = val
		}
		var p packages.Package
		if code := apiExit(c.Do(ctx, "PUT", "/v1/packages/"+url.PathEscape(pos[0])+"/settings", map[string]any{"settings": values}, &p), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Saved %s's settings.", p.Name)
		if p.Plugin != nil && p.Plugin.Enabled {
			fmt.Fprint(stdout, " It started again, fresh, to read them.")
		}
		if p.Plugin != nil && p.Plugin.Error != "" {
			fmt.Fprintf(stdout, " It's off: %s.", p.Plugin.Error)
		}
		fmt.Fprintln(stdout)
		return 0
	case sub == "activity" && len(pos) == 1:
		var out struct {
			Activity []packages.Activity `json:"activity"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/packages/"+url.PathEscape(pos[0])+"/activity", nil, &out), stderr); code != 0 {
			return code
		}
		if *asJSON {
			return printJSON(stdout, out.Activity)
		}
		if len(out.Activity) == 0 {
			fmt.Fprintln(stdout, "Nothing yet.")
		}
		for i := len(out.Activity) - 1; i >= 0; i-- {
			e := out.Activity[i]
			fmt.Fprintf(stdout, "%s  %-9s %-7s %s\n", e.At.Local().Format("2006-01-02 15:04:05"), e.Call, e.Outcome, e.Detail)
		}
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
		}
		var rb *packages.PluginChange
		if found.Plugin != nil {
			rb = found.Plugin.Rollback
		}
		more := newPermissions(rb)
		if !*yes {
			fmt.Fprintf(stdout, "Rolling %s back from %s to %s keeps %s, so you can update to it again.", found.Name, found.Version, found.RollbackTo, found.Version)
			if len(found.UsedBy) > 0 {
				fmt.Fprintf(stdout, " These overlays switch back with it: %s.", strings.Join(found.UsedBy, ", "))
			}
			fmt.Fprintln(stdout)
			describeChange(stdout, found.ID, rb)
			confirm := ""
			if len(more) > 0 {
				confirm = " -allow " + strings.Join(rb.Permissions, ",")
			}
			fmt.Fprintf(stdout, "Run again with -yes%s to roll back.\n", confirm)
			return 0
		}
		path := "/v1/packages/" + url.PathEscape(pos[0]) + "/rollback"
		if len(more) > 0 {
			want := strings.Join(rb.Permissions, ",")
			if *allow != want {
				fmt.Fprintf(stderr, "%s %s asks for more: %s. Approve each with -allow %s\n", found.Name, found.RollbackTo, strings.Join(more, ", "), want)
				return 1
			}
			path += "?" + url.Values{"permissions": {*allow}}.Encode()
		}
		var a versionAnswer
		if code := apiExit(c.Do(ctx, "POST", path, nil, &a), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Rolled %s back from %s to %s.", a.Name, a.From, a.Version)
		if a.Plugin != nil {
			a.pluginMoved(stdout)
		} else {
			a.moved(stdout)
		}
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
			if len(found.Workflows) > 0 {
				fmt.Fprintf(stdout, " These workflows use its steps and are turned off (not deleted): %s.", strings.Join(found.Workflows, ", "))
			}
			fmt.Fprintln(stdout, "\nRun again with -yes to remove it.")
			return 0
		}
		var res struct {
			Overlays  []string `json:"overlays"`
			Workflows []string `json:"workflows"`
		}
		if code := apiExit(c.Do(ctx, "DELETE", "/v1/packages/"+url.PathEscape(pos[0]), nil, &res), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Removed %s.", found.Name)
		if len(res.Overlays) > 0 {
			fmt.Fprintf(stdout, " Back to Classic: %s.", strings.Join(res.Overlays, ", "))
		}
		if len(res.Workflows) > 0 {
			fmt.Fprintf(stdout, " Workflows that used it (now off): %s.", strings.Join(res.Workflows, ", "))
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
