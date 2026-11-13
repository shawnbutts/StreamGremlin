package overlays

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Songs reach Now playing overlays only, changes only; a page that opens
// later gets the song, and the engine hears it opened; samples in the
// preview never reach OBS.
func TestSongsReachSongOverlays(t *testing.T) {
	h := NewHub(nil)
	opened := make(chan struct{}, 4)
	h.OnSongPage(func() { opened <- struct{}{} })
	so := Overlay{Name: "song", Kind: KindSong}
	page, preview := h.join(so, false), h.join(so, true)
	alert := h.join(Overlay{Name: "alerts", Kind: KindAlert}, false)
	for _, c := range []*conn{page, preview, alert} {
		drain(c)
	}
	for range 2 {
		select {
		case <-opened:
		case <-time.After(time.Second):
			t.Fatal("not told a song page opened")
		}
	}
	if h.SongPages() != 2 {
		t.Fatal(h.SongPages())
	}
	v := SongView{Playing: true, Title: "Neon Rain", Artist: "Kyle"}
	h.SetSong(v)
	h.SetSong(v) // the same again: not sent
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
	if types, _ := drain(alert); len(types) != 0 {
		t.Fatal("an alert overlay got a song", types)
	}
	late := h.join(so, false)
	if types, bodies := drain(late); count(types, "song") != 1 || string(bodies[len(bodies)-1]) == "" {
		t.Fatal("late:", types)
	}
	if pages, previews := h.SampleSong("song", true); pages != 0 || previews != 1 {
		t.Fatal(pages, previews)
	}
	if types, _ := drain(page); len(types) != 0 {
		t.Fatal("OBS got a preview sample", types)
	}
}

func TestSongOverlaysAreStored(t *testing.T) {
	s, _, _ := newStore(t)
	look := DefaultLook()
	look.SongHides = true
	o, _, err := s.Create(context.Background(), "now-playing", KindSong, look, now)
	if err != nil || o.Kind != KindSong || !o.Look.SongHides {
		t.Fatal(o, err)
	}
	if _, _, err := s.Create(context.Background(), "x", "music", look, now); err == nil {
		t.Fatal("an unknown kind was stored")
	}
}
