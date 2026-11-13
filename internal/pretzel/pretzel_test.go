package pretzel

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockpretzel"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in                        string
		state, title, artist, url string
	}{
		// As Pretzel answered on 2026-11-13: the artist may be empty.
		{"Now Playing: Caffeine Part 2 by  -> https://prtzl.io/jDH8BenoZ1PeBxQ", Playing, "Caffeine Part 2", "", "https://prtzl.io/jDH8BenoZ1PeBxQ"},
		{"Now Playing: Neon Rain by Kyle Tryhard -> https://prtzl.io/abc", Playing, "Neon Rain", "Kyle Tryhard", "https://prtzl.io/abc"},
		// " by " in the title: split at the last.
		{"Now Playing: Stand by Me by The Band -> https://prtzl.io/x", Playing, "Stand by Me", "The Band", "https://prtzl.io/x"},
		{"now playing: Lo-fi Beats by Chill", Playing, "Lo-fi Beats", "Chill", ""},
		{"Now Playing: Only a title", Playing, "Only a title", "", ""},
		{"Now Playing: Song by ", Playing, "Song", "", ""},
		// A link elsewhere isn't Pretzel's: kept in the text, never a link.
		{"Now Playing: Song by Me -> https://evil.example/x", Playing, "Song", "Me -> https://evil.example/x", ""},
		// Control and direction characters are dropped; spaces squeezed.
		{"Now Playing: Bad\u202eSong\x07  Name by A\nB -> https://prtzl.io/q", Playing, "BadSong Name", "A B", "https://prtzl.io/q"},
		{"Nothing playing", Other, "", "", ""},
		{"Now Playing:  by  -> https://prtzl.io/q", Other, "", "", ""},
	}
	for _, c := range cases {
		s := Parse(c.in)
		if s.State != c.state || s.Title != c.title || s.Artist != c.artist || s.Link != c.url {
			t.Errorf("%q: %+v", c.in, s)
		}
	}
	if s := Parse("Now Playing: " + strings.Repeat("x", 500)); len([]rune(s.Title)) != maxField || !strings.HasSuffix(s.Title, "…") {
		t.Fatal("long title:", len(s.Title))
	}
	if s := Parse("Nothing playing"); s.Text != "Nothing playing" || s.Chat() != "Nothing playing" {
		t.Fatal(s)
	}
	if c := Parse("Now Playing: Neon Rain by Kyle -> https://prtzl.io/abc").Chat(); c != "Now playing: Neon Rain by Kyle → https://prtzl.io/abc" {
		t.Fatal(c)
	}
	if c := Parse("Now Playing: Caffeine by  -> https://prtzl.io/a").Text; c != "Caffeine" {
		t.Fatal(c)
	}
}

type rig struct {
	mock     *mockpretzel.Server
	s        *Service
	mu       sync.Mutex
	shown    []Song
	id       string
	watching int
	now      time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{mock: mockpretzel.New(), id: "12345", watching: 1, now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
	srv := httptest.NewServer(r.mock)
	t.Cleanup(srv.Close)
	r.s = New(Options{Base: srv.URL, UserID: func() string { return r.id }, Watching: func() int { r.mu.Lock(); defer r.mu.Unlock(); return r.watching },
		Show: func(s Song) { r.mu.Lock(); r.shown = append(r.shown, s); r.mu.Unlock() },
		Now:  func() time.Time { return r.now }})
	return r
}

func (r *rig) check() Song {
	r.now = r.now.Add(Every)
	return r.s.Now(context.Background())
}

func (r *rig) shows() []Song {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Song(nil), r.shown...)
}

// Overlays hear of changes only; a moment's trouble doesn't hide the song;
// lasting trouble does, as unknown.
func TestChangesAndTrouble(t *testing.T) {
	r := newRig(t)
	r.mock.Set("12345", mockpretzel.Line("Neon Rain", "Kyle", "https://prtzl.io/a"))
	if s := r.check(); s.State != Playing || s.Title != "Neon Rain" {
		t.Fatal(s)
	}
	r.check() // the same song: not a change
	if n := len(r.shows()); n != 1 {
		t.Fatal("shown", n)
	}
	r.mock.Set("12345", mockpretzel.Line("Caffeine", "", "https://prtzl.io/b"))
	r.check()
	if sh := r.shows(); len(sh) != 2 || sh[1].Title != "Caffeine" || sh[1].Artist != "" {
		t.Fatal(sh)
	}

	r.mock.Fail(500, "")
	if s := r.check(); s.State != Unknown || !strings.Contains(s.Why, "500") {
		t.Fatal(s)
	}
	if n := len(r.shows()); n != 2 {
		t.Fatal("one failure hid the song")
	}
	r.check()
	if sh := r.shows(); len(sh) != 3 || sh[2].State != Unknown {
		t.Fatal("two failures:", sh)
	}
	r.check() // still failing: no repeat
	if n := len(r.shows()); n != 3 {
		t.Fatal(n)
	}
	r.mock.Fail(0, "")
	r.check()
	if sh := r.shows(); len(sh) != 4 || sh[3].Title != "Caffeine" {
		t.Fatal("back:", sh)
	}
}

func TestNotUsingAndNotSignedIn(t *testing.T) {
	r := newRig(t)
	if s := r.check(); s.State != NotUsing || s.Chat() != "Pretzel isn't set up for this channel." {
		t.Fatal(s)
	}
	// Signing in moments later isn't hidden by the check before it.
	r.id = ""
	r.s.Now(context.Background())
	r.id = "12345"
	if s := r.s.Now(context.Background()); s.State != NotUsing {
		t.Fatal("after signing in:", s)
	}
	r.id = ""
	before := r.mock.Requests()
	if s := r.check(); s.State != NotSignedIn || r.mock.Requests() != before {
		t.Fatal(s, "asked without an account")
	}
	r.id = "1/../2"
	if s := r.check(); s.State != Unknown || r.mock.Requests() != before {
		t.Fatal(s, "asked with a bad id")
	}
}

// A check moments after another reuses it; a busy Pretzel is left alone
// for as long as it asks.
func TestFreshAndBusy(t *testing.T) {
	r := newRig(t)
	r.mock.Set("12345", mockpretzel.Line("A", "B", "https://prtzl.io/a"))
	r.check()
	r.now = r.now.Add(time.Second)
	r.s.Now(context.Background())
	if n := r.mock.Requests(); n != 1 {
		t.Fatal("asked again at once:", n)
	}
	r.mock.Fail(429, "120")
	if s := r.check(); s.State != Unknown {
		t.Fatal(s)
	}
	n := r.mock.Requests()
	r.check()
	r.check()
	if r.mock.Requests() != n {
		t.Fatal("didn't wait as asked")
	}
	r.mock.Fail(0, "")
	r.now = r.now.Add(2 * time.Minute)
	if s := r.check(); s.State != Playing || r.mock.Requests() != n+1 {
		t.Fatal("after the wait:", s)
	}
}

func TestTooLongAnswer(t *testing.T) {
	r := newRig(t)
	r.mock.Set("12345", strings.Repeat("x", maxBody+1))
	if s := r.check(); s.State != Unknown {
		t.Fatal(s)
	}
}

// Run asks only while an overlay is open, and at once when one opens.
func TestRunOnlyWhileWatched(t *testing.T) {
	r := newRig(t)
	r.mock.Set("12345", mockpretzel.Line("A", "B", "https://prtzl.io/a"))
	r.s.now = time.Now
	r.watching = 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.s.Run(ctx); close(done) }()
	r.s.Wake()
	time.Sleep(100 * time.Millisecond)
	if n := r.mock.Requests(); n != 0 {
		t.Fatal("asked with nothing open:", n)
	}
	r.mu.Lock()
	r.watching = 1
	r.mu.Unlock()
	r.s.Wake()
	deadline := time.Now().Add(2 * time.Second)
	for r.mock.Requests() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r.mock.Requests() != 1 {
		t.Fatal("not asked when an overlay opened")
	}
	cancel()
	<-done
}

func TestManifestIsComplete(t *testing.T) {
	m := PretzelManifest()
	if m.VerifiedAt == "" || len(m.Docs) == 0 || len(m.Capabilities) == 0 {
		t.Fatal(m)
	}
	for _, c := range m.Capabilities {
		if c.Name == "" || c.Notes == "" || (c.Support != "supported" && c.Support != "partial" && c.Support != "unsupported" && c.Support != "unknown") {
			t.Errorf("%+v", c)
		}
	}
}
