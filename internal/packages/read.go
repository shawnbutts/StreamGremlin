// Package packages installs, lists and removes packages: overlay themes
// now, Lua plugins later (docs/adr/0036-packages-plugins-and-themes.md).
// A package is a ZIP with a manifest, streamgremlin.json. It's read with
// limits, checked, and stored in the database: nothing in it runs,
// compiles or is fetched while installing, and there's no folder on disk
// for anything else to change.
package packages

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
)

// Limits on a package (ADR-0036).
const (
	MaxZipBytes      = 16 << 20 // the ZIP itself
	MaxUnpackedBytes = 32 << 20 // all its files, unpacked
	MaxFileBytes     = 8 << 20  // one file, unpacked
	MaxFiles         = 200
	MaxRatio         = 100 // a file's unpacked size, times its packed size
	MaxPathLength    = 200
	MaxManifestBytes = 64 << 10
	MaxSettings      = 20
	MaxPackages      = 50
)

// ManifestFile is the manifest's name, at the package's root.
const ManifestFile = "streamgremlin.json"

// Format is the manifest's format, which every manifest names.
const Format = "streamgremlin.package/v1"

// API is the package API this build has.
const API = "1"

// Kinds of package.
const (
	KindTheme = "theme"
	KindLua   = "lua"
)

// Errors. Every problem with a package wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("packages: invalid")
	ErrNotFound = errors.New("packages: not installed")
	ErrConflict = errors.New("packages: conflict")
	ErrFull     = errors.New("packages: full")
	// ErrConfirm: the package would replace another publisher's, and the
	// streamer hasn't confirmed the new publisher by name.
	ErrConfirm = errors.New("packages: confirm")
	// ErrConfirmPermissions: a plugin's permissions weren't all approved.
	ErrConfirmPermissions = errors.New("packages: confirm permissions")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Manifest is streamgremlin.json. Unknown fields are refused.
type Manifest struct {
	Format      string `json:"format"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Publisher   string `json:"publisher"`
	Description string `json:"description,omitempty"`
	License     string `json:"license,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	Kind        string `json:"kind"`
	API         string `json:"api"`
	Theme       *Theme `json:"theme,omitempty"`
	Lua         *Lua   `json:"lua,omitempty"`
}

// Theme is a theme's part of the manifest.
type Theme struct {
	For      []string           `json:"for"`
	Entry    string             `json:"entry"`
	Settings []overlays.Setting `json:"settings,omitempty"`
}

// Read is a package as read from its ZIP: checked, nothing stored.
type Read struct {
	Manifest Manifest
	Files    map[string][]byte // by path
	Hashes   map[string]string // each file's SHA-256, by path
	Digest   string            // the version's: its sorted paths and file digests
	Size     int64             // its files' total size
}

// Paths lists its files, sorted.
func (r *Read) Paths() []string {
	out := make([]string, 0, len(r.Files))
	for p := range r.Files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

var (
	idPattern      = regexp.MustCompile(overlays.PackageIDPattern)
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})(-[0-9A-Za-z.-]{1,40})?$`)
	pathPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	keyPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// reserved are StreamGremlin's own namespaces: a package's id names its
// actions and events, so it can't take one of these.
var reserved = []string{"builtin", "streamgremlin", "chat", "obs", "twitch", "youtube", "points", "wheels", "wheel", "giveaways",
	"giveaway", "workflow", "workflows", "overlays", "overlay", "sounds", "sound", "quotes", "timers", "variables", "polls",
	"predictions", "ads", "rewards", "viewers", "events", "plugin", "plugins", "lua", "theme", "themes", "package", "packages",
	"system", "app", "engine", "settings", "secrets", "backups", "test",
	// The built-in looks' names, which the CLI takes as they are.
	"classic", "workshop", "neon-arcade", "selector-board", "audit", "check"}

// themeTypes are the files a theme may have.
var themeTypes = []string{".html", ".js", ".css", ".json", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".gif", ".woff2", ".woff",
	".ttf", ".otf", ".mp3", ".ogg", ".wav", ".txt", ".md"}

// secretWords make a setting's key look like it holds a secret; settings
// never do.
var secretWords = []string{"token", "password", "passwd", "secret", "key", "apikey", "auth", "credential", "credentials"}

// ReadZip reads and checks a package. The first problem found is the
// error, and nothing is kept.
func ReadZip(data []byte) (*Read, error) {
	if len(data) > MaxZipBytes {
		return nil, invalid("a package is up to %d MB", MaxZipBytes>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalid("this isn't a ZIP file")
	}
	type entry struct {
		f    *zip.File
		name string
	}
	var entries []entry
	for _, f := range zr.File {
		name := f.Name
		if skipped(name) {
			continue
		}
		if f.FileInfo().IsDir() && strings.HasSuffix(name, "/") {
			continue // folders are implied
		}
		entries = append(entries, entry{f, name})
		if len(entries) > MaxFiles {
			return nil, invalid("a package has up to %d files", MaxFiles)
		}
	}
	// A ZIP made by compressing a folder (Finder, Explorer, a download)
	// has everything in that folder: it's the package.
	if prefix := wrapper(entries, func(e entry) string { return e.name }); prefix != "" {
		for i := range entries {
			entries[i].name = strings.TrimPrefix(entries[i].name, prefix)
		}
	}
	r := &Read{Files: map[string][]byte{}, Hashes: map[string]string{}}
	folded := map[string]string{}
	for _, e := range entries {
		f, name := e.f, e.name
		if err := checkPath(name); err != nil {
			return nil, err
		}
		if !f.Mode().IsRegular() {
			return nil, invalid("%s isn't a plain file (links and devices aren't allowed)", name)
		}
		if f.Flags&0x1 != 0 {
			return nil, invalid("%s is encrypted", name)
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, invalid("%s is packed in a way StreamGremlin doesn't read (only stored or deflated)", name)
		}
		if other, ok := folded[strings.ToLower(name)]; ok {
			return nil, invalid("%s and %s differ only in capitals, which macOS and Windows treat as one", other, name)
		}
		folded[strings.ToLower(name)] = name
		if f.UncompressedSize64 > MaxFileBytes {
			return nil, invalid("%s is over %d MB unpacked", name, MaxFileBytes>>20)
		}
		if f.UncompressedSize64 > MaxRatio*max(f.CompressedSize64, 1) {
			return nil, invalid("%s unpacks to over %d times its size", name, MaxRatio)
		}
		r.Size += int64(f.UncompressedSize64) //nolint:gosec // at most 8 MiB, checked above
		if r.Size > MaxUnpackedBytes {
			return nil, invalid("a package's files are up to %d MB unpacked", MaxUnpackedBytes>>20)
		}
		b, err := readFile(f)
		if err != nil {
			return nil, invalid("%s: %v", name, err)
		}
		r.Files[name] = b
		sum := sha256.Sum256(b)
		r.Hashes[name] = hex.EncodeToString(sum[:])
	}
	raw, ok := r.Files[ManifestFile]
	if !ok {
		return nil, invalid("there's no %s at the package's top", ManifestFile)
	}
	if len(raw) > MaxManifestBytes {
		return nil, invalid("%s is up to %d KB", ManifestFile, MaxManifestBytes>>10)
	}
	m, err := parseManifest(raw)
	if err != nil {
		return nil, err
	}
	r.Manifest = m
	if m.Kind == KindTheme {
		for p := range r.Files {
			if !slices.Contains(themeTypes, strings.ToLower(path.Ext(p))) {
				return nil, invalid("%s: a theme has only pages, scripts, styles, pictures, fonts, sounds and text (%s)", p,
					strings.Join(themeTypes, " "))
			}
		}
		if _, ok := r.Files[m.Theme.Entry]; !ok {
			return nil, invalid("the theme's page %s isn't in the package", m.Theme.Entry)
		}
	}
	if m.Kind == KindLua {
		for p := range r.Files {
			if !slices.Contains(luaTypes, strings.ToLower(path.Ext(p))) {
				return nil, invalid("%s: a Lua plugin has only its script, JSON and text (%s)", p, strings.Join(luaTypes, " "))
			}
		}
		script, ok := r.Files[m.Lua.Script]
		switch {
		case !ok:
			return nil, invalid("the plugin's script %s isn't in the package", m.Lua.Script)
		case len(script) > MaxScriptBytes:
			return nil, invalid("the plugin's script is over %d KB", MaxScriptBytes>>10)
		case !utf8.Valid(script):
			return nil, invalid("the plugin's script isn't UTF-8 text")
		}
	}
	r.Digest = digest(r)
	return r, nil
}

// skipped are files Finder adds to ZIPs.
func skipped(name string) bool {
	return strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store"
}

// wrapper is the one folder every file is in, when there's no manifest at
// the top but there is one in that folder ("" otherwise).
func wrapper[E any](entries []E, name func(E) string) string {
	prefix := ""
	hasTop, hasInner := false, false
	for _, e := range entries {
		n := name(e)
		if n == ManifestFile {
			hasTop = true
		}
		i := strings.Index(n, "/")
		if i <= 0 {
			return ""
		}
		if prefix == "" {
			prefix = n[:i+1]
		}
		if n[:i+1] != prefix {
			return ""
		}
		if n == prefix+ManifestFile {
			hasInner = true
		}
	}
	if hasTop || !hasInner {
		return ""
	}
	return prefix
}

func checkPath(name string) error {
	if len(name) > MaxPathLength || !pathPattern.MatchString(name) {
		return invalid("%q: a file's path is up to %d letters, digits, . _ - and /, with no folder above the package", name, MaxPathLength)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "." || seg == ".." {
			return invalid("%q: a file's path can't have . or .. as a folder", name)
		}
	}
	return nil
}

// readFile reads a file, never more than it says it holds.
func readFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, int64(f.UncompressedSize64)+1)) //nolint:gosec // at most 8 MiB, checked
	if err != nil {
		return nil, err
	}
	if uint64(len(b)) != f.UncompressedSize64 {
		return nil, errors.New("its size isn't what the ZIP says")
	}
	return b, nil
}

// digest is the SHA-256 of the sorted paths and their files' digests.
func digest(r *Read) string {
	h := sha256.New()
	for _, p := range r.Paths() {
		_, _ = fmt.Fprintf(h, "%s\x00%s\n", p, r.Hashes[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func plain(s string, most int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= most && strings.IndexFunc(s, unicode.IsControl) < 0
}

func parseManifest(raw []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, invalid("%s: %v", ManifestFile, err)
	}
	if dec.More() {
		return m, invalid("%s has more after its object", ManifestFile)
	}
	switch {
	case m.Format != Format:
		return m, invalid("%s's format is %q", ManifestFile, Format)
	case !idPattern.MatchString(m.ID):
		return m, invalid("the id is 3 to 40 lowercase letters, digits and -, starting with a letter, like cosy-wheel")
	case slices.Contains(reserved, m.ID) || strings.HasPrefix(m.ID, "builtin"):
		return m, invalid("the id %q is one of StreamGremlin's own", m.ID)
	case !versionPattern.MatchString(m.Version):
		return m, invalid("the version is like 1.2.0 (or 1.2.0-beta.1)")
	case strings.TrimSpace(m.Name) == "" || !plain(m.Name, 60):
		return m, invalid("the name is one line, up to 60 characters")
	case strings.TrimSpace(m.Publisher) == "" || !plain(m.Publisher, 60):
		return m, invalid("the publisher is one line, up to 60 characters")
	case !plain(m.Description, 300):
		return m, invalid("the description is up to 300 characters")
	case !plain(m.License, 60):
		return m, invalid("the license is up to 60 characters")
	case m.Homepage != "" && (!plain(m.Homepage, 200) || !strings.HasPrefix(m.Homepage, "https://")):
		return m, invalid("the homepage is an https:// link up to 200 characters")
	case m.Kind != KindTheme && m.Kind != KindLua:
		return m, invalid("the kind is theme or lua")
	case m.API != API:
		return m, invalid("this package is for StreamGremlin's package API %q; this version has API %q", m.API, API)
	}
	if m.Kind == KindLua {
		return m, checkLua(m)
	}
	if m.Theme == nil || m.Lua != nil {
		return m, invalid("a theme's manifest has a theme section, and no lua section")
	}
	t := m.Theme
	if !slices.Equal(t.For, []string{"wheel"}) {
		return m, invalid(`a theme is for ["wheel"]: wheel overlays are the ones with looks`)
	}
	if checkPath(t.Entry) != nil || strings.ToLower(path.Ext(t.Entry)) != ".html" {
		return m, invalid("the theme's entry is its page: an .html file in the package")
	}
	for _, st := range t.Settings {
		if st.Key == "sounds" {
			return m, invalid(`setting "sounds" is StreamGremlin's: it's added for a theme with start.wav, end.wav or cheer.wav beside its page`)
		}
	}
	if err := checkSettings(t.Settings); err != nil {
		return m, err
	}
	return m, nil
}
