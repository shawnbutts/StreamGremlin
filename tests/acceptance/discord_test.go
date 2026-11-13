//go:build acceptance

package acceptance

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/mockdiscord"
)

// Discord through the real engine, CLI, mocktwitch and mockdiscord (D-089):
// a webhook added from standard input and never shown or logged, a test
// post, then the three templates: going live posts @everyone with the
// stream's card, !clip's clip is posted, and going offline posts the sum.
func TestDiscordThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dc := mockdiscord.New()
	dcSrv := httptest.NewServer(dc)
	t.Cleanup(dcSrv.Close)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL, "-discord-api-url", dcSrv.URL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}

	id, token := dc.AddWebhook("Stream announcements")
	link := "https://discord.com/api/webhooks/" + id + "/" + token
	add := exec.Command(bin, "-data-dir", dir, "discord", "add", "announcements")
	add.Env = cleanEnv()
	add.Stdin = strings.NewReader(link + "\n")
	out, err := add.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `Discord's webhook "Stream announcements"`) || strings.Contains(string(out), token) {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if out, _, _ := cli(t, dir, "discord"); !strings.Contains(out, "announcements") || strings.Contains(out, token) {
		t.Fatal(out)
	}
	if out, errOut, code := cli(t, dir, "discord", "test", "announcements"); code != 0 || !strings.Contains(out, "Posted") {
		t.Fatal(out, errOut)
	}
	if p := dc.Posts(); len(p) != 1 || p[0].Parse == nil || len(p[0].Parse) != 0 {
		t.Fatalf("%+v", p)
	}
	if _, _, code := cli(t, dir, "discord", "test", "nope"); code == 0 {
		t.Fatal("an unknown webhook posted")
	}

	for _, tid := range []string{"discord-live", "discord-clip", "discord-ended", "clip"} {
		out, _, _ := cli(t, dir, "templates", "show", tid)
		out = strings.ReplaceAll(out, `"webhook": ""`, `"webhook": "announcements"`)
		f := writeFile(t, t.TempDir(), tid+".json", out)
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(tid, out, errOut)
		}
	}
	tw.UpdateChannel("streamer", "Building a bot", "Software and Game Development")
	posted := func(want string) func() string {
		return func() string {
			for _, p := range dc.Posts() {
				all := p.Content
				for _, e := range p.Embeds {
					all += " " + strings.Join([]string{str(e["title"]), str(e["description"])}, " ")
				}
				if strings.Contains(all, want) {
					return ""
				}
			}
			return "not posted"
		}
	}

	tw.GoLive("streamer")
	eventually(t, "live post", posted("@everyone I'm live on Twitch! Come hang out: https://www.twitch.tv/streamer"))
	var live mockdiscord.Post
	for _, p := range dc.Posts() {
		if strings.HasPrefix(p.Content, "@everyone") {
			live = p
		}
	}
	if len(live.Parse) != 1 || live.Parse[0] != "everyone" || len(live.Embeds) != 1 || live.Embeds[0]["title"] != "Building a bot" ||
		live.Embeds[0]["description"] != "Playing Software and Game Development" {
		t.Fatalf("%+v", live)
	}

	tw.Follow("streamer", "newfan")
	tw.Say("streamer", "newfan", "hello!")
	tw.Say("streamer", "streamer", "!clip")
	eventually(t, "clip post", func() string {
		if posted("New clip by streamer: https://clips.twitch.tv/")() == "" {
			return ""
		}
		var b strings.Builder
		for _, p := range dc.Posts() {
			b.WriteString(p.Content + " | ")
		}
		for _, m := range tw.ChatLog() {
			b.WriteString(m.Text + " / ")
		}
		return b.String()
	})

	tw.GoOffline("streamer")
	eventually(t, "summary", posted("New followers: 1"))
	eventually(t, "chatters", posted("Chatters: 1"))

	logs, _ := filepath.Glob(filepath.Join(dir, "logs", "*"))
	for _, f := range logs {
		b, _ := os.ReadFile(f) //nolint:gosec // the test's own data folder
		if strings.Contains(string(b), token) {
			t.Fatalf("the webhook's token is in %s", f)
		}
	}
}

func str(v any) string { s, _ := v.(string); return s }
