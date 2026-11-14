//go:build acceptance

package acceptance

import (
	"bufio"
	"bytes"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/mockyoutube"
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
// repeating a message; YouTube chat makes no first words yet; the broadcast ending goes back to looking.
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
	if out := chat(); strings.Count(out, "hello from YouTube") != 1 || strings.Count(out, "after the drop") != 1 {
		t.Fatalf("a message twice:\n%s", out)
	}
	if yt.LastPageToken() == "" {
		t.Fatal("the restart didn't resume from the saved page token")
	}
	// Log only: no first words for the YouTube viewer.
	if out := chat(); strings.Contains(out, "chat.first_words.received  (youtube)") {
		t.Fatalf("first words for YouTube chat:\n%s", out)
	}

	// The broadcast ends: back to looking.
	yt.EndBroadcast(b.ID)
	eventually(t, "looking again", func() string {
		out, _, _ := cli(t, dir, "youtube")
		if !strings.Contains(out, "Chat:           not live on YouTube") || !strings.Contains(out, "Acceptance stream") {
			return out
		}
		return ""
	})
}
