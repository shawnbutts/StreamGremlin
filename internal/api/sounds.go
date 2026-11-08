package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/sounds"
)

func soundError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "a sound is up to 5 MB")
	case errors.Is(err, sounds.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	case errors.Is(err, sounds.ErrExists), errors.Is(err, sounds.ErrFull):
		writeError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, sounds.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such sound")
	case errors.Is(err, sounds.ErrNoPlayer):
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "no StreamGremlin window is open to play sounds; open the app")
	case errors.Is(err, sounds.ErrNoAnswer):
		writeError(w, http.StatusGatewayTimeout, CodeUnknownOutcome, "the app was asked to play it, but didn't say whether it did")
	default:
		internalError(w, r, d.Logger, err)
	}
}

// SoundList is the body of GET /v1/sounds.
type SoundList struct {
	Sounds []sounds.Sound `json:"sounds"`
	// Player says whether a StreamGremlin window is open to play sounds.
	Player bool `json:"player"`
}

func soundRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/sounds",
			Summary:    "The sound library, by name (without audio), and whether a window is open to play sounds.",
			Permission: PermSoundsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := d.Sounds.List(r.Context())
				if err != nil {
					soundError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, SoundList{Sounds: list, Player: d.Player.Connected()})
			},
		},
		{
			Method: "POST", Path: "/v1/sounds/{name}",
			Summary: "Add a sound: the body is the audio itself (MP3, WAV, OGG or M4A, up to 5 MB); ?volume= 0-100 (default 80). " +
				"The same audio again is harmless (200); other audio under a taken name is refused (409).",
			Permission: PermSoundsWrite, MaxBody: sounds.MaxBytes + 1,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				volume := sounds.DefaultVolume
				if v := r.URL.Query().Get("volume"); v != "" {
					n, err := strconv.Atoi(v)
					if err != nil {
						writeError(w, http.StatusBadRequest, CodeInvalidRequest, "the volume is 0 to 100")
						return
					}
					volume = n
				}
				data, err := io.ReadAll(r.Body)
				if err != nil {
					soundError(w, r, d, err)
					return
				}
				s, created, err := d.Sounds.Add(r.Context(), r.PathValue("name"), data, volume, d.Now())
				if err != nil {
					soundError(w, r, d, err)
					return
				}
				code := http.StatusOK
				if created {
					code = http.StatusCreated
					d.Logger.Info("sound added", "sound", s.Name, "format", s.Format, "bytes", s.Size)
				}
				writeJSON(w, code, s)
			},
		},
		{
			Method: "PUT", Path: "/v1/sounds/{name}",
			Summary:    "Change a sound's volume: {volume} 0-100. Repeating it is harmless.",
			Permission: PermSoundsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Volume *int `json:"volume"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				if in.Volume == nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "volume is required")
					return
				}
				s, err := d.Sounds.SetVolume(r.Context(), r.PathValue("name"), *in.Volume, d.Now())
				if err != nil {
					soundError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, s)
			},
		},
		{
			Method: "DELETE", Path: "/v1/sounds/{name}",
			Summary:    "Delete a sound. Workflows that play it fail until it's added again.",
			Permission: PermSoundsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				ok, err := d.Sounds.Delete(r.Context(), r.PathValue("name"))
				if err != nil {
					soundError(w, r, d, err)
					return
				}
				if !ok {
					writeError(w, http.StatusNotFound, CodeNotFound, "no such sound")
					return
				}
				d.Logger.Info("sound deleted", "sound", r.PathValue("name"))
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "GET", Path: "/v1/sounds/{name}/audio",
			Summary:    "A sound's audio, with its media type. Supports ranges, for the player.",
			Permission: PermSoundsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				s, data, err := d.Sounds.Audio(r.Context(), r.PathValue("name"))
				if err != nil {
					soundError(w, r, d, err)
					return
				}
				w.Header().Set("Content-Type", sounds.MediaType(s.Format))
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeContent(w, r, "", s.UpdatedAt, bytes.NewReader(data))
			},
		},
		{
			Method: "POST", Path: "/v1/sounds/{name}/play",
			Summary: "Play a sound now in the app's window: {volume} 0-100 (default its own). Answers once the window says it " +
				"started: 503 when no window is open, 504 when it didn't answer. Each request plays again.",
			Permission: PermSoundsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Volume *int `json:"volume"`
				}
				if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
					return
				}
				s, err := d.Sounds.Get(r.Context(), r.PathValue("name"))
				if err != nil {
					soundError(w, r, d, err)
					return
				}
				volume := s.Volume
				if in.Volume != nil {
					if *in.Volume < 0 || *in.Volume > 100 {
						writeError(w, http.StatusBadRequest, CodeInvalidRequest, "the volume is 0 to 100")
						return
					}
					volume = *in.Volume
				}
				res, err := d.Player.Play(r.Context(), s, volume, "")
				if err != nil {
					soundError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, res)
			},
		},
		{
			Method: "POST", Path: "/v1/player/stop",
			Summary:    "Stop every sound playing in the app's window.",
			Permission: PermSoundsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if err := d.Player.Stop(); err != nil {
					soundError(w, r, d, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "GET", Path: "/v1/player/next",
			Summary: "For the app's window: what to play after ?after= (a position; -1 for the current one), waiting up to " +
				"?wait= seconds (0-25). Answers {commands, head}.",
			Permission: PermSoundsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "after is a number")
					return
				}
				// Under the server's 30-second write timeout.
				wait, err := intParam(r.URL.Query().Get("wait"), 0, 0, 25)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "wait is 0 to 25 seconds")
					return
				}
				cmds, head := d.Player.Next(r.Context(), after, time.Duration(wait)*time.Second)
				writeJSON(w, http.StatusOK, map[string]any{"commands": cmds, "head": head})
			},
		},
		{
			Method: "POST", Path: "/v1/player/reports",
			Summary:    "For the app's window: whether a play started: {id, ok, error}.",
			Permission: PermSoundsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in sounds.Report
				if !decodeJSON(w, r, &in) {
					return
				}
				if !d.Player.Report(in) {
					writeError(w, http.StatusNotFound, CodeNotFound, "nothing is waiting for that play")
					return
				}
				w.WriteHeader(http.StatusNoContent)
			},
		},
	}
}
