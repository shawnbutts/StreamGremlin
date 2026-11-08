package api

import (
	"errors"
	"net/http"
	"os"

	"code.streamgremlin.com/streamgremlin/internal/backup"
	"code.streamgremlin.com/streamgremlin/internal/buildinfo"
	"code.streamgremlin.com/streamgremlin/internal/maintenance"
)

// Backups is the body of GET /v1/backups.
type Backups struct {
	Backups     []backup.Info        `json:"backups"`
	Pending     string               `json:"pending,omitempty"` // restored at the next start
	LastRestore *backup.Result       `json:"last_restore,omitempty"`
	Settings    maintenance.Settings `json:"settings"`
	Status      maintenance.Status   `json:"status"`
}

func backupError(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	switch {
	case errors.Is(err, backup.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "no such backup")
	case errors.Is(err, backup.ErrInvalid), errors.Is(err, maintenance.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	default:
		internalError(w, r, d.Logger, err)
	}
}

func backupRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "GET", Path: "/v1/backups",
			Summary:    "Backups, newest first, with the restore waiting for the next start, how the last one went, and the backup and history settings.",
			Permission: PermBackupsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				list, err := backup.List(d.DataDir)
				if err != nil {
					backupError(w, r, d, err)
					return
				}
				st, err := d.Maintenance.Settings(r.Context())
				if err != nil {
					backupError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, Backups{Backups: list, Pending: backup.Pending(d.DataDir), LastRestore: backup.LastResult(d.DataDir),
					Settings: st, Status: d.Maintenance.Status(r.Context())})
			},
		},
		{
			Method: "POST", Path: "/v1/backups", Summary: "Back up now: the database, never secrets. Answers the new backup.",
			Permission: PermBackupsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				info, err := backup.Create(r.Context(), d.DB, d.DataDir, backup.Manual, buildinfo.Version, d.Now())
				if err != nil {
					backupError(w, r, d, err)
					return
				}
				d.Logger.Info("backup made", "backup", info.Name)
				writeJSON(w, http.StatusCreated, info)
			},
		},
		{
			Method: "POST", Path: "/v1/backups/{name}/verify",
			Summary:    "Check a backup fully: every file against the manifest, SQLite's integrity check, and that this version can use it. 400 says what's wrong.",
			Permission: PermBackupsRead,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				tmp, err := backup.Verify(r.Context(), d.DataDir, r.PathValue("name"))
				if err != nil {
					backupError(w, r, d, err)
					return
				}
				removeQuietly(tmp)
				writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			},
		},
		{
			Method: "POST", Path: "/v1/backups/{name}/restore",
			Summary:    "Restore a backup at the next start: it's checked now, and nothing changes until StreamGremlin restarts. What it replaces is backed up first.",
			Permission: PermBackupsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if err := backup.Stage(r.Context(), d.DataDir, r.PathValue("name")); err != nil {
					backupError(w, r, d, err)
					return
				}
				d.Logger.Info("restore staged for the next start", "backup", r.PathValue("name"))
				writeJSON(w, http.StatusAccepted, map[string]any{"pending": r.PathValue("name"), "restart_required": true})
			},
		},
		{
			Method: "DELETE", Path: "/v1/backups/{name}/restore", Summary: "Don't restore that backup at the next start after all.",
			Permission: PermBackupsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if backup.Pending(d.DataDir) != r.PathValue("name") {
					writeError(w, http.StatusNotFound, CodeNotFound, "that backup isn't waiting to be restored")
					return
				}
				if err := backup.Unstage(d.DataDir); err != nil {
					backupError(w, r, d, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "DELETE", Path: "/v1/backups/{name}", Summary: "Delete a backup.",
			Permission: PermBackupsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if backup.Pending(d.DataDir) == r.PathValue("name") {
					writeError(w, http.StatusConflict, CodeConflict, "that backup is waiting to be restored; cancel the restore first")
					return
				}
				if err := backup.Delete(d.DataDir, r.PathValue("name")); err != nil {
					backupError(w, r, d, err)
					return
				}
				d.Logger.Info("backup deleted", "backup", r.PathValue("name"))
				w.WriteHeader(http.StatusNoContent)
			},
		},
		{
			Method: "PUT", Path: "/v1/settings/maintenance",
			Summary:    "How often to back up, how many automatic backups to keep, and how many days of events and runs to keep (0: always).",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var in maintenance.Settings
				if !decodeJSON(w, r, &in) {
					return
				}
				st, err := d.Maintenance.SetSettings(r.Context(), in)
				if err != nil {
					backupError(w, r, d, err)
					return
				}
				writeJSON(w, http.StatusOK, st)
			},
		},
		{
			Method: "POST", Path: "/v1/maintenance/sweep", Summary: "Clear old events and finished runs now, by the history settings, and say how many.",
			Permission: PermSettingsWrite,
			Handler: func(w http.ResponseWriter, r *http.Request) {
				res, err := d.Maintenance.Sweep(r.Context())
				if err != nil {
					internalError(w, r, d.Logger, err)
					return
				}
				writeJSON(w, http.StatusOK, res)
			},
		},
	}
}

func removeQuietly(p string) { _ = os.Remove(p) }
