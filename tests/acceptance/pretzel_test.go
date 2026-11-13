//go:build acceptance

package acceptance

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"code.streamgremlin.com/streamgremlin/internal/mockpretzel"
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

// Pretzel through the real engine, CLI, mocktwitch and mockpretzel
// (D-090): nothing is asked until something needs the song; the CLI says
// what's playing; a Now playing overlay opened as OBS would gets the song
// at once and each change; !song answers in chat.
func TestPretzelThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	pz := mockpretzel.New()
	pzSrv := httptest.NewServer(pz)
	t.Cleanup(pzSrv.Close)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL, "-pretzel-api-url", pzSrv.URL)

	if out, _, code := cli(t, dir, "pretzel"); code == 0 || !strings.Contains(out, "sign in to Twitch") {
		t.Fatal("not signed in:", out)
	}
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	status, _, _ := cli(t, dir, "twitch", "status", "-json")
	m := regexp.MustCompile(`"user_id":\s*"([0-9]+)"`).FindStringSubmatch(status)
	if m == nil {
		t.Fatal(status)
	}
	id := m[1]
	if n := pz.Requests(); n != 0 {
		t.Fatal("Pretzel was asked with nothing needing it:", n)
	}
	if out, _, code := cli(t, dir, "pretzel"); code == 0 || !strings.Contains(out, "Pretzel doesn't know your Twitch account") {
		t.Fatal("not using Pretzel:", out)
	}

	// A check moments after another reuses it, so this one may take a few seconds.
	pz.Set(id, "Now Playing: Caffeine Part 2 by  -> https://prtzl.io/abc")
	eventually(t, "the CLI", func() string {
		if out, errOut, code := cli(t, dir, "pretzel"); code != 0 || strings.TrimSpace(out) != "Now playing: Caffeine Part 2 → https://prtzl.io/abc" {
			return out + errOut
		}
		return ""
	})

	out, errOut, code := cli(t, dir, "overlays", "add", "-song", "now-playing")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	songs := songPage(t, linkIn(t, out))
	last := func(want string) func() string {
		return func() string {
			s := songs()
			if len(s) > 0 && s[len(s)-1]["title"] == want && s[len(s)-1]["playing"] == true {
				return ""
			}
			return "songs: " + func() string { b, _ := json.Marshal(s); return string(b) }()
		}
	}
	eventually(t, "the song on open", last("Caffeine Part 2"))
	// Overlays are checked every 15 seconds; any check sends a change, so
	// the CLI's brings it sooner.
	pz.Set(id, mockpretzel.Line("Neon Rain", "Kyle", "https://prtzl.io/def"))
	eventually(t, "the next song", func() string { cli(t, dir, "pretzel"); return last("Neon Rain")() })
	if s := songs(); s[len(s)-1]["artist"] != "Kyle" {
		t.Fatal(s)
	}

	tmpl, _, _ := cli(t, dir, "templates", "show", "song")
	if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, t.TempDir(), "song.json", tmpl)); code != 0 {
		t.Fatal(out, errOut)
	}
	tw.Say("streamer", "viewer1", "!song")
	eventually(t, "!song", func() string {
		var b strings.Builder
		for _, m := range tw.ChatLog() {
			if m.Text == "Now playing: Neon Rain by Kyle → https://prtzl.io/def" {
				return ""
			}
			b.WriteString(m.Text + " / ")
		}
		return b.String()
	})
}
