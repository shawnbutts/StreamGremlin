// Package pretzel reads what Pretzel, the music app for streamers, is
// playing (D-090), from the now-playing address Pretzel documents for
// chat bots' !song commands. It only reads: it never controls Pretzel,
// and it never replaces Pretzel's own chat attribution.
//
// It asks Pretzel only while a Now playing overlay is open, when a
// workflow step asks, or when the streamer presses Check.
package pretzel

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// DefaultBase is Pretzel's API.
const DefaultBase = "https://api.pretzel.rocks"

// States of a Song.
const (
	// Playing: Pretzel said "Now Playing: …".
	Playing = "playing"
	// Other: Pretzel answered with something else, shown as it said it
	// in chat, never on the overlay.
	Other = "other"
	// NotUsing: Pretzel doesn't know this Twitch account.
	NotUsing = "not_using"
	// NotSignedIn: StreamGremlin isn't signed in to Twitch, so it doesn't
	// know the account to ask about.
	NotSignedIn = "not_signed_in"
	// Unknown: Pretzel didn't answer, or answered with an error.
	Unknown = "unknown"
)

// Timing.
const (
	// Every is how often an open Now playing overlay is kept up to date.
	// Pretzel publishes no limit; this is gentle.
	Every = 15 * time.Second
	// fresh: a check this recent is reused rather than asked again.
	fresh = 5 * time.Second
	// failuresToHide: a song stays up through this many failed checks
	// in a row less one, so a moment's trouble doesn't blink it away.
	failuresToHide = 2
	// maxBackoff caps how long a busy Pretzel is left alone.
	maxBackoff = 5 * time.Minute
	maxBody    = 4096
	maxField   = 200
)

// Song is what Pretzel said, and when.
type Song struct {
	State  string `json:"state"`
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"` // "" when Pretzel names none
	// Text is the song for showing: "Title by Artist", or the title.
	// For Other, what Pretzel said.
	Text string `json:"text,omitempty"`
	// Link is Pretzel's link to the song (https://prtzl.io/…), if given.
	Link string `json:"link,omitempty"`
	// Why says what went wrong, for NotUsing, NotSignedIn and Unknown.
	Why       string    `json:"why,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Chat is the song as a chat reply: what a !song command says.
func (s Song) Chat() string {
	switch s.State {
	case Playing:
		if s.Link != "" {
			return "Now playing: " + s.Text + " → " + s.Link
		}
		return "Now playing: " + s.Text
	case Other:
		return s.Text
	case NotUsing, NotSignedIn:
		return "Pretzel isn't set up for this channel."
	}
	return "I can't tell what's playing right now."
}

var (
	idPattern   = regexp.MustCompile(`^[0-9]{1,20}$`)
	linkPattern = regexp.MustCompile(`^https://prtzl\.io/[A-Za-z0-9_-]{1,64}$`)
)

// clean makes Pretzel's text safe to show: valid UTF-8, no control or
// direction-changing characters, spaces squeezed, and not too long.
func clean(s string, limit int) string {
	s = strings.ToValidUTF8(s, "")
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
	if utf8.RuneCountInString(out) > limit {
		out = string([]rune(out)[:limit-1]) + "…"
	}
	return out
}

// Parse reads Pretzel's now-playing line, as its API sends it:
//
//	Now Playing: Title by Artist -> https://prtzl.io/abc
//
// The artist may be missing ("Title by  -> …"). A title with " by " in it
// is split at the last one, so "Stand by Me by Ben" is right; an artist
// with " by " in it isn't, and can't be told apart. Anything else is
// Other, kept as said.
func Parse(line string) Song {
	const prefix = "now playing:"
	raw := strings.ToValidUTF8(line, "")
	if len(raw) < len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return Song{State: Other, Text: clean(raw, 2*maxField)}
	}
	rest := raw[len(prefix):]
	var link string
	if i := strings.LastIndex(rest, "->"); i >= 0 {
		if l := strings.TrimSpace(rest[i+2:]); linkPattern.MatchString(l) {
			link, rest = l, rest[:i]
		}
	}
	rest = strings.TrimRightFunc(rest, unicode.IsSpace)
	title, artist := rest, ""
	if i := strings.LastIndex(rest, " by "); i >= 0 {
		title, artist = rest[:i], rest[i+4:]
	} else if strings.HasSuffix(rest, " by") {
		title = strings.TrimSuffix(rest, " by")
	}
	title, artist = clean(title, maxField), clean(artist, maxField)
	if title == "" {
		title, artist = artist, ""
	}
	if title == "" {
		return Song{State: Other, Text: clean(raw, 2*maxField)}
	}
	text := title
	if artist != "" {
		text += " by " + artist
	}
	return Song{State: Playing, Title: title, Artist: artist, Text: text, Link: link}
}

// Same reports whether two songs show alike: a new check of the same
// song isn't a change.
func Same(a, b Song) bool {
	return a.State == b.State && a.Title == b.Title && a.Artist == b.Artist && a.Text == b.Text && a.Link == b.Link
}

// Options configures a Service.
type Options struct {
	// Base replaces DefaultBase, for mockpretzel.
	Base string
	HTTP *http.Client
	// UserID is the streamer's numeric Twitch user id, "" when not
	// signed in.
	UserID func() string
	// Watching counts open Now playing overlays, OBS's and the app's
	// previews: with none, nothing is asked.
	Watching func() int
	// Show gets each change to what overlays should show.
	Show   func(Song)
	Logger *slog.Logger
	Now    func() time.Time
}

// Service reads Pretzel and keeps overlays up to date.
type Service struct {
	base     string
	http     *http.Client
	userID   func() string
	watching func() int
	show     func(Song)
	log      *slog.Logger
	now      func() time.Time
	wake     chan struct{}

	check sync.Mutex // one check at a time

	mu       sync.Mutex
	last     Song   // the latest check
	lastID   string // the account it was for
	shown    Song   // what overlays were last told
	failures int    // failed checks in a row
	until    time.Time
	stop     context.CancelFunc
	done     chan struct{}
}

// New returns a Service. Call Run to keep overlays up to date.
func New(o Options) *Service {
	s := &Service{base: strings.TrimRight(o.Base, "/"), http: o.HTTP, userID: o.UserID, watching: o.Watching, show: o.Show,
		log: o.Logger, now: o.Now, wake: make(chan struct{}, 1)}
	if s.base == "" {
		s.base = DefaultBase
	}
	if s.http == nil {
		s.http = &http.Client{Timeout: 10 * time.Second}
	}
	if s.userID == nil {
		s.userID = func() string { return "" }
	}
	if s.watching == nil {
		s.watching = func() int { return 0 }
	}
	if s.show == nil {
		s.show = func(Song) {}
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.last = Song{State: Unknown, Why: "not checked yet"}
	s.shown = Song{State: Unknown}
	return s
}

// Last is the latest check, without asking Pretzel.
func (s *Service) Last() Song {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// Wake checks soon: a Now playing overlay just opened.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Now asks Pretzel what's playing, unless it was asked moments ago.
func (s *Service) Now(ctx context.Context) Song {
	s.check.Lock()
	defer s.check.Unlock()
	id := s.userID()
	s.mu.Lock()
	last, lastID, until := s.last, s.lastID, s.until
	s.mu.Unlock()
	now := s.now()
	if !last.CheckedAt.IsZero() && now.Sub(last.CheckedAt) < fresh && id == lastID {
		return last
	}
	if now.Before(until) {
		// Pretzel asked to be left alone for a while.
		return Song{State: Unknown, Why: "Pretzel is busy; trying again shortly", CheckedAt: now}
	}
	song, wait := s.fetch(ctx, id)
	song.CheckedAt = s.now()
	s.mu.Lock()
	s.last, s.lastID = song, id
	if wait > 0 {
		s.until = song.CheckedAt.Add(min(wait, maxBackoff))
	}
	if song.State == Unknown {
		s.failures++
	} else {
		s.failures = 0
	}
	// A song stays shown through a failed check or so.
	show := song.State != Unknown || s.failures >= failuresToHide || s.shown.State != Playing
	changed := show && !Same(song, s.shown)
	if changed {
		s.shown = song
	}
	s.mu.Unlock()
	if changed {
		if song.State == Unknown {
			s.log.Warn("pretzel: can't tell what's playing", "why", song.Why)
		}
		s.show(song)
	}
	return song
}

// Start keeps open Now playing overlays up to date until Close.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.mu.Lock()
	s.stop, s.done = cancel, done
	s.mu.Unlock()
	go func() { defer close(done); s.Run(ctx) }()
}

// Close stops Start's checks, and waits for one under way.
func (s *Service) Close() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
}

// Run keeps open Now playing overlays up to date until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
		if s.watching() > 0 {
			s.Now(ctx)
		}
	}
}

// fetch asks Pretzel once. wait > 0 when Pretzel asked to wait.
func (s *Service) fetch(ctx context.Context, id string) (Song, time.Duration) {
	if id == "" {
		return Song{State: NotSignedIn, Why: "sign in to Twitch on Connections, so StreamGremlin knows your account"}, 0
	}
	if !idPattern.MatchString(id) {
		return Song{State: Unknown, Why: "your Twitch account's id doesn't look right"}, 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/playing/twitch/"+id, nil)
	if err != nil {
		return Song{State: Unknown, Why: err.Error()}, 0
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "StreamGremlin (https://streamgremlin.com)")
	resp, err := s.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Song{State: Unknown, Why: "stopped"}, 0
		}
		return Song{State: Unknown, Why: "Pretzel didn't answer"}, 0
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Song{State: Unknown, Why: "Pretzel's answer was cut off"}, 0
	}
	if len(body) > maxBody {
		return Song{State: Unknown, Why: "Pretzel's answer was too long"}, 0
	}
	text := strings.TrimSpace(string(body))
	switch {
	case resp.StatusCode == http.StatusOK && text != "":
		return Parse(text), 0
	case resp.StatusCode == http.StatusOK:
		return Song{State: Unknown, Why: "Pretzel's answer was empty"}, 0
	case resp.StatusCode == http.StatusBadRequest && strings.EqualFold(text, "ID not found"),
		resp.StatusCode == http.StatusNotFound:
		return Song{State: NotUsing, Why: "Pretzel doesn't know your Twitch account: sign in to Pretzel with it"}, 0
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		wait := Every * 4
		if n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && n > 0 {
			wait = time.Duration(n) * time.Second
		}
		return Song{State: Unknown, Why: "Pretzel is busy; trying again shortly"}, wait
	}
	return Song{State: Unknown, Why: fmt.Sprintf("Pretzel answered %d", resp.StatusCode)}, 0
}
