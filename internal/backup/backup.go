// Package backup makes, checks and restores backups of the data folder
// (docs/adr/0012-backups-and-retention.md). A backup is one ZIP archive: a
// consistent copy of the database and a manifest of every file's size and
// SHA-256. Secrets never enter a backup: they stay in the keyring or the
// encrypted secrets file, which is never read here.
//
// Restoring is staged: Stage checks a backup and marks it, and ApplyPending
// swaps it in at the next start, before anything opens the database.
package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // for opening backups read-only

	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Format is the manifest's format.
var Format = brand.Format("backup")

// Kinds of backup.
const (
	Manual       = "manual"        // made by the streamer
	Scheduled    = "scheduled"     // made every day; the newest few are kept
	PreRestore   = "pre-restore"   // the state a restore replaced
	PreMigration = "pre-migration" // the database before a schema change (database only)
	PreUpdate    = "pre-update"    // made just before installing an update (D-120)
)

// dbName is the database's name inside an archive.
var dbName = brand.Slug + ".db"

// MaxDatabaseBytes caps a database read from a backup (8 GiB).
const MaxDatabaseBytes = 8 << 30

// File is one file in an archive.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes an archive.
type Manifest struct {
	Format        string    `json:"format"`
	Kind          string    `json:"kind"`
	CreatedAt     time.Time `json:"created_at"`
	AppVersion    string    `json:"app_version"`
	SchemaVersion int       `json:"schema_version"`
	Files         []File    `json:"files"`
}

// Info is one backup in the backups folder.
type Info struct {
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	CreatedAt     time.Time `json:"created_at"`
	Size          int64     `json:"size"`
	SchemaVersion int       `json:"schema_version,omitempty"`
	AppVersion    string    `json:"app_version,omitempty"`
	// Problem: why it can't be used, from a quick look (Verify checks fully).
	Problem string `json:"problem,omitempty"`
}

// Errors.
var (
	ErrNotFound = errors.New("backup: no such backup")
	ErrInvalid  = errors.New("backup: it can't be used")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

var (
	archiveName   = regexp.MustCompile(`^` + brand.Slug + `-(manual|scheduled|pre-restore|pre-update)-(\d{8}T\d{6}\.\d{3}Z)\.zip$`)
	migrationName = regexp.MustCompile(`^(\d{8}T\d{6}\.\d{3}Z)-before-migration-\d{4}\.db$`)
	stamp         = "20060102T150405.000Z"
)

// Dir is the backups folder.
func Dir(dataDir string) string { return datadir.BackupDir(dataDir) }

// path resolves a backup's name, refusing anything that isn't one.
func path(dataDir, name string) (string, error) {
	if !archiveName.MatchString(name) && !migrationName.MatchString(name) {
		return "", ErrNotFound
	}
	p := filepath.Join(Dir(dataDir), name)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// Create writes a backup of db, made with VACUUM INTO so it's consistent
// while the engine runs.
func Create(ctx context.Context, db *storage.DB, dataDir, kind, appVersion string, now time.Time) (Info, error) {
	if kind != Manual && kind != Scheduled && kind != PreRestore && kind != PreUpdate {
		return Info{}, fmt.Errorf("backup: unknown kind %q", kind)
	}
	dir := Dir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Info{}, err
	}
	schema, err := db.SchemaVersion(ctx)
	if err != nil {
		return Info{}, err
	}
	tmpDB := filepath.Join(dir, fmt.Sprintf(".tmp-%d.db", now.UnixNano()))
	defer func() { _ = os.Remove(tmpDB) }()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmpDB); err != nil {
		return Info{}, fmt.Errorf("backup: copy the database: %w", err)
	}
	sum, size, err := hashFile(tmpDB)
	if err != nil {
		return Info{}, err
	}
	m := Manifest{Format: Format, Kind: kind, CreatedAt: now.UTC(), AppVersion: appVersion, SchemaVersion: schema,
		Files: []File{{Path: dbName, Size: size, SHA256: sum}}}
	name := fmt.Sprintf("%s-%s-%s.zip", brand.Slug, kind, now.UTC().Format(stamp))
	final := filepath.Join(dir, name)
	tmpZip := final + ".tmp"
	if err := writeArchive(tmpZip, m, tmpDB); err != nil {
		_ = os.Remove(tmpZip)
		return Info{}, err
	}
	if err := os.Rename(tmpZip, final); err != nil {
		_ = os.Remove(tmpZip)
		return Info{}, err
	}
	st, _ := os.Stat(final)
	return Info{Name: name, Kind: kind, CreatedAt: m.CreatedAt, Size: st.Size(), SchemaVersion: schema, AppVersion: appVersion}, nil
}

func writeArchive(path string, m Manifest, dbFile string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	mf, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	w, err := zw.Create(dbName)
	if err != nil {
		return err
	}
	src, err := os.Open(dbFile) //nolint:gosec // our own temporary copy in the backups folder
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err := io.Copy(w, src); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Sync()
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p) //nolint:gosec // a file in the data folder
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// List returns the backups, newest first.
func List(dataDir string) ([]Info, error) {
	entries, err := os.ReadDir(Dir(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return []Info{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Info{}
	for _, e := range entries {
		name := e.Name()
		st, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		if m := migrationName.FindStringSubmatch(name); m != nil {
			at, _ := time.Parse(stamp, m[1])
			out = append(out, Info{Name: name, Kind: PreMigration, CreatedAt: at, Size: st.Size()})
			continue
		}
		m := archiveName.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		at, _ := time.Parse(stamp, m[2])
		info := Info{Name: name, Kind: m[1], CreatedAt: at, Size: st.Size()}
		if man, err := readManifest(filepath.Join(Dir(dataDir), name)); err != nil {
			info.Problem = err.Error()
		} else {
			info.SchemaVersion, info.AppVersion = man.SchemaVersion, man.AppVersion
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func readManifest(p string) (Manifest, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return Manifest{}, invalid("not a readable archive")
	}
	defer func() { _ = zr.Close() }()
	return manifestOf(&zr.Reader)
}

func manifestOf(zr *zip.Reader) (Manifest, error) {
	var m Manifest
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		if f.UncompressedSize64 > 1<<20 {
			return m, invalid("the manifest is too big")
		}
		rc, err := f.Open()
		if err != nil {
			return m, invalid("the manifest can't be read")
		}
		defer rc.Close()
		dec := json.NewDecoder(io.LimitReader(rc, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return m, invalid("the manifest is malformed")
		}
		if m.Format != Format {
			return m, invalid("it's a %q backup, not %s", m.Format, Format)
		}
		return m, nil
	}
	return m, invalid("it has no manifest")
}

// Delete removes a backup.
func Delete(dataDir, name string) error {
	p, err := path(dataDir, name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// Prune deletes scheduled backups beyond the newest keep. Other kinds are
// the streamer's to delete.
func Prune(dataDir string, keep int) (int, error) {
	list, err := List(dataDir)
	if err != nil {
		return 0, err
	}
	n, seen := 0, 0
	for _, b := range list {
		if b.Kind != Scheduled {
			continue
		}
		seen++
		if seen > keep {
			if err := os.Remove(filepath.Join(Dir(dataDir), b.Name)); err == nil {
				n++
			}
		}
	}
	return n, nil
}

// LastOf returns when the newest backup of kind was made, or zero.
func LastOf(dataDir, kind string) time.Time {
	list, _ := List(dataDir)
	for _, b := range list {
		if b.Kind == kind && b.Problem == "" {
			return b.CreatedAt
		}
	}
	return time.Time{}
}

// Verify checks a backup fully: its manifest, every file's size and hash,
// SQLite's integrity check, and that this build can use its schema. It
// returns the database extracted to a temporary file in the data folder,
// for Stage and ApplyPending; the caller removes it.
func Verify(ctx context.Context, dataDir, name string) (string, error) {
	p, err := path(dataDir, name)
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(dataDir, fmt.Sprintf(".restore-check-%d.db", time.Now().UnixNano()))
	if migrationName.MatchString(name) {
		if err := copyFile(p, tmp); err != nil {
			return "", err
		}
	} else if err := extract(p, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := checkDatabase(ctx, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// extract checks an archive against its manifest and writes its database to dst.
func extract(p, dst string) error {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return invalid("not a readable archive")
	}
	defer func() { _ = zr.Close() }()
	m, err := manifestOf(&zr.Reader)
	if err != nil {
		return err
	}
	if len(m.Files) != 1 || m.Files[0].Path != dbName {
		return invalid("it should hold exactly %s", dbName)
	}
	want := m.Files[0]
	var found *zip.File
	for _, f := range zr.File {
		switch f.Name {
		case "manifest.json":
		case dbName:
			if found != nil {
				return invalid("%s is in it twice", dbName)
			}
			found = f
		default:
			return invalid("it holds a file the manifest doesn't list: %s", f.Name)
		}
	}
	if found == nil {
		return invalid("%s is missing", dbName)
	}
	if want.Size < 0 || want.Size > MaxDatabaseBytes {
		return invalid("the database is too big")
	}
	rc, err := found.Open()
	if err != nil {
		return invalid("%s can't be read", dbName)
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a fresh file in the data folder
	if err != nil {
		return err
	}
	h := sha256.New()
	// Read at most one byte past the declared size: a longer file is refused
	// without being read to its end.
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(rc, want.Size+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return invalid("%s can't be read: %v", dbName, err)
	}
	if n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		return invalid("%s doesn't match the manifest: it was changed or damaged", dbName)
	}
	return nil
}

// checkDatabase opens a database read-only and checks it can be restored.
func checkDatabase(ctx context.Context, p string) error {
	db, err := sql.Open("sqlite", readOnlyURI(p))
	if err != nil {
		return err
	}
	defer db.Close()
	var ok string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		return invalid("the database is damaged (SQLite's integrity check: %s)", firstNonEmpty(ok, fmt.Sprint(err)))
	}
	migs, err := storage.Migrations()
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return invalid("it isn't a %s database", brand.Name)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return err
		}
		if v > len(migs) {
			return invalid("it's from a newer version of %s (schema %d; this one knows %d)", brand.Name, v, len(migs))
		}
		if migs[v-1].Checksum != sum {
			return invalid("its schema step %04d differs from this version's", v)
		}
	}
	return rows.Err()
}

// readOnlyURI is an SQLite URI that opens p without changing it: the
// macOS data folder has a space in its path, and Windows paths a drive.
func readOnlyURI(p string) string {
	s := filepath.ToSlash(p)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return "file://" + (&url.URL{Path: s}).EscapedPath() + "?mode=ro&immutable=1"
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // a backup in the data folder
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a fresh file in the data folder
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
