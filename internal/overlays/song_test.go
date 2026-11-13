package overlays

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Songs reach their own Now playing overlay only, changes only; a page
// that opens later gets the song; samples in the preview never reach OBS;
// a page never learns where the files are.
func TestSongsReachTheirOverlay(t *testing.T) {
	h := NewHub(nil)
	look := DefaultLook()
	look.SongFile, look.ArtFile, look.PausedText = "/Users/me/Pretzel/song.txt", "/Users/me/Pretzel/art.jpg", "Paused"
	so := Overlay{Name: "song", Kind: KindSong, Look: look}
	page, preview := h.join(so, false), h.join(so, true)
	other := h.join(Overlay{Name: "other-song", Kind: KindSong}, false)
	alert := h.join(Overlay{Name: "alerts", Kind: KindAlert}, false)
	_, bodies := drain(page)
	if strings.Contains(string(bodies[0]), "Pretzel") || strings.Contains(string(bodies[0]), "Paused") {
		t.Fatal("the hello told the page where the files are:", string(bodies[0]))
	}
	for _, c := range []*conn{preview, other, alert} {
		drain(c)
	}
	if got := h.SongOverlays(); len(got) != 2 {
		t.Fatal(got)
	}
	v := SongView{Playing: true, Title: "Neon Rain", Artist: "Kyle", Picture: "art?v=1-2"}
	h.SetSong("song", v)
	h.SetSong("song", v) // the same again: not sent
	for name, c := range map[string]*conn{"page": page, "preview": preview} {
		types, bodies := drain(c)
		if count(types, "song") != 1 {
			t.Fatal(name, types)
		}
		var got SongView
		if json.Unmarshal(bodies[0], &got) != nil || got != v {
			t.Fatal(name, string(bodies[0]))
		}
	}
	for name, c := range map[string]*conn{"other": other, "alert": alert} {
		if types, _ := drain(c); len(types) != 0 {
			t.Fatal(name, "got a song", types)
		}
	}
	late := h.join(so, false)
	if types, bodies := drain(late); count(types, "song") != 1 || !strings.Contains(string(bodies[len(bodies)-1]), "Neon Rain") {
		t.Fatal("late:", types)
	}
	if pages, previews := h.SampleSong("song", true); pages != 0 || previews != 1 {
		t.Fatal(pages, previews)
	}
	if types, _ := drain(page); len(types) != 0 {
		t.Fatal("OBS got a preview sample", types)
	}
}

func TestSongLooksAreChecked(t *testing.T) {
	s, _, _ := newStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	look := DefaultLook()
	look.SongHides, look.SongFile, look.ArtFile, look.PausedText = true, filepath.Join(dir, "song.txt"), filepath.Join(dir, "art.JPG"), "Paused"
	o, _, err := s.Create(ctx, "now-playing", KindSong, look, now)
	if err != nil || o.Kind != KindSong || !o.Look.SongHides || o.Look.SongFile != look.SongFile {
		t.Fatal(o, err)
	}
	for _, bad := range []func(l *Look){
		func(l *Look) { l.SongFile = "song.txt" },             // not a full path
		func(l *Look) { l.SongFile = dir + "/../x/song.txt" }, // not clean
		func(l *Look) { l.SongFile = filepath.Join(dir, "song.json") },
		func(l *Look) { l.ArtFile = filepath.Join(dir, "art.gif") },
		func(l *Look) { l.PausedText = "two\nlines" },
	} {
		l := look
		bad(&l)
		if _, err := s.SetLook(ctx, "now-playing", l, now); err == nil {
			t.Errorf("%+v was saved", l)
		}
	}
	if _, _, err := s.Create(ctx, "alerts", KindAlert, look, now); err == nil {
		t.Fatal("an alert overlay took song files")
	}
	if _, _, err := s.Create(ctx, "x", "music", DefaultLook(), now); err == nil {
		t.Fatal("an unknown kind was stored")
	}
}

func TestReadSong(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "song.txt")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct{ in, paused, title, artist, why string }{
		{"Caffeine Part 2", "", "Caffeine Part 2", "", ""},
		{"Neon Rain\r\nKyle Tryhard\r\nAlbum\r\n", "", "Neon Rain", "Kyle Tryhard", ""},
		{"\n\n  Spaced   out  \n", "", "Spaced out", "", ""},
		{"Bad\u202eSong\x07", "", "BadSong", "", ""},
		{"", "", "", "", "empty"},
		{"  \n ", "", "", "", "empty"},
		{"Paused", "paused", "", "", "paused"},
		{"Paused", "", "Paused", "", ""},
	}
	for _, c := range cases {
		title, artist, why := readSong(write(c.in), c.paused)
		if title != c.title || artist != c.artist || why != c.why {
			t.Errorf("%q: %q %q %q", c.in, title, artist, why)
		}
	}
	if _, _, why := readSong(write(strings.Repeat("x", maxSongFile+1)), ""); !strings.Contains(why, "too big") {
		t.Fatal(why)
	}
	if title, _, _ := readSong(write(strings.Repeat("x", 500)), ""); len([]rune(title)) != maxSongLine {
		t.Fatal(len(title))
	}
	if _, _, why := readSong(filepath.Join(dir, "missing.txt"), ""); !strings.Contains(why, "Write to File") {
		t.Fatal(why)
	}
}

// A tiny JPEG and PNG: enough for a picture's kind to be told.
var (
	jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, bytes.Repeat([]byte{0}, 64)...)
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
)

// The reader: only open overlays' files; a file is read once it's
// stopped changing; each change reaches the page; the picture is served
// only through the overlay's link and only as a JPEG or PNG; paused and
// missing files show nothing, and say why.
func TestSongFilesReachTheOverlay(t *testing.T) {
	s, hub, srv := serve(t)
	ctx := context.Background()
	dir := t.TempDir()
	songPath, artPath := filepath.Join(dir, "song.txt"), filepath.Join(dir, "art.jpg")
	look := DefaultLook()
	look.SongFile, look.ArtFile, look.PausedText = songPath, artPath, "Paused"
	_, token, err := s.Create(ctx, "now-playing", KindSong, look, now)
	if err != nil {
		t.Fatal(err)
	}
	r := NewSongReader(s, hub, nil)
	tick := func() { r.Tick(ctx) }

	write := func(p string, b []byte) {
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		// A different time, as a later write has.
		at := time.Now().Add(time.Duration(len(b)) * time.Second)
		_ = os.Chtimes(p, at, at)
	}
	write(songPath, []byte("Neon Rain\nKyle"))
	write(artPath, jpegBytes)
	tick()
	if st := r.Status("now-playing"); st.Open {
		t.Fatal("read with nothing open:", st)
	}

	page := hub.join(Overlay{Name: "now-playing", Kind: KindSong, Look: look}, false)
	drain(page)
	tick() // seen
	tick() // the same: read
	types, bodies := drain(page)
	if count(types, "song") != 1 {
		t.Fatal(types)
	}
	var v SongView
	_ = json.Unmarshal(bodies[len(bodies)-1], &v)
	if !v.Playing || v.Title != "Neon Rain" || v.Artist != "Kyle" || !strings.HasPrefix(v.Picture, "art?v=") {
		t.Fatalf("%+v", v)
	}
	res, err := http.Get(srv.URL + "/o/" + token + "/" + v.Picture)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(b, jpegBytes) {
		t.Fatal(res.StatusCode, res.Header.Get("Content-Type"))
	}

	// The next song: shown once the file's settled.
	write(songPath, []byte("Caffeine Part 2"))
	tick()
	if types, _ := drain(page); len(types) != 0 {
		t.Fatal("read a file still changing:", types)
	}
	tick()
	_, bodies = drain(page)
	v = SongView{}
	_ = json.Unmarshal(bodies[len(bodies)-1], &v)
	if v.Title != "Caffeine Part 2" || v.Artist != "" {
		t.Fatalf("%+v", v)
	}

	// Paused: nothing shown, and no problem said.
	write(songPath, []byte("Paused\n"))
	tick()
	tick()
	if st := r.Status("now-playing"); st.Song.Playing || st.Problem != "" || !st.Open {
		t.Fatalf("paused: %+v", st)
	}

	// A picture that isn't one is never served or shown.
	write(songPath, []byte("Back again"))
	write(artPath, []byte("#!/bin/sh\necho not a picture"))
	tick()
	tick()
	if st := r.Status("now-playing"); !st.Song.Playing || st.Song.Picture != "" || !strings.Contains(st.Problem, "JPEG or PNG") {
		t.Fatalf("not a picture: %+v", st)
	}
	res, err = http.Get(srv.URL + "/o/" + token + "/art")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("served a file that isn't a picture:", res.StatusCode)
	}
	write(artPath, pngBytes)
	tick()
	tick()
	if st := r.Status("now-playing"); st.Song.Picture == "" || st.Problem != "" {
		t.Fatalf("png: %+v", st)
	}

	// The song's file gone: nothing shown, and why.
	_ = os.Remove(songPath)
	tick()
	tick()
	if st := r.Status("now-playing"); st.Song.Playing || !strings.Contains(st.Problem, "isn't there") {
		t.Fatalf("missing: %+v", st)
	}

	// Closed: forgotten.
	hub.leave(page)
	tick()
	if st := r.Status("now-playing"); st.Open {
		t.Fatal("still reading a closed overlay")
	}
}
