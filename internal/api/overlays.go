package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
)

func overlayError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, overlays.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, overlays.ErrExists), errors.Is(err, overlays.ErrFull), errors.Is(err, overlays.ErrNoLink):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, overlays.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such overlay")
	case errors.Is(err, overlays.ErrNotOpen):
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "the overlay isn't open in OBS: add its link as a browser source in a scene")
	case errors.Is(err, overlays.ErrBusy), errors.Is(err, overlays.ErrWaitedTooLong):
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, err.Error())
	case errors.Is(err, overlays.ErrNoAnswer):
		writeError(w, http.StatusGatewayTimeout, CodeUnknownOutcome, "the overlay was sent the alert, but didn't say whether it showed it")
	default:
		internalError(w, r, d.Logger, err)
	}
}

// OverlayView is an overlay as the API shows it: no link (see /link).
type OverlayView struct {
	overlays.Overlay
	Pages    int `json:"pages"`    // open in OBS (or a browser)
	Previews int `json:"previews"` // open in the app's previews
	// Song is how reading a Now playing overlay's files is going (D-091).
	Song *overlays.SongStatus `json:"song,omitempty"`
}

// OverlayList is the body of GET /v1/overlays.
type OverlayList struct {
	Overlays []OverlayView `json:"overlays"`
	// Server is where overlay pages are served; "" when it's off.
	Server string `json:"server"`
	// ServerNote says when it isn't the usual address, so links copied
	// before won't work.
	ServerNote string        `json:"server_note,omitempty"`
	Positions  []string      `json:"positions"`
	Animations []string      `json:"animations"`
	Fonts      []string      `json:"fonts"`
	Default    overlays.Look `json:"default_look"`
	// Themes are the looks a wheel overlay can have besides Classic
	// (docs/adr/0036-packages-plugins-and-themes.md), with their settings.
	Themes []overlays.Theme `json:"themes"`
}

// OverlayLink is an overlay's private link.
type OverlayLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func overlayRoutes(d Deps) []Route {
	server := func() (string, string) {
		if d.OverlayServer == nil {
			return "", ""
		}
		return d.OverlayServer()
	}
	view := func(o overlays.Overlay) OverlayView {
		pages, previews := d.Hub.Open(o.Name)
		v := OverlayView{Overlay: o, Pages: pages, Previews: previews}
		if o.Kind == overlays.KindSong && d.Songs != nil {
			st := d.Songs.Status(o.Name)
			v.Song = &st
		}
		return v
	}
	link := func(w http.ResponseWriter, name, token string) {
		base, _ := server()
		if base == "" {
			writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "the overlay server is off")
			return
		}
		writeJSON(w, http.StatusOK, OverlayLink{Name: name, URL: base + "/o/" + token + "/"})
	}
	return []Route{
		{
			Method: "GET", Path: "/v1/overlays",
			Summary:    "Your overlays, with how many pages have each open, and where overlays are served. Links aren't listed.",
			Permission: PermOverlaysRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Overlays.List(r.Context())
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				out := OverlayList{Overlays: []OverlayView{}, Positions: overlays.Positions, Animations: overlays.Animations, Fonts: overlays.Fonts, Default: overlays.DefaultLook(),
					Themes: d.Themes.All()}
				for _, o := range list {
					out.Overlays = append(out.Overlays, view(o))
				}
				out.Server, out.ServerNote = server()
				writeJSON(w, http.StatusOK, out)
			},
		},
		{
			Method: "POST", Path: "/v1/overlays",
			Summary:    "Add an overlay {name, kind: alert, giveaway, wheel or song (Now playing), look?}. Answers with it and its private link. A taken name is refused (409).",
			Permission: PermOverlaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				// A look given in part keeps the defaults for the rest.
				look := overlays.DefaultLook()
				in := struct {
					Name string         `json:"name"`
					Kind string         `json:"kind"`
					Look *overlays.Look `json:"look"`
				}{Look: &look}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.Kind == "" {
					in.Kind = overlays.KindAlert
				}
				if in.Look == nil {
					in.Look = &look
				}
				o, token, err := d.Overlays.Create(r.Context(), in.Name, in.Kind, *in.Look, d.Now())
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				d.Logger.Info("overlay added", "overlay", o.Name, "kind", o.Kind)
				base, _ := server()
				out := map[string]any{"overlay": view(o)}
				if base != "" {
					out["url"] = base + "/o/" + token + "/"
				}
				writeJSON(w, http.StatusCreated, out)
			},
		},
		{
			Method: "PUT", Path: "/v1/overlays/{name}",
			Summary:    "Change an overlay's look {look}. A wheel overlay's look may be a theme (look.theme, from GET /v1/overlays themes) with its settings (look.theme_settings). Open pages change at once. Repeating it is harmless.",
			Permission: PermOverlaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				cur, err := d.Overlays.Get(r.Context(), r.PathValue("name"))
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				// A look given in part keeps the rest as it is. A theme's
				// settings are replaced, not merged: another theme's would
				// be refused. Not given, they stay with the same theme.
				look := cur.Look
				look.ThemeSettings = nil
				in := struct {
					Look *overlays.Look `json:"look"`
				}{Look: &look}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.Look == nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "look is required")
					return
				}
				if in.Look.ThemeSettings == nil && in.Look.Theme == cur.Look.Theme {
					in.Look.ThemeSettings = cur.Look.ThemeSettings
				}
				// Sounds play on giveaway and wheel overlays, and only ones in
				// the library.
				for _, name := range []string{in.Look.StartSound, in.Look.EndSound, in.Look.CheerSound} {
					if name == "" {
						continue
					}
					if cur.Kind == overlays.KindAlert || cur.Kind == overlays.KindSong {
						writeError(w, http.StatusBadRequest, CodeInvalidRequest,
							"these sounds are for giveaway and wheel overlays; an alert overlay plays the sound its step chooses")
						return
					}
					if _, err := d.Sounds.Get(r.Context(), name); err != nil {
						writeError(w, http.StatusBadRequest, CodeInvalidRequest, "there's no sound "+strconv.Quote(name)+" in your library")
						return
					}
				}
				o, err := d.Overlays.SetLook(r.Context(), cur.Name, *in.Look, d.Now())
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				d.Hub.Changed(o)
				writeJSON(w, http.StatusOK, view(o))
			},
		},
		{
			Method: "DELETE", Path: "/v1/overlays/{name}",
			Summary:    "Delete an overlay. Its link stops working and open pages close. Repeating it is harmless.",
			Permission: PermOverlaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				name := r.PathValue("name")
				ok, err := d.Overlays.Delete(r.Context(), name)
				d.Hub.Disconnect(name)
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				if ok {
					d.Logger.Info("overlay deleted", "overlay", name)
				}
				writeJSON(w, http.StatusOK, map[string]any{"deleted": ok})
			},
		},
		{
			Method: "GET", Path: "/v1/overlays/{name}/link",
			Summary:    "An overlay's private link, to paste into OBS as a browser source. Keep it private: anyone with it can see the overlay.",
			Permission: PermOverlaysRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				name := r.PathValue("name")
				token, err := d.Overlays.Link(r.Context(), name)
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				link(w, name, token)
			},
		},
		{
			Method: "POST", Path: "/v1/overlays/{name}/link",
			Summary:    "Make a new link for an overlay. The old link stops working at once; paste the new one into OBS.",
			Permission: PermOverlaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				name := r.PathValue("name")
				token, err := d.Overlays.NewLink(r.Context(), name, d.Now())
				d.Hub.Disconnect(name) // the old link mustn't keep showing anything
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				d.Logger.Info("overlay link replaced", "overlay", name)
				link(w, name, token)
			},
		},
		{
			Method: "POST", Path: "/v1/overlays/{name}/test",
			Summary: "Show a sample alert (or, on a giveaway overlay, a sample giveaway and draw; on a wheel overlay, a sample spin; on a Now playing overlay, a sample song) on an overlay in OBS, to see where it goes. Fails when the overlay isn't open in OBS (503). " +
				"?preview=1 shows it in the app's previews only, never in OBS; with {look}, in that look, unsaved.",
			Permission: PermOverlaysWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				o, err := d.Overlays.Get(r.Context(), r.PathValue("name"))
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				if o.Kind == overlays.KindGiveaway || o.Kind == overlays.KindWheel || o.Kind == overlays.KindSong {
					// A made-up giveaway and draw, a spin or a song: previews only, or OBS too.
					previewsOnly := r.URL.Query().Get("preview") == "1"
					if o.Kind == overlays.KindSong && previewsOnly && r.ContentLength != 0 {
						// A song in an unsaved look, in the previews only.
						look := o.Look
						in := struct {
							Look *overlays.Look `json:"look"`
						}{Look: &look}
						if !decodeJSON(w, r, &in) {
							return
						}
						if in.Look != nil {
							if err := in.Look.Check(); err != nil {
								overlayError(w, r, d, err)
								return
							}
							o.Look = *in.Look
							d.Hub.PreviewLook(o)
						}
					}
					sample := d.Hub.SampleGiveaway
					switch o.Kind {
					case overlays.KindWheel:
						sample = d.Hub.SampleWheel
					case overlays.KindSong:
						sample = d.Hub.SampleSong
					}
					pages, previews := sample(o.Name, previewsOnly)
					if !previewsOnly && pages == 0 {
						overlayError(w, r, d, overlays.ErrNotOpen)
						return
					}
					writeJSON(w, http.StatusOK, overlays.Result{Overlay: o.Name, Shown: pages > 0, Pages: pages, Previews: previews,
						Test: previewsOnly})
					return
				}
				sample := overlays.Alert{Title: "Sample alert", Text: "This is where your alerts appear", Name: "Sample", Seconds: o.Look.Seconds}
				if r.URL.Query().Get("preview") == "1" {
					// A look in the body is tried in the previews only: OBS
					// keeps the saved one.
					if r.ContentLength != 0 {
						look := o.Look
						in := struct {
							Look *overlays.Look `json:"look"`
						}{Look: &look}
						if !decodeJSON(w, r, &in) {
							return
						}
						if in.Look != nil {
							if err := in.Look.Check(); err != nil {
								overlayError(w, r, d, err)
								return
							}
							o.Look = *in.Look
							sample.Seconds = o.Look.Seconds
							d.Hub.PreviewLook(o)
						}
					}
					writeJSON(w, http.StatusOK, d.Hub.Preview(o.Name, sample))
					return
				}
				ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
				defer cancel()
				res, err := d.Hub.Show(ctx, o.Name, sample, "")
				if err != nil {
					overlayError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, res)
			},
		},
	}
}
