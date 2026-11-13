package overlays

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Reading a Now playing overlay's files (D-091). The music player
// rewrites them as the song changes. They're read only while the overlay
// is open, and only once a file has stopped changing, so a file caught
// half-written is never shown.

// Limits on the files.
const (
	// SongEvery is how often open overlays' files are looked at. A change
	// shows one or two looks later.
	SongEvery   = 500 * time.Millisecond
	maxSongFile = 4 << 10
	maxArtFile  = 5 << 20
	maxSongLine = 200
)

// stamp is what a file looked like: whether it's there, its size and
// when it last changed.
type stamp struct {
	ok   bool
	size int64
	mod  time.Time
}

func stampOf(path string) stamp {
	if path == "" {
		return stamp{}
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return stamp{}
	}
	return stamp{ok: true, size: st.Size(), mod: st.ModTime()}
}

// songState is one open overlay's files, as last read.
type songState struct {
	look               Look
	text, art          stamp // as last read
	textNext, artNext  stamp // as last seen: read once it's seen the same twice
	textRead, artRead  bool
	title, artist, pic string
	playing            bool
	textWhy, artWhy    string
}

// SongStatus is how reading an overlay's files is going, for the app.
type SongStatus struct {
	// Open: the overlay is open, so its files are being read.
	Open bool     `json:"open"`
	Song SongView `json:"song"`
	// Problem says why the song or its picture can't be shown.
	Problem string `json:"problem,omitempty"`
}

// SongReader reads open Now playing overlays' files and sends each change.
type SongReader struct {
	store *Store
	hub   *Hub
	log   *slog.Logger
	every time.Duration

	mu     sync.Mutex
	states map[string]*songState
	stop   context.CancelFunc
	done   chan struct{}
}

// NewSongReader returns a SongReader; Start starts it.
func NewSongReader(store *Store, hub *Hub, log *slog.Logger) *SongReader {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &SongReader{store: store, hub: hub, log: log, every: SongEvery, states: map[string]*songState{}}
}

// Start reads open overlays' files until Close.
func (r *SongReader) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.stop, r.done = cancel, make(chan struct{})
	go func() {
		defer close(r.done)
		t := time.NewTicker(r.every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.Tick(ctx)
			}
		}
	}()
}

// Close stops reading.
func (r *SongReader) Close() {
	if r.stop != nil {
		r.stop()
		<-r.done
	}
}

// Status says how reading an overlay's files is going.
func (r *SongReader) Status(name string) SongStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.states[name]
	if st == nil {
		return SongStatus{}
	}
	return SongStatus{Open: true, Song: st.view(), Problem: st.problem()}
}

// Tick looks at every open overlay's files once.
func (r *SongReader) Tick(ctx context.Context) {
	open := r.hub.SongOverlays()
	r.mu.Lock()
	defer r.mu.Unlock()
	keep := map[string]bool{}
	for _, name := range open {
		keep[name] = true
		o, err := r.store.Get(ctx, name)
		if err != nil {
			continue
		}
		st := r.states[name]
		if st == nil || st.look.SongFile != o.Look.SongFile || st.look.ArtFile != o.Look.ArtFile || st.look.PausedText != o.Look.PausedText {
			st = &songState{look: o.Look} // new files: read them afresh
			r.states[name] = st
		}
		before := st.view()
		r.readText(name, st)
		r.readArt(name, st)
		if v := st.view(); v != before || !st.textRead {
			r.hub.SetSong(name, v)
		}
	}
	for name := range r.states {
		if !keep[name] {
			delete(r.states, name) // closed: read afresh when it opens again
		}
	}
}

func (st *songState) view() SongView {
	if !st.playing {
		return SongView{}
	}
	return SongView{Playing: true, Title: st.title, Artist: st.artist, Picture: st.pic}
}

func (st *songState) problem() string {
	switch {
	case st.textWhy != "" && st.artWhy != "":
		return st.textWhy + "; " + st.artWhy
	case st.textWhy != "":
		return st.textWhy
	}
	return st.artWhy
}

// steady reports whether a file looks as it did a moment ago, and has
// changed since it was read: then it's read.
func steady(now stamp, next *stamp, last stamp, read bool) bool {
	if now != *next {
		*next = now
		return false
	}
	return now != last || !read
}

func (r *SongReader) readText(name string, st *songState) {
	path := st.look.SongFile
	if path == "" {
		st.playing, st.textRead, st.textWhy = false, true, "no song file is set"
		return
	}
	now := stampOf(path)
	if !steady(now, &st.textNext, st.text, st.textRead) {
		return
	}
	st.text, st.textRead = now, true
	title, artist, why := readSong(path, st.look.PausedText)
	if why != st.textWhy && why != "" && why != "paused" && why != "empty" {
		r.log.Warn("a Now playing overlay's song file can't be read", "overlay", name, "why", why)
	}
	st.textWhy = ""
	if why != "paused" && why != "empty" {
		st.textWhy = why
	}
	st.playing, st.title, st.artist = title != "", title, artist
}

func (r *SongReader) readArt(name string, st *songState) {
	path := st.look.ArtFile
	if path == "" {
		st.pic, st.artRead, st.artWhy = "", true, ""
		return
	}
	now := stampOf(path)
	if !steady(now, &st.artNext, st.art, st.artRead) {
		return
	}
	st.art, st.artRead = now, true
	if _, err := readArt(path); err != nil {
		if err.Error() != st.artWhy {
			r.log.Warn("a Now playing overlay's picture can't be read", "overlay", name, "why", err.Error())
		}
		st.pic, st.artWhy = "", err.Error()
		return
	}
	// The page asks for it again when it changes.
	st.pic, st.artWhy = fmt.Sprintf("art?v=%x-%x", now.mod.UnixNano(), now.size), ""
}

// readSong reads a song file: its first line is the title, its second
// (if any) the artist. why is "empty" or "paused" when nothing plays, or
// says what's wrong.
func readSong(path, paused string) (title, artist, why string) {
	f, err := os.Open(path) //nolint:gosec // the streamer's own file, named in the overlay's settings
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", "the song's file isn't there: is your music player writing it?"
		}
		return "", "", "the song's file can't be read"
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSongFile+1))
	switch {
	case err != nil:
		return "", "", "the song's file can't be read"
	case len(b) > maxSongFile:
		return "", "", "the song's file is too big (over 4 KB) to be a song's"
	}
	var lines []string
	for _, l := range strings.Split(strings.ToValidUTF8(string(b), ""), "\n") {
		if l = cleanLine(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "", "", "empty"
	}
	if p := strings.TrimSpace(paused); p != "" && strings.EqualFold(strings.Join(lines, " "), cleanLine(p)) {
		return "", "", "paused"
	}
	title = lines[0]
	if len(lines) > 1 {
		artist = lines[1]
	}
	return title, artist, ""
}

// cleanLine makes a line safe to show: no control or direction-changing
// characters, spaces squeezed, and not too long.
func cleanLine(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Bidi_Control, r), r == '\u200b', r == '\ufeff':
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > maxSongLine {
		out = string([]rune(out)[:maxSongLine-1]) + "…"
	}
	return out
}

// readArt reads a picture file, and says what kind it is: only a JPEG or
// PNG under 5 MB is ever served.
func readArt(path string) (art []byte, err error) {
	f, err := os.Open(path) //nolint:gosec // the streamer's own file, named in the overlay's settings
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("the picture's file isn't there")
		}
		return nil, errors.New("the picture's file can't be read")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxArtFile+1))
	switch {
	case err != nil:
		return nil, errors.New("the picture's file can't be read")
	case len(b) > maxArtFile:
		return nil, errors.New("the picture's file is over 5 MB")
	}
	if t := http.DetectContentType(b); t != "image/jpeg" && t != "image/png" {
		return nil, errors.New("the picture's file isn't a JPEG or PNG picture")
	}
	return b, nil
}

// serveArt answers a Now playing overlay's page asking for its picture.
func serveArt(w http.ResponseWriter, o Overlay) {
	if o.Kind != KindSong || o.Look.ArtFile == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	b, err := readArt(o.Look.ArtFile)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(b))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}
