// Package sounds keeps the sound library and asks the desktop app to play
// sounds (docs/adr/0016-sounds.md). Sounds are stored in the database, so
// backups carry them; each has its own volume. The engine never plays
// anything itself: the app's window does, and says whether it worked.
package sounds

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Limits.
const (
	MaxBytes      = 5 << 20   // one sound
	MaxTotalBytes = 100 << 20 // the whole library
	MaxSounds     = 100
	DefaultVolume = 80
)

// Errors. Every validation error wraps ErrInvalid.
var (
	ErrInvalid  = errors.New("sounds: invalid")
	ErrNotFound = errors.New("sounds: no such sound")
	ErrFull     = errors.New("sounds: the library is full")
	ErrExists   = errors.New("sounds: a sound with that name exists")
)

// NamePattern is what a sound's name looks like.
const NamePattern = `^[a-z0-9][a-z0-9_-]{0,63}$`

var namePattern = regexp.MustCompile(NamePattern)

// Formats and their media types.
var mediaTypes = map[string]string{"mp3": "audio/mpeg", "wav": "audio/wav", "ogg": "audio/ogg", "m4a": "audio/mp4"}

// MediaType is the Content-Type for a format.
func MediaType(format string) string { return mediaTypes[format] }

// Sound is one sound in the library, without its audio.
type Sound struct {
	Name      string    `json:"name"`
	Format    string    `json:"format"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	Volume    int       `json:"volume"` // 0 to 100
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// CheckName reports what's wrong with a sound's name.
func CheckName(name string) error {
	if !namePattern.MatchString(name) {
		return invalid("a sound's name is up to 64 lowercase letters, digits, - and _, like airhorn")
	}
	return nil
}

// NameFromFile suggests a name from a file name: "Air Horn!.mp3" → "air-horn".
func NameFromFile(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.TrimRight(b.String(), "-")
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "-")
	}
	if name == "" {
		name = "sound"
	}
	return name
}

// Sniff names an audio format from a file's first bytes, or "" when it's
// none StreamGremlin plays. The bytes decide, never the file's name.
func Sniff(b []byte) string {
	switch {
	case len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE")):
		return "wav"
	case len(b) >= 4 && bytes.Equal(b[0:4], []byte("OggS")):
		return "ogg"
	case len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")) && audioBrand(string(b[8:12])):
		return "m4a"
	case len(b) >= 3 && bytes.Equal(b[0:3], []byte("ID3")):
		return "mp3"
	case len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0 && b[1]&0x06 != 0: // an MPEG audio frame, layer I-III
		return "mp3"
	}
	return ""
}

func audioBrand(b string) bool {
	switch b {
	case "M4A ", "M4B ", "mp42", "isom", "dash", "iso2", "mp41":
		return true
	}
	return false
}

// Store keeps the library.
type Store struct{ db *storage.DB }

// NewStore returns a Store.
func NewStore(db *storage.DB) *Store { return &Store{db: db} }

const columns = "name, format, size, sha256, volume, created_at, updated_at"

type scanner interface{ Scan(...any) error }

func scan(s scanner) (Sound, error) {
	var x Sound
	var created, updated string
	if err := s.Scan(&x.Name, &x.Format, &x.Size, &x.SHA256, &x.Volume, &created, &updated); err != nil {
		return Sound{}, err
	}
	x.CreatedAt, _ = storage.ParseTimestamp(created)
	x.UpdatedAt, _ = storage.ParseTimestamp(updated)
	return x, nil
}

// Add saves a new sound. Adding the same audio under the same name again
// is harmless (created is false); different audio under a taken name is
// refused, so nothing is replaced by accident.
func (s *Store) Add(ctx context.Context, name string, data []byte, volume int, now time.Time) (Sound, bool, error) {
	if err := CheckName(name); err != nil {
		return Sound{}, false, err
	}
	if volume < 0 || volume > 100 {
		return Sound{}, false, invalid("the volume is 0 to 100")
	}
	if len(data) == 0 {
		return Sound{}, false, invalid("no audio arrived: the file is empty, or it wasn't sent")
	}
	if len(data) > MaxBytes {
		return Sound{}, false, invalid("a sound is up to %d MB", MaxBytes>>20)
	}
	format := Sniff(data)
	if format == "" {
		return Sound{}, false, invalid("that isn't an MP3, WAV, OGG or M4A sound")
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sound{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM sounds WHERE name = ?", name))
	switch {
	case err == nil && old.SHA256 == hash:
		return old, false, nil
	case err == nil:
		return Sound{}, false, fmt.Errorf("%w: %q; delete it first, or choose another name", ErrExists, name)
	case !errors.Is(err, sql.ErrNoRows):
		return Sound{}, false, err
	}
	var count int
	var total int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(size), 0) FROM sounds").Scan(&count, &total); err != nil {
		return Sound{}, false, err
	}
	if count >= MaxSounds {
		return Sound{}, false, fmt.Errorf("%w: up to %d sounds", ErrFull, MaxSounds)
	}
	if total+int64(len(data)) > MaxTotalBytes {
		return Sound{}, false, fmt.Errorf("%w: up to %d MB of sounds in all", ErrFull, MaxTotalBytes>>20)
	}
	ts := storage.Timestamp(now)
	if _, err := tx.ExecContext(ctx, `INSERT INTO sounds (name, format, size, sha256, volume, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, name, format, len(data), hash, volume, data, ts, ts); err != nil {
		return Sound{}, false, fmt.Errorf("sounds: add: %w", err)
	}
	x, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM sounds WHERE name = ?", name))
	if err != nil {
		return Sound{}, false, err
	}
	return x, true, tx.Commit()
}

// List returns every sound, by name, without audio.
func (s *Store) List(ctx context.Context) ([]Sound, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM sounds ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("sounds: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Sound{}
	for rows.Next() {
		x, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Get returns one sound, without audio.
func (s *Store) Get(ctx context.Context, name string) (Sound, error) {
	x, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM sounds WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return Sound{}, ErrNotFound
	}
	return x, err
}

// Audio returns a sound and its audio.
func (s *Store) Audio(ctx context.Context, name string) (Sound, []byte, error) {
	x, err := s.Get(ctx, name)
	if err != nil {
		return Sound{}, nil, err
	}
	var data []byte
	if err := s.db.QueryRowContext(ctx, "SELECT data FROM sounds WHERE name = ?", name).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Sound{}, nil, ErrNotFound
		}
		return Sound{}, nil, err
	}
	return x, data, nil
}

// SetVolume changes a sound's volume (0 to 100).
func (s *Store) SetVolume(ctx context.Context, name string, volume int, now time.Time) (Sound, error) {
	if volume < 0 || volume > 100 {
		return Sound{}, invalid("the volume is 0 to 100")
	}
	x, err := scan(s.db.QueryRowContext(ctx, `UPDATE sounds SET volume = ?,
		updated_at = CASE WHEN volume = ? THEN updated_at ELSE ? END WHERE name = ? RETURNING `+columns,
		volume, volume, storage.Timestamp(now), name))
	if errors.Is(err, sql.ErrNoRows) {
		return Sound{}, ErrNotFound
	}
	return x, err
}

// Delete removes a sound.
func (s *Store) Delete(ctx context.Context, name string) (bool, error) {
	r, err := s.db.ExecContext(ctx, "DELETE FROM sounds WHERE name = ?", name)
	if err != nil {
		return false, fmt.Errorf("sounds: delete: %w", err)
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}
