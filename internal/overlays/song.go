package overlays

// Now playing overlays (D-091) show the song Pretzel's desktop app is
// playing, read from the files its Write to File writes: the song as
// text, and its cover as a picture. Each overlay names its own files;
// the engine reads them while the overlay is open (songfile.go) and sends
// each change. The page shows nothing when nothing is playing or the
// files can't be read.

// KindSong shows the current song.
const KindSong = "song"

// SongView is what a Now playing overlay shows.
type SongView struct {
	// Playing is false when nothing is playing, Pretzel says it's paused,
	// or the song's file can't be read: the page shows nothing.
	Playing bool `json:"playing"`
	// Title is the file's first line; Artist its second, if any.
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	// Picture is the cover, through the overlay's own link
	// ("art?v=…"), or "" for none.
	Picture string `json:"picture,omitempty"`
	// Sample: a made-up song, from Sample in OBS or the preview.
	Sample bool `json:"sample,omitempty"`
}

// SetSong sends a Now playing overlay its song, and keeps it for pages
// that open later.
func (h *Hub) SetSong(name string, v SongView) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.songs[name] == v {
		return
	}
	h.songs[name] = v
	b := message("song", v)
	for c := range h.conns[name] {
		if c.kind == KindSong {
			h.sendLocked(c, b)
		}
	}
}

// SongOverlays names the Now playing overlays open now, in OBS or the
// app's previews: only their files are read.
func (h *Hub) SongOverlays() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for name, cs := range h.conns {
		for c := range cs {
			if c.kind == KindSong {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// SampleSong shows a made-up song on one overlay: in the app's previews
// only, or in OBS as well, for a few seconds; then the page shows the real
// song again.
func (h *Hub) SampleSong(name string, previewsOnly bool) (pages, previews int) {
	v := SongView{Playing: true, Title: "A sample song", Artist: "A sample artist", Sample: true}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns[name] {
		if c.kind != KindSong || (previewsOnly && !c.preview) {
			continue
		}
		if c.preview {
			previews++
		} else {
			pages++
		}
		h.sendLocked(c, message("song", v))
	}
	return pages, previews
}
