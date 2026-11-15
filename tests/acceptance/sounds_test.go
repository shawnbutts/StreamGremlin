//go:build acceptance

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWindow plays the desktop app's window over the real API: it asks for
// what to play and reports each play as started. It returns what it heard.
func fakeWindow(t *testing.T, e *engine) (heard func() []string, stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		after := int64(-1)
		for ctx.Err() == nil {
			code, body := call(t, e, "GET", "/v1/player/next?wait=1&after="+strconv.FormatInt(after, 10), e.token, "", nil)
			if code != 200 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			var next struct {
				Commands []struct {
					ID, Kind, Sound string
					Volume          int
				}
				Head int64
			}
			_ = json.Unmarshal([]byte(body), &next)
			for _, c := range next.Commands {
				mu.Lock()
				got = append(got, c.Kind+" "+c.Sound+" "+strconv.Itoa(c.Volume))
				mu.Unlock()
				if c.Kind == "play" {
					call(t, e, "POST", "/v1/player/reports", e.token, `{"id":"`+c.ID+`","ok":true}`, nil)
				}
			}
			after = next.Head
		}
	}()
	return func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), got...)
		}, func() {
			cancel()
			<-done
		}
}

// Sounds through the real engine and CLI: added from a file, refused when
// it isn't audio, played only when a window is open, played by a chat
// command once, never by a test event, and kept across a crash.
func TestSoundsThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	files := t.TempDir()
	wav := writeFile(t, files, "Air Horn.wav", "RIFF\x24\x00\x00\x00WAVEfmt "+strings.Repeat("\x00", 64))
	notAudio := writeFile(t, files, "fake.mp3", "<html>not a sound</html>")

	if out, errOut, code := cli(t, dir, "sounds", "add", "-volume", "60", wav); code != 0 || !strings.Contains(out, "Added air-horn (wav") {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "sounds", "add", notAudio); code == 0 || !strings.Contains(errOut, "isn't an MP3, WAV, OGG or M4A") {
		t.Fatalf("not audio: %d %s", code, errOut)
	}
	out, _, _ := cli(t, dir, "sounds")
	if !strings.Contains(out, "air-horn") || !strings.Contains(out, "volume 60") || !strings.Contains(out, "No StreamGremlin window is open") {
		t.Fatalf("list:\n%s", out)
	}
	if _, errOut, code := cli(t, dir, "sounds", "play", "air-horn"); code == 0 || !strings.Contains(errOut, "no StreamGremlin window is open") {
		t.Fatalf("no window: %d %s", code, errOut)
	}

	heard, stop := fakeWindow(t, e)
	eventually(t, "the window", func() string {
		if out, _, _ := cli(t, dir, "sounds"); !strings.Contains(out, "The app is open") {
			return "not yet: " + out
		}
		return ""
	})
	if out, errOut, code := cli(t, dir, "sounds", "play", "-volume", "20", "air-horn"); code != 0 || !strings.Contains(out, "Playing air-horn at volume 20") {
		t.Fatal(out, errOut)
	}

	// A chat command plays it once; a test event never does.
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return "not yet: " + out
		}
		return ""
	})
	horn := writeFile(t, t.TempDir(), "horn.json", `{"schema":"streamgremlin.workflow/v1","id":"chat.horn","name":"Horn",
		"triggers":[{"type":"chat.command.received","where":{"command":"horn"}}],
		"steps":[{"action":"sounds.play","with":{"sound":"air-horn"}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", horn); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.horn")
	cli(t, dir, "events", "simulate", "chat.command.received", "command=!horn")
	eventually(t, "the test run", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.horn"); !strings.Contains(out, "succeeded (test)") {
			return "not yet: " + out
		}
		return ""
	})
	tw.Say("streamer", "Fan", "!horn")
	eventually(t, "the horn", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.horn"); strings.Count(out, "succeeded") < 2 {
			return out
		}
		return ""
	})
	stop()
	if got := heard(); len(got) != 2 || got[0] != "play air-horn 20" || got[1] != "play air-horn 60" {
		t.Fatalf("heard %q", got)
	}

	// A crash: the library stays.
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	if out, _, _ := cli(t, dir, "sounds"); !strings.Contains(out, "air-horn") {
		t.Fatalf("after a crash:\n%s", out)
	}
	if out, errOut, code := cli(t, dir, "sounds", "volume", "air-horn", "35"); code != 0 || !strings.Contains(out, "volume 35") {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "sounds", "delete", "air-horn"); code != 0 {
		t.Fatal(out, errOut)
	}
}

// One of several sounds, picked at random each time, through real chat.
func TestOneOfSeveralSoundsThroughChat(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	files := t.TempDir()
	for _, name := range []string{"airhorn", "chime", "boing"} {
		f := writeFile(t, files, name+".wav", "RIFF\x24\x00\x00\x00WAVE"+name+strings.Repeat("\x00", 64))
		if out, errOut, code := cli(t, dir, "sounds", "add", f); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	heard, stop := fakeWindow(t, e)
	defer stop()
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return "not yet: " + out
		}
		return ""
	})
	f := writeFile(t, t.TempDir(), "horn.json", `{"schema":"streamgremlin.workflow/v1","id":"chat.horn","name":"Horn",
		"triggers":[{"type":"chat.command.received","where":{"command":"horn"}}],
		"steps":[{"action":"sounds.play","with":{"sound":{"one_of":["airhorn","chime","boing"]}}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.horn")
	for range 15 {
		tw.Say("streamer", "Fan", "!horn")
	}
	eventually(t, "15 sounds", func() string {
		if n := len(heard()); n < 15 {
			return fmt.Sprintf("heard %d", n)
		}
		return ""
	})
	seen := map[string]bool{}
	for _, h := range heard() {
		name := strings.Fields(h)[1]
		if name != "airhorn" && name != "chime" && name != "boing" {
			t.Fatalf("played %q", h)
		}
		seen[name] = true
	}
	if len(seen) < 2 {
		t.Fatalf("always the same: %v", seen)
	}
}
