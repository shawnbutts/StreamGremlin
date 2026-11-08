package sounds

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Tiny files with real headers, made here (no sound files in the repo).
func wav(n int) []byte {
	b := append([]byte("RIFF\x00\x00\x00\x00WAVEfmt "), make([]byte, n)...)
	b[20] = byte(n & 0xff) // differs by size, so two sizes are different audio
	return b
}

var (
	mp3ID3   = append([]byte("ID3\x04\x00\x00"), make([]byte, 64)...)
	mp3Frame = append([]byte{0xFF, 0xFB, 0x90, 0x64}, make([]byte, 64)...)
	ogg      = append([]byte("OggS\x00\x02"), make([]byte, 64)...)
	m4a      = append([]byte("\x00\x00\x00\x20ftypM4A \x00\x00\x00\x00"), make([]byte, 64)...)
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return NewStore(db)
}

var now = time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC)

func TestSniffDecidesTheFormat(t *testing.T) {
	for want, b := range map[string][]byte{"wav": wav(8), "mp3": mp3ID3, "ogg": ogg, "m4a": m4a} {
		if got := Sniff(b); got != want {
			t.Errorf("%s: %q", want, got)
		}
	}
	if Sniff(mp3Frame) != "mp3" {
		t.Error("an MPEG frame")
	}
	for _, b := range [][]byte{nil, []byte("hello"), []byte("<!doctype html>"), []byte("\x89PNG\r\n\x1a\n"),
		[]byte("\x00\x00\x00\x20ftypqt  "), {0xFF, 0xF9, 0x00}} {
		if f := Sniff(b); f != "" {
			t.Errorf("%q sniffed as %s", b, f)
		}
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{"Air Horn!.mp3": "air-horn", "/x/y/BOOM_2.wav": "boom_2", "🎺.ogg": "sound", "a--b.m4a": "a-b"} {
		if got := NameFromFile(in); got != want || CheckName(got) != nil {
			t.Errorf("%q: %q", in, got)
		}
	}
	for _, bad := range []string{"", "Air", "-x", "a.b", "a/b", "../x"} {
		if CheckName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTheLibrary(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a, created, err := s.Add(ctx, "airhorn", mp3ID3, 70, now)
	if err != nil || !created || a.Format != "mp3" || a.Volume != 70 || a.Size != int64(len(mp3ID3)) || len(a.SHA256) != 64 {
		t.Fatalf("%+v %v %v", a, created, err)
	}
	// The same audio again is harmless; other audio under the name is refused.
	if _, created, err := s.Add(ctx, "airhorn", mp3ID3, 10, now); err != nil || created {
		t.Fatalf("repeat: %v %v", created, err)
	}
	if _, _, err := s.Add(ctx, "airhorn", wav(8), 70, now); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		data []byte
		vol  int
	}{{"Bad", ogg, 50}, {"x", ogg, 101}, {"x", ogg, -1}, {"x", nil, 50}, {"x", []byte("not audio"), 50},
		{"x", append(bytes.Clone(ogg), make([]byte, MaxBytes)...), 50}} {
		if _, _, err := s.Add(ctx, c.name, c.data, c.vol, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s %d bytes vol %d: %v", c.name, len(c.data), c.vol, err)
		}
	}
	if _, _, err := s.Add(ctx, "ding", wav(16), DefaultVolume, now); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(ctx)
	if len(list) != 2 || list[0].Name != "airhorn" || list[1].Name != "ding" {
		t.Fatalf("%+v", list)
	}
	x, data, err := s.Audio(ctx, "ding")
	if err != nil || x.Format != "wav" || !bytes.Equal(data, wav(16)) {
		t.Fatalf("%+v %v", x, err)
	}
	later := now.Add(time.Hour)
	if v, err := s.SetVolume(ctx, "ding", DefaultVolume, later); err != nil || !v.UpdatedAt.Equal(now) {
		t.Fatalf("unchanged: %+v %v", v, err)
	}
	if v, err := s.SetVolume(ctx, "ding", 25, later); err != nil || v.Volume != 25 || !v.UpdatedAt.Equal(later) {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := s.SetVolume(ctx, "ding", 200, later); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.SetVolume(ctx, "nope", 5, later); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if ok, _ := s.Delete(ctx, "ding"); !ok {
		t.Fatal("delete")
	}
	if ok, _ := s.Delete(ctx, "ding"); ok {
		t.Fatal("twice")
	}
	if _, _, err := s.Audio(ctx, "ding"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestTheLibraryIsLimited(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	big := append(bytes.Clone(ogg), make([]byte, MaxBytes-len(ogg))...)
	for i := 0; ; i++ {
		_, _, err := s.Add(ctx, "big"+string(rune('a'+i)), append(bytes.Clone(big[:len(big)-1]), byte(i)), 50, now)
		if errors.Is(err, ErrFull) {
			if i != MaxTotalBytes/MaxBytes {
				t.Fatalf("full after %d", i)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// player runs a fake window: it polls, and answers each play with answer.
func window(ctx context.Context, p *Player, answer func(Command) Report, seen chan<- Command) {
	_, after := p.Next(ctx, -1, 0)
	for ctx.Err() == nil {
		cmds, head := p.Next(ctx, after, 50*time.Millisecond)
		for _, c := range cmds {
			if seen != nil {
				seen <- c
			}
			if c.Kind == "play" && answer != nil {
				p.Report(answer(c))
			}
		}
		after = head
	}
}

func TestPlayingWithoutAWindowSaysSo(t *testing.T) {
	p := NewPlayer(nil)
	if p.Connected() {
		t.Fatal("connected with no window")
	}
	if _, err := p.Play(context.Background(), Sound{Name: "ding"}, 50, ""); !errors.Is(err, ErrNoPlayer) {
		t.Fatal(err)
	}
	if err := p.Stop(); !errors.Is(err, ErrNoPlayer) {
		t.Fatal(err)
	}
}

func TestAWindowPlaysAndReports(t *testing.T) {
	p := NewPlayer(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan Command, 10)
	go window(ctx, p, func(c Command) Report {
		if c.Sound == "broken" {
			return Report{ID: c.ID, Error: "NotSupportedError"}
		}
		return Report{ID: c.ID, OK: true}
	}, seen)
	for !p.Connected() {
		time.Sleep(time.Millisecond)
	}
	res, err := p.Play(ctx, Sound{Name: "ding", Format: "wav"}, 40, "job1/steps[0]")
	if err != nil || !res.Played || res.Volume != 40 {
		t.Fatalf("%+v %v", res, err)
	}
	if c := <-seen; c.Kind != "play" || c.Sound != "ding" || c.Format != "wav" || c.Volume != 40 {
		t.Fatalf("%+v", c)
	}
	// A retried step doesn't play again.
	if res, err := p.Play(ctx, Sound{Name: "ding"}, 40, "job1/steps[0]"); err != nil || !res.Repeated || res.Played {
		t.Fatalf("%+v %v", res, err)
	}
	// A failure says why, and a retry may try again.
	if _, err := p.Play(ctx, Sound{Name: "broken"}, 40, "job2/steps[0]"); err == nil || !contains(err.Error(), "NotSupportedError") {
		t.Fatal(err)
	}
	<-seen
	if _, err := p.Play(ctx, Sound{Name: "broken"}, 40, "job2/steps[0]"); err == nil {
		t.Fatal("the retry wasn't tried")
	}
	<-seen
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if c := <-seen; c.Kind != "stop" {
		t.Fatalf("%+v", c)
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }

func TestAWindowThatDoesntAnswer(t *testing.T) {
	p := NewPlayer(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go window(ctx, p, nil, nil) // polls, never reports
	for !p.Connected() {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	_, err := p.Play(ctx, Sound{Name: "ding"}, 50, "k")
	if !errors.Is(err, ErrNoAnswer) || time.Since(start) < AnswerWait {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	// It may have played: a retry doesn't risk playing twice.
	if res, err := p.Play(ctx, Sound{Name: "ding"}, 50, "k"); err != nil || !res.Repeated {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestANewWindowPlaysNothingOld(t *testing.T) {
	clock := now
	var mu sync.Mutex
	tick := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	p := NewPlayer(tick)
	ctx := context.Background()
	_, _ = p.Next(ctx, -1, 0) // a window was here
	go func() { _, _ = p.Play(ctx, Sound{Name: "old"}, 50, "") }()
	for {
		p.mu.Lock()
		n := len(p.commands)
		p.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// A window opening now starts from the current position.
	cmds, head := p.Next(ctx, -1, 0)
	if len(cmds) != 0 || head != 1 {
		t.Fatalf("%+v %d", cmds, head)
	}
	// One that was asleep doesn't play stale commands.
	mu.Lock()
	clock = clock.Add(Stale)
	mu.Unlock()
	cmds, head = p.Next(ctx, 0, 0)
	if len(cmds) != 0 || head != 1 {
		t.Fatalf("stale: %+v %d", cmds, head)
	}
	// A window gone quiet for long is gone.
	mu.Lock()
	clock = clock.Add(PlayerGone)
	mu.Unlock()
	if p.Connected() {
		t.Fatal("still connected")
	}
}
