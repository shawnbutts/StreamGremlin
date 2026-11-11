package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/packages"
	"code.streamgremlin.com/streamgremlin/internal/plugins"
)

// Packages: installed overlay themes, and later Lua plugins
// (docs/adr/0036-packages-plugins-and-themes.md).

func packageError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	var tooBig *http.MaxBytesError
	msg := err.Error()
	for _, prefix := range []string{"packages: invalid: ", "packages: conflict: ", "packages: full: ", "packages: confirm: ",
		"packages: confirm permissions: ", "plugins: off: ", "plugins: didn't load: "} {
		msg = strings.TrimPrefix(msg, prefix)
	}
	switch {
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "a package is up to 16 MB")
	case errors.Is(err, packages.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, msg)
	case errors.Is(err, packages.ErrConflict), errors.Is(err, packages.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, msg)
	case errors.Is(err, packages.ErrConfirm):
		writeError(w, http.StatusConflict, CodeConfirmPublisher, msg)
	case errors.Is(err, packages.ErrConfirmPermissions):
		writeError(w, http.StatusConflict, CodeConfirmPermissions, msg)
	case errors.Is(err, plugins.ErrLoad), errors.Is(err, plugins.ErrOff):
		writeError(w, http.StatusConflict, CodeConflict, msg)
	case errors.Is(err, packages.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such package is installed")
	default:
		internalError(w, r, d.Logger, err)
	}
}

// PackageView is an installed package, with the overlays using it.
type PackageView struct {
	packages.Package
	// UsedBy are the overlays whose look it is, by name.
	UsedBy []string `json:"used_by"`
	// Workflows are those using a plugin's steps, by name.
	Workflows []string `json:"workflows,omitempty"`
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// refreshThemes tells overlays what's installed now.
func refreshThemes(ctx context.Context, d Deps) error {
	themes, err := d.Packages.Themes(ctx)
	if err != nil {
		return err
	}
	d.Themes.SetInstalled(themes)
	return nil
}

// Changed is what a new version did to an overlay using it: settings it
// had that the new version doesn't take are back to their defaults.
type Changed struct {
	Overlay string   `json:"overlay"`
	Reset   []string `json:"reset"`
}

// switchVersion tells overlays a package's version changed: they move to
// it together, each keeping the settings the new version still takes.
func switchVersion(ctx context.Context, d Deps, id string) ([]Changed, error) {
	if err := refreshThemes(ctx, d); err != nil {
		return nil, err
	}
	refits, err := d.Overlays.RefitTheme(ctx, id, d.Now())
	if err != nil {
		return nil, err
	}
	out := make([]Changed, 0, len(refits))
	for _, rf := range refits {
		d.Hub.Changed(rf.Overlay)
		out = append(out, Changed{Overlay: rf.Overlay.Name, Reset: rf.Reset})
	}
	return out, nil
}

// VersionAnswer is an install, update or roll back's answer.
type VersionAnswer struct {
	packages.Package
	// From is the version installed before, for an update or roll back.
	From string `json:"from,omitempty"`
	// Overlays are those using it, and what each had reset.
	Overlays []Changed `json:"overlays"`
	// Workflows a plugin's new version turned off, by name: they used a
	// step it doesn't have. Reset are its settings back to their default.
	Workflows []string `json:"workflows,omitempty"`
	Reset     []string `json:"reset,omitempty"`
}

// stepNames are a plugin's steps as workflows name them.
func stepNames(id string, actions []packages.Action) []string {
	out := []string{}
	for _, a := range actions {
		out = append(out, id+"."+a.Name)
	}
	return out
}

// affected fills in the workflows using the steps a plugin's other version
// drops.
func affected(ctx context.Context, d Deps, id string, ch *packages.PluginChange) error {
	if ch == nil || len(ch.RemovedSteps) == 0 || d.Workflows == nil {
		return nil
	}
	using, err := plugins.UsingSteps(ctx, d.Workflows, stepNames(id, ch.RemovedSteps))
	if err != nil {
		return err
	}
	for _, w := range using {
		ch.Workflows = append(ch.Workflows, w.Name)
	}
	return nil
}

// pluginSwitched moves a running plugin to the version now installed: its
// steps, the workflows using ones it dropped, and its worker. One that
// won't load is left off, saying why, in the package answered.
func pluginSwitched(ctx context.Context, d Deps, in packages.Installed) (VersionAnswer, error) {
	a := VersionAnswer{Package: in.Package, From: in.From, Overlays: []Changed{}, Reset: in.Reset}
	if d.Plugins == nil {
		return a, nil
	}
	off, err := d.Plugins.Switched(ctx, in.ID, d.Workflows)
	switch {
	case errors.Is(err, plugins.ErrLoad):
		d.Logger.Warn("plugin's new version didn't load", "plugin", in.ID, "version", in.Version, "err", err)
	case err != nil:
		return a, err
	}
	a.Workflows = off
	a.Package, err = d.Packages.Get(ctx, in.ID)
	return a, err
}

func packageRoutes(d Deps) []Route {
	readZip := func(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			packageError(w, r, d, err)
			return nil, false
		}
		return data, true
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/packages",
			Summary: "Installed packages (overlay themes and Lua plugins), with their version, publisher (as stated, not verified), " +
				"settings, and the overlays using each; for a plugin, whether it's on (or why it failed), its permissions, steps and " +
				"settings' values, and the workflows using it.",
			Permission: PermPackagesRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Packages.List(r.Context())
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				all, err := d.Overlays.List(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				out := make([]PackageView, 0, len(list))
				for _, p := range list {
					v := PackageView{Package: p, UsedBy: []string{}}
					for _, o := range all {
						if o.Look.Theme == p.ID {
							v.UsedBy = append(v.UsedBy, o.Name)
						}
					}
					if p.Plugin != nil {
						if v.Workflows, err = plugins.Using(r.Context(), d.Workflows, p.ID); err != nil {
							internalError(w, r, d.Logger, err)
							return
						}
						if err := affected(r.Context(), d, p.ID, p.Plugin.Rollback); err != nil {
							internalError(w, r, d.Logger, err)
							return
						}
					}
					out = append(out, v)
				}
				writeJSON(w, http.StatusOK, map[string]any{"packages": out})
			},
		},
		{
			Method: "POST", Path: "/v1/packages/check",
			Summary: "Read a package (the body is its ZIP, up to 16 MB) and say what it is and what installing it would do. " +
				"Stores nothing. For a plugin's update, plugin_update names the permissions it asks for that aren't approved, the steps it " +
				"drops, the workflows using them and the settings that go back to their default. A package that can't be installed is " +
				"refused (400, or 409 when it clashes with what's installed).",
			Permission: PermPackagesWrite, MaxBody: packages.MaxZipBytes + 1,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				data, ok := readZip(w, r)
				if !ok {
					return
				}
				pv, _, err := d.Packages.Check(r.Context(), data)
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				if err := affected(r.Context(), d, pv.ID, pv.PluginUpdate); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, pv)
			},
		},
		{
			Method: "POST", Path: "/v1/packages",
			Summary: "Install a package the streamer was shown and confirmed: the body is its ZIP again, ?digest= the digest " +
				"/v1/packages/check gave (anything else is refused). Another version of an installed id updates it (the old one " +
				"is kept, to roll back to) and moves the overlays using it, each keeping the settings the new version takes. One " +
				"from another publisher than the installed one also needs ?publisher= the new publisher's name, exactly " +
				"(otherwise 409 confirm_publisher). A Lua plugin needs ?permissions= exactly the permissions it asks for, comma-separated, " +
				"each approved (otherwise 409 confirm_permissions); it's installed off. Updating a plugin asks only when the new version asks " +
				"for a permission not approved now; workflows using steps it drops are turned off and named, its settings kept where they " +
				"fit, and if it's on it's loaded again, fresh. Exactly what's installed again is harmless (200).",
			Permission: PermPackagesWrite, MaxBody: packages.MaxZipBytes + 1,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				dg := r.URL.Query().Get("digest")
				if !digestPattern.MatchString(dg) {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "digest is the one /v1/packages/check gave: check the package first")
					return
				}
				data, ok := readZip(w, r)
				if !ok {
					return
				}
				var perms []string
				if q := r.URL.Query().Get("permissions"); q != "" {
					perms = strings.Split(q, ",")
				}
				in, err := d.Packages.InstallWith(r.Context(), data,
					packages.Approval{Digest: dg, Publisher: r.URL.Query().Get("publisher"), Permissions: perms}, d.Now())
				if err != nil {
					d.Logger.Warn("package refused", "err", err)
					packageError(w, r, d, err)
					return
				}
				p := in.Package
				if !in.Changed {
					writeJSON(w, http.StatusOK, VersionAnswer{Package: p, Overlays: []Changed{}})
					return
				}
				answer := VersionAnswer{Package: p, From: in.From, Overlays: []Changed{}}
				switch {
				case p.Plugin != nil && in.From != "":
					if answer, err = pluginSwitched(r.Context(), d, in); err != nil {
						internalError(w, r, d.Logger, err)
						return
					}
				case p.Plugin != nil:
					if d.Plugins != nil {
						if err := d.Plugins.Installed(r.Context(), p.ID); err != nil {
							internalError(w, r, d.Logger, err)
							return
						}
					}
				default:
					if answer.Overlays, err = switchVersion(r.Context(), d, p.ID); err != nil {
						internalError(w, r, d.Logger, err)
						return
					}
				}
				d.Logger.Info("package installed", "package", p.ID, "version", p.Version, "from", in.From, "publisher", p.Publisher, "digest", p.Digest,
					"workflows_off", answer.Workflows, "settings_reset", in.Reset)
				writeJSON(w, http.StatusCreated, answer)
			},
		},
		{
			Method: "POST", Path: "/v1/packages/{id}/rollback",
			Summary: "Roll a package back to the version before the installed one (its rollback_to), kept from an earlier update. " +
				"Overlays using it move back together, keeping the settings that version takes. A plugin is loaded again as that version " +
				"if it's on; workflows using steps that version doesn't have are turned off; one that asks for permissions not approved " +
				"now needs ?permissions= exactly those it asks for (otherwise 409 confirm_permissions). 409 when there's none.",
			Permission: PermPackagesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var perms []string
				if q := r.URL.Query().Get("permissions"); q != "" {
					perms = strings.Split(q, ",")
				}
				in, err := d.Packages.RollbackWith(r.Context(), r.PathValue("id"), perms, d.Now())
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				answer := VersionAnswer{Package: in.Package, From: in.From, Overlays: []Changed{}}
				if in.Plugin != nil {
					answer, err = pluginSwitched(r.Context(), d, in)
				} else {
					answer.Overlays, err = switchVersion(r.Context(), d, in.ID)
				}
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				d.Logger.Info("package rolled back", "package", in.ID, "version", in.Version, "from", in.From, "workflows_off", answer.Workflows)
				writeJSON(w, http.StatusOK, answer)
			},
		},
		{
			Method: "DELETE", Path: "/v1/packages/{id}",
			Summary: "Remove a package: every overlay using a theme goes back to the Classic look first; a plugin is stopped and " +
				"every workflow using its steps is turned off (never deleted). Its files, versions, settings and activity are deleted; " +
				"its audit stays. Answers the package, the overlays changed and the workflows using it.",
			Permission: PermPackagesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				id := r.PathValue("id")
				cur, err := d.Packages.Get(r.Context(), id)
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				workflows := []string{}
				if cur.Plugin != nil && d.Plugins != nil {
					if workflows, err = d.Plugins.Removing(r.Context(), id, d.Workflows); err != nil {
						internalError(w, r, d.Logger, err)
						return
					}
				}
				// Classic first, so no overlay is ever left with a look
				// that isn't there.
				changed, err := d.Overlays.ResetTheme(r.Context(), id, d.Now())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				names := []string{}
				for _, o := range changed {
					names = append(names, o.Name)
				}
				p, err := d.Packages.Remove(r.Context(), id, d.Now())
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				if err := refreshThemes(r.Context(), d); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				for _, o := range changed {
					d.Hub.Changed(o)
				}
				d.Logger.Info("package removed", "package", p.ID, "version", p.Version, "overlays_to_classic", names, "workflows_off", workflows)
				writeJSON(w, http.StatusOK, map[string]any{"package": p, "overlays": names, "workflows": workflows})
			},
		},
		{
			Method: "GET", Path: "/v1/packages/audit",
			Summary:    "The package audit, newest first: installs, removals and refusals, kept after a package is removed. ?limit= 1-200 (50).",
			Permission: PermPackagesRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				limit, err := intParam(r.URL.Query().Get("limit"), 50, 1, 200)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit is 1 to 200")
					return
				}
				list, err := d.Packages.Audit(r.Context(), limit)
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"audit": list})
			},
		},
	}
}
