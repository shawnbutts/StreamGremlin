//go:build acceptance

package acceptance

import (
	"bufio"
	"bytes"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
)

func startMockTwitch(t *testing.T) (*mocktwitch.Server, string) {
	t.Helper()
	m := mocktwitch.New()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	m.SetBase(srv.URL)
	return m, srv.URL
}

// signInThroughCLI runs `twitch sign-in`, enters its code at the mock as
// login, and waits for the CLI to report the result.
func signInThroughCLI(t *testing.T, dir string, m *mocktwitch.Server, login string, extra ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"-data-dir", dir, "twitch", "sign-in"}, extra...)...)
	cmd.Env = cleanEnv()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	sc := bufio.NewScanner(stdout)
	codeRe := regexp.MustCompile(`enter the code ([A-Z]+)`)
	for sc.Scan() {
		out.WriteString(sc.Text() + "\n")
		if m1 := codeRe.FindStringSubmatch(sc.Text()); m1 != nil {
			if err := m.Approve(m1[1], login); err != nil {
				t.Fatal(err)
			}
		}
	}
	err := cmd.Wait()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return out.String() + stderr.String(), code
}

func TestTwitchSignInThroughTheCLI(t *testing.T) {
	m, url := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", url, "-debug")

	out, _, _ := cli(t, dir, "twitch")
	if !strings.Contains(out, "Client ID:  none") || !strings.Contains(out, "not the real Twitch") {
		t.Fatalf("status:\n%s", out)
	}
	if _, errOut, code := cli(t, dir, "twitch", "sign-in"); code != 2 || !strings.Contains(errOut, "Client ID") {
		t.Fatalf("sign-in without a client id: %d %s", code, errOut)
	}
	if out, errOut, code := cli(t, dir, "twitch", "client-id", "abcdefghij0123456789"); code != 0 {
		t.Fatalf("client-id: %d %s%s", code, out, errOut)
	}
	if out, code := signInThroughCLI(t, dir, m, "GremlinStreamer"); code != 0 || !strings.Contains(out, "Signed in as gremlinstreamer") {
		t.Fatalf("sign-in: %d\n%s", code, out)
	}
	// The bot can't be the same account, and can be another.
	if out, code := signInThroughCLI(t, dir, m, "gremlinstreamer", "-bot"); code != 1 || !strings.Contains(out, "your own account") {
		t.Fatalf("bot as streamer: %d\n%s", code, out)
	}
	if out, code := signInThroughCLI(t, dir, m, "GremlinBot", "-bot"); code != 0 || !strings.Contains(out, "private browser window") {
		t.Fatalf("bot: %d\n%s", code, out)
	}

	// A crash, then a restart: both sign-ins are back, checked with Twitch.
	e.kill()
	e = startEngine(t, dir, "-twitch-auth-url", url)
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _, _ = cli(t, dir, "twitch")
		if strings.Contains(out, "signed in as gremlinstreamer") && strings.Contains(out, "signed in as gremlinbot") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after restart:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// No token is readable anywhere but the encrypted secrets file.
	tokens := append(m.Tokens("gremlinstreamer"), m.Tokens("gremlinbot")...)
	if len(tokens) < 4 {
		t.Fatalf("expected live tokens, got %d", len(tokens))
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(path)
		for _, tok := range tokens {
			if bytes.Contains(b, []byte(tok)) {
				t.Errorf("a Twitch token is readable in %s", path)
			}
		}
		return nil
	})
	code, body := call(t, e, "GET", "/v1/connections", e.token, "", nil)
	for _, tok := range tokens {
		if strings.Contains(body, tok) || strings.Contains(e.stderr.String(), tok) {
			t.Fatal("a token in an API answer or stderr")
		}
	}
	if code != 200 {
		t.Fatal(code)
	}

	// Removing the app at Twitch ends the session at the next check (a restart).
	m.Disconnect("gremlinbot")
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", url)
	deadline = time.Now().Add(10 * time.Second)
	for {
		out, _, _ = cli(t, dir, "twitch")
		if strings.Contains(out, "Bot:        needs signing in again") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after disconnect:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if out, _, code := cli(t, dir, "twitch", "sign-out"); code != 0 || !strings.Contains(out, "Signed out") {
		t.Fatalf("sign-out: %d %s", code, out)
	}
	if m.ValidTokens("gremlinstreamer") != 0 {
		t.Fatal("sign-out didn't revoke the token")
	}
	if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "You:        signed out") {
		t.Fatalf("after sign-out:\n%s", out)
	}
}

func TestTwitchTestServerMustBeLocal(t *testing.T) {
	cmd := exec.Command(bin, "-data-dir", t.TempDir(), "engine", "-addr", "127.0.0.1:0", "-twitch-auth-url", "http://evil.example:80")
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "must be on this computer") {
		t.Fatalf("%v\n%s", err, out)
	}
}
