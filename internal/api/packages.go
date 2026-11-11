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
					}
					out = append(out, v)
				}
				writeJSON(w, http.StatusOK, map[string]any{"packages": out})
			},
		},
		{
			Method: "POST", Path: "/v1/packages/check",
			Summary: "Read a package (the body is its ZIP, up to 16 MB) and say what it is and what installing it would do. " +
				"Stores nothing. A package that can't be installed is refused (400, or 409 when it clashes with what's installed).",
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
				"each approved (otherwise 409 confirm_permissions); it's installed off. Exactly what's installed again is harmless (200).",
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
				p, from, created, err := d.Packages.InstallWith(r.Context(), data,
					packages.Approval{Digest: dg, Publisher: r.URL.Query().Get("publisher"), Permissions: perms}, d.Now())
				if err != nil {
					d.Logger.Warn("package refused", "err", err)
					packageError(w, r, d, err)
					return
				}
				if !created {
					writeJSON(w, http.StatusOK, VersionAnswer{Package: p, Overlays: []Changed{}})
					return
				}
				changed := []Changed{}
				if p.Plugin != nil {
					if d.Plugins != nil {
						if err := d.Plugins.Installed(r.Context(), p.ID); err != nil {
							internalError(w, r, d.Logger, err)
							return
						}
					}
				} else if changed, err = switchVersion(r.Context(), d, p.ID); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				d.Logger.Info("package installed", "package", p.ID, "version", p.Version, "from", from, "publisher", p.Publisher, "digest", p.Digest)
				writeJSON(w, http.StatusCreated, VersionAnswer{Package: p, From: from, Overlays: changed})
			},
		},
		{
			Method: "POST", Path: "/v1/packages/{id}/rollback",
			Summary: "Roll a package back to the version before the installed one (its rollback_to), kept from an earlier update. " +
				"Overlays using it move back together, keeping the settings that version takes. 409 when there's none.",
			Permission: PermPackagesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				p, from, err := d.Packages.Rollback(r.Context(), r.PathValue("id"), d.Now())
				if err != nil {
					packageError(w, r, d, err)
					return
				}
				changed, err := switchVersion(r.Context(), d, p.ID)
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				d.Logger.Info("package rolled back", "package", p.ID, "version", p.Version, "from", from)
				writeJSON(w, http.StatusOK, VersionAnswer{Package: p, From: from, Overlays: changed})
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
