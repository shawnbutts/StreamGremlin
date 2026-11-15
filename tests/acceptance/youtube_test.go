//go:build acceptance

package acceptance

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockyoutube"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

const ytClientID = "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"

// youtubeSignIn runs `youtube sign-in` and acts as the browser: it opens
// the printed link at mockyoutube, picks channel, and follows Google's
// redirect back to the engine.
func youtubeSignIn(t *testing.T, dir, channel string, extra ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"-data-dir", dir, "youtube", "sign-in"}, extra...)...)
	cmd.Env = cleanEnv()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		out.WriteString(line + "\n")
		if strings.HasPrefix(line, "http://") {
			u, err := url.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			q.Set("mock_channel", channel)
			u.RawQuery = q.Encode()
			resp, err := http.Get(u.String())
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		}
	}
	err := cmd.Wait()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return out.String() + stderr.String(), code
}

// YouTube sign-in through the real engine, CLI and mockyoutube (M6 slice
// 1, D-096): the streamer's own Google project with its secret from
// standard input, signing in as you and the bot, the bot refused as your
// own channel, the quota counted, a restart, and signing out. The secret
// and tokens never appear in output or logs.
// quotaUsed: 1 unit for each sign-in's channel, and 1 for each look for
// a live broadcast (a sign-in of your channel, and a start, look at once).
var quotaUsed = regexp.MustCompile(`about \d+ of 10000 units used today`)

func TestYouTubeSignInThroughTheCLI(t *testing.T) {
	yt := mockyoutube.New()
	yt.RequireSecret("GOCSPX-acceptance-secret")
	ytSrv := mockyoutube.Serve(yt)
	t.Cleanup(ytSrv.Close)
	dir := t.TempDir()
	e := startEngine(t, dir, "-youtube-url", ytSrv.URL)

	out, _, _ := cli(t, dir, "youtube")
	if !strings.Contains(out, "Google project: none") || !strings.Contains(out, "not the real Google") ||
		!strings.Contains(out, "needs a Google project's client ID") {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "youtube", "sign-in"); code != 2 || !strings.Contains(errOut, "client ID") {
		t.Fatal(code, errOut)
	}
	if _, errOut, code := cli(t, dir, "youtube", "client", "not-an-id"); code != 2 || !strings.Contains(errOut, "googleusercontent") {
		t.Fatal(code, errOut)
	}
	set := exec.Command(bin, "-data-dir", dir, "youtube", "client", ytClientID, "-secret")
	set.Env = cleanEnv()
	set.Stdin = strings.NewReader("GOCSPX-acceptance-secret\n")
	if b, err := set.CombinedOutput(); err != nil || !strings.Contains(string(b), "with its client secret") || strings.Contains(string(b), "GOCSPX") {
		t.Fatalf("%v\n%s", err, b)
	}

	if out, code := youtubeSignIn(t, dir, "UCstreamer"); code != 0 || !strings.Contains(out, "Signed in as Streamer.") {
		t.Fatal(code, out)
	}
	if out, code := youtubeSignIn(t, dir, "UCstreamer", "-bot"); code != 1 || !strings.Contains(out, "your own channel") {
		t.Fatal(code, out)
	}
	if out, code := youtubeSignIn(t, dir, "UCbot", "-bot"); code != 0 || !strings.Contains(out, "Signed in as Bot.") {
		t.Fatal(code, out)
	}
	out, _, _ = cli(t, dir, "youtube")
	if !strings.Contains(out, "signed in as Streamer (@streamer)") || !strings.Contains(out, "signed in as Bot") ||
		!quotaUsed.MatchString(out) {
		t.Fatal(out)
	}

	// A restart checks both with Google again.
	e.kill()
	startEngine(t, dir, "-youtube-url", ytSrv.URL)
	eventually(t, "signed in after a restart", func() string {
		out, _, _ := cli(t, dir, "youtube")
		if strings.Count(out, "signed in as") != 2 || !quotaUsed.MatchString(out) {
			return out
		}
		return ""
	})

	if out, _, code := cli(t, dir, "youtube", "sign-out", "-bot"); code != 0 || !strings.Contains(out, "Signed out") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "youtube"); !strings.Contains(out, "Bot:            signed out") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "secrets"); strings.Contains(out, "GOCSPX") || !strings.Contains(out, "youtube/client-secret") {
		t.Fatal(out)
	}

	logs, _ := filepath.Glob(filepath.Join(dir, "logs", "*"))
	for _, f := range logs {
		b, _ := os.ReadFile(f) //nolint:gosec // the test's own data folder
		if bytes.Contains(b, []byte("GOCSPX")) || bytes.Contains(b, []byte("access_token")) || bytes.Contains(b, []byte("code_verifier")) {
			t.Fatalf("%s holds a secret", f)
		}
	}
}

// YouTube chat through the real engine, CLI and mockyoutube (M6 slice 2,
// D-097): not live, then Find my stream reads the broadcast's chat into
// the event log; a dropped stream and a restart resume without missing or
// repeating a message; the broadcast ending goes back to looking.
func TestYouTubeChatThroughTheCLI(t *testing.T) {
	yt := mockyoutube.New()
	ytSrv := mockyoutube.Serve(yt)
	t.Cleanup(ytSrv.Close)
	dir := t.TempDir()
	e := startEngine(t, dir, "-youtube-url", ytSrv.URL)
	if _, errOut, code := cli(t, dir, "youtube", "client", ytClientID); code != 0 {
		t.Fatal(errOut)
	}
	if out, code := youtubeSignIn(t, dir, "UCstreamer"); code != 0 {
		t.Fatal(code, out)
	}
	if out, _, code := cli(t, dir, "youtube", "find"); code != 0 || !strings.Contains(out, "Chat: not live on YouTube") {
		t.Fatal(code, out)
	}
	b := yt.GoLive("UCstreamer", "Acceptance stream")
	if out, _, code := cli(t, dir, "youtube", "find"); code != 0 || !strings.Contains(out, "reading chat of “Acceptance stream”") {
		t.Fatal(code, out)
	}
	yt.Say(b.ChatID, "UCviewer", "YouTube Viewer", "hello from YouTube")
	chat := func() string {
		out, _, _ := cli(t, dir, "events", "-types", "chat.*")
		return out
	}
	has := func(n int, text string) func() string {
		return func() string {
			if out := chat(); strings.Count(out, text) != n {
				return out
			}
			return ""
		}
	}
	eventually(t, "the YouTube message", has(1, "chat.message.received  (youtube)  YouTube Viewer: hello from YouTube"))

	// A dropped stream resumes where it was.
	yt.DropStreams()
	yt.Say(b.ChatID, "UCviewer", "YouTube Viewer", "after the drop")
	eventually(t, "the message after a drop", has(1, "after the drop"))

	// A restart resumes too: messages sent while the engine was down arrive, once.
	e.kill()
	yt.Say(b.ChatID, "UCviewer", "YouTube Viewer", "while down")
	startEngine(t, dir, "-youtube-url", ytSrv.URL)
	eventually(t, "the message sent while down", has(1, "while down"))
	if out := chat(); strings.Count(out, "chat.message.received  (youtube)  YouTube Viewer: hello from YouTube") != 1 ||
		strings.Count(out, "after the drop") != 1 {
		t.Fatalf("a message twice:\n%s", out)
	}
	if yt.LastPageToken() == "" {
		t.Fatal("the restart didn't resume from the saved page token")
	}

	// The broadcast ends: back to looking.
	yt.EndBroadcast(b.ID)
	eventually(t, "looking again", func() string {
		out, _, _ := cli(t, dir, "youtube")
		if !strings.Contains(out, "Chat:           not live on YouTube") || !strings.Contains(out, "Acceptance stream") {
			return "not yet: " + out
		}
		return ""
	})
}

// One combined chat through the real engine, CLI and mockyoutube (M6
// slice 3, D-098): a YouTube viewer's command is answered on YouTube, as
// a reply, and the answer's echo starts nothing; their first words are
// found; the CLI sends to YouTube and to both (Twitch isn't signed in, so
// it says so and YouTube still gets it).
func TestYouTubeCombinedChatThroughTheCLI(t *testing.T) {
	yt := mockyoutube.New()
	ytSrv := mockyoutube.Serve(yt)
	t.Cleanup(ytSrv.Close)
	dir := t.TempDir()
	startEngine(t, dir, "-youtube-url", ytSrv.URL)
	if _, errOut, code := cli(t, dir, "youtube", "client", ytClientID); code != 0 {
		t.Fatal(errOut)
	}
	if out, code := youtubeSignIn(t, dir, "UCstreamer"); code != 0 {
		t.Fatal(code, out)
	}
	files := t.TempDir()
	f := writeFile(t, files, "hello.json", `{"schema":"streamgremlin.workflow/v1","id":"hello","name":"Hello",
		"triggers":[{"type":"chat.command.received","where":{"command":"hello"}}],
		"steps":[{"action":"chat.send","with":{"message":"Hi ${event.actor.display_name}!","reply_to":"${event.payload.message_id}"}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "workflows", "on", "hello"); code != 0 {
		t.Fatal(out, errOut)
	}
	b := yt.GoLive("UCstreamer", "Combined")
	if out, _, code := cli(t, dir, "youtube", "find"); code != 0 || !strings.Contains(out, "reading chat") {
		t.Fatal(code, out)
	}
	yt.Say(b.ChatID, "UCviewer", "YouTube Viewer", "!hello")
	eventually(t, "the answer on YouTube", func() string {
		for _, m := range yt.Sent() {
			if m.Text == "@YouTube Viewer Hi YouTube Viewer!" && m.Channel == "UCstreamer" {
				return ""
			}
		}
		return fmt.Sprintf("%+v", yt.Sent())
	})
	chat := func() string {
		out, _, _ := cli(t, dir, "events", "-types", "chat.*")
		return out
	}
	eventually(t, "first words and the answer stored as sent", func() string {
		out := chat()
		if !strings.Contains(out, "chat.first_words.received  (youtube)  YouTube Viewer") ||
			!strings.Contains(out, "chat.message.sent  (youtube)") {
			return "not yet: " + out
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	if n := len(yt.Sent()); n != 1 {
		t.Fatalf("the workflow answered %d times: %+v", n, yt.Sent())
	}

	if out, errOut, code := cli(t, dir, "chat", "send", "-to", "youtube", "from", "the", "cli"); code != 0 ||
		!strings.Contains(out, "Sent to YouTube as Streamer.") {
		t.Fatal(code, out, errOut)
	}
	out, errOut, code := cli(t, dir, "chat", "send", "-to", "both", "to", "both")
	if code != 1 || !strings.Contains(out, "Sent to YouTube") || !strings.Contains(errOut, "Not sent to Twitch") {
		t.Fatal(code, out, errOut)
	}
	if out, _, _ := cli(t, dir, "youtube"); !quotaUsed.MatchString(out) {
		t.Fatal(out)
	}
}

// YouTube's events through the real engine, CLI and mockyoutube (M6
// slice 4, D-099): going live, a Super Chat thanked on YouTube by a
// workflow, a new member in the event log, and going offline.
func TestYouTubeEventsThroughTheCLI(t *testing.T) {
	yt := mockyoutube.New()
	ytSrv := mockyoutube.Serve(yt)
	t.Cleanup(ytSrv.Close)
	dir := t.TempDir()
	startEngine(t, dir, "-youtube-url", ytSrv.URL)
	if _, errOut, code := cli(t, dir, "youtube", "client", ytClientID); code != 0 {
		t.Fatal(errOut)
	}
	if out, code := youtubeSignIn(t, dir, "UCstreamer"); code != 0 {
		t.Fatal(code, out)
	}
	files := t.TempDir()
	f := writeFile(t, files, "sc.json", `{"schema":"streamgremlin.workflow/v1","id":"sc","name":"Thank Super Chats",
		"triggers":[{"type":"monetization.super_chat.received"}],
		"steps":[{"action":"chat.send","with":{"message":"Thanks for the ${event.payload.amount_display}, ${event.payload.from}!"}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "workflows", "on", "sc"); code != 0 {
		t.Fatal(out, errOut)
	}
	b := yt.GoLive("UCstreamer", "Events stream")
	if out, _, code := cli(t, dir, "youtube", "find"); code != 0 || !strings.Contains(out, "reading chat") {
		t.Fatal(code, out)
	}
	yt.Add(b.ChatID, livechat.Message{Author: livechat.Author{ChannelID: "UCfan", DisplayName: "Big Fan"},
		Snippet: livechat.Snippet{Type: livechat.TypeSuperChat, SuperChat: &livechat.SuperChat{AmountMicros: 5_000_000, Currency: "USD",
			AmountDisplayString: "$5.00", UserComment: "love it"}}})
	yt.Add(b.ChatID, livechat.Message{Author: livechat.Author{ChannelID: "UCnew", DisplayName: "New Member"},
		Snippet: livechat.Snippet{Type: livechat.TypeNewSponsor, NewSponsor: &livechat.NewSponsor{MemberLevelName: "Gold"}}})
	eventually(t, "the thanks on YouTube", func() string {
		for _, m := range yt.Sent() {
			if m.Text == "Thanks for the $5.00, Big Fan!" {
				return ""
			}
		}
		return fmt.Sprintf("%+v", yt.Sent())
	})
	all := func() string {
		out, _, _ := cli(t, dir, "events", "-types", "monetization.*,broadcast.*")
		return out
	}
	eventually(t, "the events", func() string {
		out := all()
		if !strings.Contains(out, "monetization.super_chat.received  (youtube)  Big Fan") ||
			!strings.Contains(out, "monetization.subscription.started  (youtube)  New Member") ||
			strings.Count(out, "broadcast.channel.online  (youtube)") != 1 {
			return out
		}
		return ""
	})
	yt.EndBroadcast(b.ID)
	eventually(t, "offline", func() string {
		if out := all(); strings.Count(out, "broadcast.channel.offline  (youtube)") != 1 {
			return out
		}
		return ""
	})
}

// Moderating YouTube through the real engine, CLI and mockyoutube (M6
// slice 5, D-100): a timeout undone by the ban id YouTube gave, an unban
// refused for a ban not made here, Twitch-only actions refused, and a chat
// filter deleting a link on YouTube by default, shown as deleted.
func TestYouTubeModerationThroughTheCLI(t *testing.T) {
	yt := mockyoutube.New()
	ytSrv := mockyoutube.Serve(yt)
	t.Cleanup(ytSrv.Close)
	dir := t.TempDir()
	e := startEngine(t, dir, "-youtube-url", ytSrv.URL)
	if _, errOut, code := cli(t, dir, "youtube", "client", ytClientID); code != 0 {
		t.Fatal(errOut)
	}
	if out, code := youtubeSignIn(t, dir, "UCstreamer"); code != 0 {
		t.Fatal(code, out)
	}
	b := yt.GoLive("UCstreamer", "Mod stream")
	if out, _, code := cli(t, dir, "youtube", "find"); code != 0 || !strings.Contains(out, "reading chat") {
		t.Fatal(code, out)
	}
	ok := func(want string, args ...string) {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
	}
	ok("Timed out UCspam for 10m0s.", "mod", "-youtube", "timeout", "UCspam", "10m")
	if !yt.Banned("UCspam") {
		t.Fatal("not timed out")
	}
	ok("Unbanned UCspam.", "mod", "-youtube", "unban", "UCspam")
	if yt.Banned("UCspam") {
		t.Fatal("still timed out")
	}
	if _, errOut, code := cli(t, dir, "mod", "-youtube", "unban", "UCspam"); code == 0 || !strings.Contains(errOut, "YouTube Studio") {
		t.Fatal("an unban without a ban from here:", code, errOut)
	}
	if _, errOut, code := cli(t, dir, "mod", "-youtube", "warn", "UCspam", "hi"); code != 2 || !strings.Contains(errOut, "YouTube can't warn") {
		t.Fatal(code, errOut)
	}
	ok("UCspam", "mod", "-youtube", "history", "UCspam")

	// A filter set to time out deletes on YouTube unless told otherwise.
	body := `{"on":true,
		"words":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"list":[]},
		"links":{"on":true,"action":"timeout","seconds":600,"exempt_vips":true,"exempt_subscribers":false,"allowed":["twitch.tv"]},
		"emotes":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"max":10},
		"caps":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"min_letters":10,"percent":70},
		"symbols":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"min_length":15,"percent":50},
		"repeats":{"on":false,"action":"delete","seconds":0,"exempt_vips":true,"exempt_subscribers":false,"times":3}}`
	if code, out := call(t, e, "PUT", "/v1/filters", e.token, body, nil); code != 200 {
		t.Fatal(code, out)
	}
	msg := yt.Say(b.ChatID, "UClinker", "Linker", "cheap followers at bit.ly/x")
	eventually(t, "the link deleted on YouTube", func() string {
		if d := yt.Deleted(); len(d) != 1 || d[0] != msg {
			return fmt.Sprintf("%v", d)
		}
		return ""
	})
	if yt.Banned("UClinker") {
		t.Fatal("timed out on YouTube without being asked to")
	}
	eventually(t, "the deletion in the event log", func() string {
		if out, _, _ := cli(t, dir, "events", "-types", "chat.message.deleted"); !strings.Contains(out, "chat.message.deleted  (youtube)") {
			return "not yet: " + out
		}
		return ""
	})
	ok("Linker", "filters")
}

// YouTube viewers and points through the real engine, CLI, mocktwitch and
// mockyoutube (M6 slice 6, D-101): YouTube chat earns, !link proves one
// person's two accounts in chat, the balances join, @name finds them once
// linked (and refuses to guess before), and unlinking leaves the points on
// Twitch.
func TestYouTubeViewersAndLinkThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	yt := mockyoutube.New()
	ytSrv := mockyoutube.Serve(yt)
	t.Cleanup(ytSrv.Close)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL, "-youtube-url", ytSrv.URL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "youtube", "client", ytClientID); code != 0 {
		t.Fatal(errOut)
	}
	if out, code := youtubeSignIn(t, dir, "UCstreamer"); code != 0 {
		t.Fatal(code, out)
	}
	ok := func(want string, args ...string) string {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
		return out
	}
	ok("on", "points", "earning", "on")
	ok("", "points", "earning", "set", "chat=5", "every=0", "live-only=no")
	tmpl, _, _ := cli(t, dir, "templates", "show", "link")
	if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, t.TempDir(), "link.json", tmpl)); code != 0 {
		t.Fatal(out, errOut)
	}
	ok("", "workflows", "on", "chat.link")
	b := yt.GoLive("UCstreamer", "Both")
	ok("reading chat", "youtube", "find")
	eventually(t, "Twitch chat", func() string {
		if tw.Subscribed("streamer") == 0 {
			return "not yet"
		}
		return ""
	})
	tw.Say("streamer", "ann", "hello from Twitch")
	yt.Say(b.ChatID, "UCann", "@Ann", "hello from YouTube")
	eventually(t, "both viewers with points", func() string {
		out, _, _ := cli(t, dir, "viewers", "ann")
		if !regexp.MustCompile(`Twitch\s+5 points`).MatchString(out) || !regexp.MustCompile(`YouTube\s+5 points`).MatchString(out) {
			return out
		}
		return ""
	})
	// Before linking, @ann is two people: nobody is guessed.
	if _, errOut, code := cli(t, dir, "points", "give", "@ann", "1"); code == 0 || !strings.Contains(errOut, "someone else on YouTube") {
		t.Fatal("guessed:", code, errOut)
	}

	tw.Say("streamer", "ann", "!link @ann")
	var code string
	eventually(t, "a code on Twitch", func() string {
		for _, m := range tw.ChatLog() {
			if m := regexp.MustCompile(`type !link (\d{6}) in YouTube chat as @ann`).FindStringSubmatch(m.Text); m != nil {
				code = m[1]
				return ""
			}
		}
		return fmt.Sprintf("%+v", tw.ChatLog())
	})
	yt.Say(b.ChatID, "UCimpostor", "@Someone", "!link "+code) // someone else who read it: nothing
	yt.Say(b.ChatID, "UCann", "@Ann", "!link "+code)
	eventually(t, "linked, said on YouTube", func() string {
		for _, m := range yt.Sent() {
			if strings.Contains(m.Text, "Linked! ann on Twitch and @Ann on YouTube now share one points balance.") {
				return ""
			}
		}
		return fmt.Sprintf("%+v", yt.Sent())
	})
	for _, m := range yt.Sent() {
		if strings.Contains(m.Text, "@Someone") && strings.Contains(m.Text, "Linked") {
			t.Fatal("the impostor linked")
		}
	}
	// One shared balance, found by @ann now.
	ok("Linked to @Ann on YouTube", "viewers", "show", "@ann")
	ok("ann now has 120.", "points", "give", "@ann", "100")
	// Each account shows the one shared balance: 5 + 5 for hello, 5 + 5 for
	// !link, and the 100.
	shared := regexp.MustCompile(`(?m)^(ann|@Ann)\s+(Twitch|YouTube)\s+(\d+) points.*linked to`)
	eventually(t, "the shared balance", func() string {
		out, _, _ := cli(t, dir, "viewers", "ann")
		m := shared.FindAllStringSubmatch(out, -1)
		if len(m) != 2 || m[0][3] != m[1][3] || m[0][3] != "120" {
			return out
		}
		return ""
	})
	ok("Unlinked", "viewers", "unlink", "@ann")
	out := ok("", "viewers", "ann")
	if !regexp.MustCompile(`Twitch\s+120 points`).MatchString(out) || !regexp.MustCompile(`YouTube\s+0 points`).MatchString(out) ||
		strings.Contains(out, "linked to") {
		t.Fatal("after unlinking, the points stay on Twitch and YouTube starts again:", out)
	}
	ok("✓ Every change balances", "points", "audit")
}

// M6 slice 7 (D-102) through the real engine, CLI and mockyoutube: the
// viewer count and title from the minute check, changing the title, a new
// public subscriber as a follow, and a YouTube poll started, voted on and
// ended. (Later checks, changes in the count and active chatters run on
// minutes; their unit tests shorten them.)
func TestYouTubeStreamAndPollsThroughTheCLI(t *testing.T) {
	yt := mockyoutube.New()
	ytSrv := mockyoutube.Serve(yt)
	t.Cleanup(ytSrv.Close)
	dir := t.TempDir()
	startEngine(t, dir, "-youtube-url", ytSrv.URL)
	if _, errOut, code := cli(t, dir, "youtube", "client", ytClientID); code != 0 {
		t.Fatal(errOut)
	}
	if out, code := youtubeSignIn(t, dir, "UCstreamer"); code != 0 {
		t.Fatal(code, out)
	}
	ok := func(want string, args ...string) string {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
		return out
	}
	b := yt.GoLive("UCstreamer", "Friday")
	yt.SetViewers(b.ID, 37)
	yt.Subscribe("UCstreamer", "UCold", "Old Fan", b.Started.Add(-time.Hour))
	yt.Subscribe("UCstreamer", "UCnew", "New Fan", b.Started.Add(time.Second))
	ok("reading chat", "youtube", "find")
	eventually(t, "the viewer count", func() string {
		out, _, _ := cli(t, dir, "youtube")
		if !strings.Contains(out, "Viewers:        37") || !strings.Contains(out, "Title:          Friday") {
			return "not yet: " + out
		}
		return ""
	})
	// Only the subscriber since the broadcast started is a follow.
	eventually(t, "a YouTube subscriber as a follow", func() string {
		out, _, _ := cli(t, dir, "events", "-types", "audience.follow.created")
		if !strings.Contains(out, "New Fan") || strings.Contains(out, "Old Fan") {
			return "not yet: " + out
		}
		return ""
	})

	if _, errOut, code := cli(t, dir, "youtube", "title", "a", "<b>"); code == 0 || !strings.Contains(errOut, "< or >") {
		t.Fatal("a bad title:", code, errOut)
	}
	ok("Title: “Late night”", "youtube", "title", "Late", "night")
	if ups := yt.VideoUpdates(); len(ups) != 1 || ups[0].Title != "Late night" || ups[0].CategoryID == "" || len(ups[0].Tags) == 0 {
		t.Fatalf("%+v", ups)
	}
	ok("Late night", "events", "-types", "broadcast.channel.updated")

	ok("Started “Snack?”", "youtube", "poll", "Snack?", "Chips", "Fruit")
	if _, errOut, code := cli(t, dir, "youtube", "poll", "Again?", "a", "b"); code == 0 || !strings.Contains(errOut, "already running") {
		t.Fatal("a second poll:", code, errOut)
	}
	yt.Vote(b.ChatID, 0)
	yt.Vote(b.ChatID, 0)
	eventually(t, "votes", func() string {
		out, _, _ := cli(t, dir, "youtube")
		if !strings.Contains(out, "Chips 2, Fruit 0") {
			return "not yet: " + out
		}
		return ""
	})
	ok("Ended “Snack?”: Chips 2, Fruit 0", "youtube", "poll", "end")
	ok("Snack?", "events", "-types", "chat.poll.ended")
	if _, _, ok := yt.ActivePoll(b.ChatID); ok {
		t.Fatal("the poll is still running")
	}
}

// An add-on on both chats (M6 slice 8, D-103) through the real engine,
// mocktwitch and mockyoutube: Dice tray answers each viewer where they
// typed, and opening a round is said in both.
func TestAddOnOnEveryChat(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	yt := mockyoutube.New()
	ytSrv := mockyoutube.Serve(yt)
	t.Cleanup(ytSrv.Close)
	dir := t.TempDir()
	startEngine(t, dir, "-twitch-auth-url", twURL, "-youtube-url", ytSrv.URL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	if _, errOut, code := cli(t, dir, "youtube", "client", ytClientID); code != 0 {
		t.Fatal(errOut)
	}
	if out, code := youtubeSignIn(t, dir, "UCstreamer"); code != 0 {
		t.Fatal(code, out)
	}
	zipPath := filepath.Join(t.TempDir(), "dice-tray.zip")
	zipFolder(t, filepath.Join("..", "..", "examples", "plugins", "dice-tray"), zipPath)
	if out, errOut, code := cli(t, dir, "packages", "install", zipPath, "-yes", "-allow", "chat.send,events.publish,points.rounds"); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "packages", "on", "dice-tray"); code != 0 {
		t.Fatal(out, errOut)
	}
	files := t.TempDir()
	for _, id := range []string{"dice-tray/roll", "dice-tray/round"} {
		w, errOut, code := cli(t, dir, "templates", "show", id)
		if code != 0 {
			t.Fatal(errOut)
		}
		if out, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, "w.json", w)); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	cli(t, dir, "workflows", "on", "chat.dice-tray-roll")
	cli(t, dir, "workflows", "on", "chat.dice-tray-round")
	b := yt.GoLive("UCstreamer", "Both")
	if out, _, code := cli(t, dir, "youtube", "find"); code != 0 || !strings.Contains(out, "reading chat") {
		t.Fatal(out)
	}
	eventually(t, "Twitch chat", func() string {
		if tw.Subscribed("streamer") == 0 {
			return "not yet"
		}
		return ""
	})
	onTwitch := func(text string) bool {
		for _, m := range tw.ChatLog() {
			if strings.Contains(m.Text, text) {
				return true
			}
		}
		return false
	}
	onYouTube := func(text string) bool {
		for _, m := range yt.Sent() {
			if strings.Contains(m.Text, text) {
				return true
			}
		}
		return false
	}
	yt.Say(b.ChatID, "UCyoko", "@Yoko", "!roll")
	tw.Say("streamer", "Tess", "!roll")
	eventually(t, "each answered where they typed", func() string {
		if !onYouTube("Yoko rolled") || !onTwitch("Tess rolled") {
			return fmt.Sprintf("twitch %+v, youtube %+v", tw.ChatLog(), yt.Sent())
		}
		return ""
	})
	if onTwitch("Yoko rolled") || onYouTube("Tess rolled") {
		t.Fatalf("answered in the wrong chat: twitch %+v, youtube %+v", tw.ChatLog(), yt.Sent())
	}
	tw.Say("streamer", "streamer", "!diceround")
	eventually(t, "the round said in both", func() string {
		if !onYouTube("dice round is open") || !onTwitch("dice round is open") {
			return fmt.Sprintf("twitch %+v, youtube %+v", tw.ChatLog(), yt.Sent())
		}
		return ""
	})
	out, _, _ := cli(t, dir, "packages", "activity", "dice-tray")
	if !strings.Contains(out, "to YouTube: @Yoko rolled") && !strings.Contains(out, "to YouTube: Yoko rolled") || !strings.Contains(out, "to Twitch and YouTube: A dice round is open") {
		t.Fatalf("activity:\n%s", out)
	}
}
