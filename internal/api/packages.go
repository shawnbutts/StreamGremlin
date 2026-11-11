package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/packages"
)

// Packages: installed overlay themes, and later Lua plugins
// (docs/adr/0036-packages-plugins-and-themes.md).

func packageError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	var tooBig *http.MaxBytesError
	msg := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(err.Error(), "packages: invalid: "), "packages: conflict: "), "packages: full: ")
	switch {
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "a package is up to 16 MB")
	case errors.Is(err, packages.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, msg)
	case errors.Is(err, packages.ErrConflict), errors.Is(err, packages.ErrFull):
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
			Summary:    "Installed packages (overlay themes), with their version, publisher (as stated, not verified), settings, and the overlays using each.",
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
				"/v1/packages/check gave (anything else is refused). Installed, nothing changes until an overlay uses it. " +
				"Exactly what's installed again is harmless (200).",
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
				p, created, err := d.Packages.Install(r.Context(), data, dg, d.Now())
				if err != nil {
					d.Logger.Warn("package refused", "err", err)
					packageError(w, r, d, err)
					return
				}
				if err := refreshThemes(r.Context(), d); err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				code := http.StatusOK
				if created {
					code = http.StatusCreated
					d.Logger.Info("package installed", "package", p.ID, "version", p.Version, "publisher", p.Publisher, "digest", p.Digest)
				}
				writeJSON(w, code, p)
			},
		},
		{
			Method: "DELETE", Path: "/v1/packages/{id}",
			Summary: "Remove a package: every overlay using a theme goes back to the Classic look first. Its files and versions " +
				"are deleted; its audit stays. Answers the package and the overlays changed.",
			Permission: PermPackagesWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				id := r.PathValue("id")
				if _, err := d.Packages.Get(r.Context(), id); err != nil {
					packageError(w, r, d, err)
					return
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
				d.Logger.Info("package removed", "package", p.ID, "version", p.Version, "overlays_to_classic", names)
				writeJSON(w, http.StatusOK, map[string]any{"package": p, "overlays": names})
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
