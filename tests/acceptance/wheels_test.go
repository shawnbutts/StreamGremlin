//go:build acceptance

package acceptance

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Wheels through the real engine, CLI and mocktwitch: a wheel overlay
// opened as OBS would; a wheel added from the CLI with its odds; a CLI spin
// shown on the overlay, waiting until the wheel stops; a chat command whose
// workflow spins and replies with the result, never before the wheel
// stops; and the record checks out with "wheels verify".
func TestWheelsThroughTheCLIAndChat(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if tw.Subscribed("streamer") == 0 {
			return "not reading chat"
		}
		return ""
	})
	out, errOut, code := cli(t, dir, "overlays", "add", "-wheel", "wheel")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	link := linkIn(t, out)
	page := openPage(t, link)
	// A sound as the wheel stops, played by the overlay through its link.
	tada := writeFile(t, t.TempDir(), "tada.wav", "RIFF\x24\x00\x00\x00WAVEfmt "+strings.Repeat("\x00", 64))
	if out, errOut, code := cli(t, dir, "sounds", "add", tada); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "overlays", "look", "wheel", "-end-sound", "tada", "-end-volume", "70", "-cheer-sound", "tada"); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "the sounds in the overlay's look", func() string { return page.has("hello", `"cheer_sound":"tada"`) })
	if res, err := http.Get(link + "sound/tada"); err != nil || res.StatusCode != 200 {
		t.Fatal("the overlay can't fetch its sound:", err)
	} else {
		_ = res.Body.Close()
	}

	out, errOut, code = cli(t, dir, "wheels", "add", "next-game", "-title", "Next game", "-choice", "Mario:2", "-choice", "Zelda",
		"-choice", "Tetris:1:#ff8800", "-seconds", "2")
	if code != 0 || !strings.Contains(out, "Mario 50%, Zelda 25%, Tetris 25%") {
		t.Fatal(out, errOut)
	}
	started := time.Now()
	out, errOut, code = cli(t, dir, "wheels", "spin", "next-game")
	if code != 0 || !strings.Contains(out, "It landed on") {
		t.Fatal(out, errOut)
	}
	if took := time.Since(started); took < 2*time.Second {
		t.Fatalf("the spin settled after %v, before the wheel could stop", took)
	}
	eventually(t, "the spin on the overlay", func() string { return page.has("spin", `"title":"Next game"`) })
	id := regexp.MustCompile(`Spin (\S+)`).FindStringSubmatch(out)[1]
	if out, _, code := cli(t, dir, "wheels", "verify", id); code != 0 || !strings.Contains(out, "✓ Number") {
		t.Fatal(out)
	}

	// !spin in chat: the workflow spins and replies once the wheel stops.
	wf := writeFile(t, t.TempDir(), "spin.json", `{"schema":"streamgremlin.workflow/v1","id":"chat.spin","name":"Spin the wheel",
		"triggers":[{"type":"chat.command.received","where":{"command":"spin"}}],
		"steps":[{"id":"s","action":"wheels.spin","with":{"wheel":"next-game"}},
			{"action":"chat.send","with":{"message":"The wheel landed on ${steps.s.output.label}!"}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", wf); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.spin")
	asked := time.Now()
	tw.Say("streamer", "ann", "!spin")
	var answered time.Time
	eventually(t, "the reply", func() string {
		for _, m := range tw.ChatLog() {
			if strings.HasPrefix(m.Text, "The wheel landed on ") {
				answered = time.Now()
				return ""
			}
		}
		return "no reply yet"
	})
	if wait := answered.Sub(asked); wait < 2*time.Second {
		t.Fatalf("chat heard the result %v after !spin, before the wheel could stop", wait)
	}
	if out, _, _ := cli(t, dir, "wheels"); strings.Count(out, "Next game: ") != 2 {
		t.Fatalf("wheels:\n%s", out)
	}
}
