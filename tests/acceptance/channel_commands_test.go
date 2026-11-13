//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// The channel commands through the real engine, CLI and mocktwitch
// (D-086): !uptime offline and live, !title and !game changed by you and
// only read for a viewer, !clip refused offline and posted live, !marker,
// and !followage for yourself and for someone else.
func TestChannelCommandsThroughChat(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	for _, id := range []string{"title", "game", "uptime", "followage", "clip", "marker"} {
		out, _, _ := cli(t, dir, "templates", "show", id)
		f := writeFile(t, t.TempDir(), id+".json", out)
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(id, out, errOut)
		}
	}
	tw.UpdateChannel("streamer", "Old title", "Just Chatting")
	said := func(text string) bool {
		for _, m := range tw.ChatLog() {
			if strings.Contains(m.Text, text) {
				return true
			}
		}
		return false
	}
	ask := func(who, text, want string) {
		t.Helper()
		tw.Say("streamer", who, text)
		eventually(t, want, func() string {
			if !said(want) {
				return "not said"
			}
			return ""
		})
	}

	ask("fan", "!uptime", "We're not live right now.")
	ask("fan", "!title", "Title: Old title")
	ask("streamer", "!title Building a bot, live", "Title changed to: Building a bot, live")
	if title, _ := tw.Channel("streamer"); title != "Building a bot, live" {
		t.Fatalf("title %q", title)
	}
	edits := tw.ChannelEdits()
	ask("fan", "!title Hacked", "Title: Building a bot, live")
	if tw.ChannelEdits() != edits {
		t.Fatal("a viewer changed the title")
	}
	ask("streamer", "!game minecraft", "Category changed to Minecraft.")
	if _, cat := tw.Channel("streamer"); cat != "Minecraft" {
		t.Fatalf("category %q", cat)
	}
	ask("fan", "!game", "We're in Minecraft.")
	ask("streamer", "!clip", "couldn't make a clip: you're not live")

	tw.GoLive("streamer")
	ask("fan", "!uptime", "We've been live for less than a minute.")
	ask("streamer", "!clip Nice jump", "Clipped! https://clips.twitch.tv/Clip")
	if c := tw.Clips(); len(c) != 1 || c[0].Title != "Nice jump" {
		t.Fatalf("%+v", c)
	}
	ask("streamer", "!marker boss down", "Marked at 0:")
	if m := tw.Markers(); len(m) != 1 || m[0].Description != "boss down" {
		t.Fatalf("%+v", m)
	}

	tw.SetFollower("streamer", "fan", time.Now().Add(-40*24*time.Hour))
	ask("fan", "!followage", "fan has followed for 1 month.")
	tw.Say("streamer", "lurker", "hi")
	ask("fan", "!followage @lurker", "lurker doesn't follow yet.")
}
