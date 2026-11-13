//go:build acceptance

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// songPage opens a Now playing overlay as OBS would and keeps the songs
// it's sent.
func songPage(t *testing.T, link string) func() []map[string]any {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(link, "http")+"ws",
		&websocket.DialOptions{Subprotocols: []string{"streamgremlin.overlay.v1"}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []map[string]any
	go func() {
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m struct {
				Type string
				Body map[string]any
			}
			if json.Unmarshal(b, &m) == nil && m.Type == "song" {
				mu.Lock()
				got = append(got, m.Body)
				mu.Unlock()
			}
		}
	}()
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), got...)
	}
}

// Now playing from Pretzel's files, through the real engine and CLI
// (D-091): the overlay's files set by the CLI; a page opened as OBS would
// gets the song and its cover, and each change; paused shows nothing; the
// cover comes only through the overlay's link; the page never learns the
// paths.
func TestNowPlayingFromFiles(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	files := t.TempDir()
	song, art := filepath.Join(files, "song.txt"), filepath.Join(files, "album.jpg")
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, bytes.Repeat([]byte{7}, 200)...)
	writeFile(t, files, "song.txt", "Neon Rain\nKyle Tryhard\n")
	if err := os.WriteFile(art, jpeg, 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := cli(t, dir, "overlays", "add", "-song", "now-playing")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	link := linkIn(t, out)
	if out, errOut, code := cli(t, dir, "overlays", "look", "now-playing", "-song-file", song, "-art-file", art, "-paused-text", "Paused"); code != 0 {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "overlays", "look", "now-playing", "-song-file", "song.txt"); code == 0 || !strings.Contains(errOut, "full path") {
		t.Fatal("a relative path was taken:", errOut)
	}

	res, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(page), files) {
		t.Fatal("the page names the files")
	}
	songs := songPage(t, link)
	last := func(want, artist string) func() string {
		return func() string {
			s := songs()
			if len(s) > 0 {
				l := s[len(s)-1]
				if (want == "" && l["playing"] == false) || (l["title"] == want && (artist == "" || l["artist"] == artist)) {
					return ""
				}
			}
			b, _ := json.Marshal(s)
			return string(b)
		}
	}
	eventually(t, "the song on open", last("Neon Rain", "Kyle Tryhard"))
	s := songs()
	pic, _ := s[len(s)-1]["picture"].(string)
	if !strings.HasPrefix(pic, "art?v=") {
		t.Fatal("no cover:", s)
	}
	for _, m := range s {
		if b, _ := json.Marshal(m); strings.Contains(string(b), files) {
			t.Fatal("a song names the files:", string(b))
		}
	}
	res, err = http.Get(link + pic)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(got, jpeg) {
		t.Fatal("the cover:", res.StatusCode, res.Header.Get("Content-Type"))
	}
	res, err = http.Get(strings.Replace(link, token(link), "not-the-token", 1) + "art")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("the cover without the link:", res.StatusCode)
	}

	writeFile(t, files, "song.txt", "Caffeine Part 2")
	eventually(t, "the next song", last("Caffeine Part 2", ""))
	writeFile(t, files, "song.txt", "Paused")
	eventually(t, "paused", last("", ""))

	if out, _, _ := cli(t, dir, "overlays", "-json"); !strings.Contains(out, `"song_file"`) || !strings.Contains(out, `"open": true`) {
		t.Fatal("the app's view:", out)
	}
}
