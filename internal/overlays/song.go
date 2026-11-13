package overlays

// Now playing overlays (D-090) show the song Pretzel is playing. The
// engine asks Pretzel only while one is open, and sends each change; the
// page shows a song only while it's playing, and nothing when it's
// unknown.

// KindSong shows the current song.
const KindSong = "song"

// SongView is what a Now playing overlay shows.
type SongView struct {
	// Playing is false when nothing is playing or it's unknown: the page
	// shows nothing.
	Playing bool   `json:"playing"`
	Title   string `json:"title,omitempty"`
	Artist  string `json:"artist,omitempty"`
	// Sample: a made-up song, from Sample in OBS or the preview.
	Sample bool `json:"sample,omitempty"`
}

// OnSongPage says what to call when a Now playing overlay opens, so it
// shows the song at once rather than at the next check.
func (h *Hub) OnSongPage(f func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.songOpened = f
}

// SetSong sends the song to Now playing overlays, and keeps it for pages
// that open later.
func (h *Hub) SetSong(v SongView) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.song == v {
		return
	}
	h.song = v
	b := message("song", v)
	for _, cs := range h.conns {
		for c := range cs {
			if c.kind == KindSong {
				h.sendLocked(c, b)
			}
		}
	}
}

// SongPages counts open Now playing overlays, OBS's and the app's
// previews: while there are none, Pretzel isn't asked.
func (h *Hub) SongPages() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, cs := range h.conns {
		for c := range cs {
			if c.kind == KindSong {
				n++
			}
		}
	}
	return n
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
