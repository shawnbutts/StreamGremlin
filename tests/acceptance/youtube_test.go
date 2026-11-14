//go:build acceptance

package acceptance

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
func TestYouTubeSignInThroughTheCLI(t *testing.T) {
	yt := mockyoutube.New()
	yt.RequireSecret("GOCSPX-acceptance-secret")
	ytSrv := httptest.NewServer(yt)
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
		!strings.Contains(out, "about 3 of 10000 units") {
		t.Fatal(out)
	}

	// A restart checks both with Google again.
	e.kill()
	startEngine(t, dir, "-youtube-url", ytSrv.URL)
	eventually(t, "signed in after a restart", func() string {
		out, _, _ := cli(t, dir, "youtube")
		if strings.Count(out, "signed in as") != 2 || !strings.Contains(out, "about 3 of") {
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
