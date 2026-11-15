//go:build acceptance

package acceptance

import (
	"bytes"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockobs"
)

func startMockOBS(t *testing.T) (*mockobs.Server, string) {
	t.Helper()
	m := mockobs.New()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestOBSThroughTheCLI(t *testing.T) {
	m, url := startMockOBS(t)
	m.SetPassword("obs-canary-pw-71c")
	dir := t.TempDir()
	e := startEngine(t, dir)

	run := func(want int, args ...string) string {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		if code != want {
			t.Fatalf("%v: %d\n%s%s", args, code, out, errOut)
		}
		return out + errOut
	}
	connect := exec.Command(bin, "-data-dir", dir, "obs", "connect", "-url", url, "-password-stdin")
	connect.Env = cleanEnv()
	connect.Stdin = strings.NewReader("obs-canary-pw-71c\n")
	out, err := connect.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "connected (OBS 31.0.0") || strings.Contains(string(out), "canary") {
		t.Fatalf("connect: %v\n%s", err, out)
	}
	if out := run(0, "obs", "scenes"); !strings.Contains(out, "▶ Starting Soon") || !strings.Contains(out, "Be Right Back") {
		t.Fatalf("scenes:\n%s", out)
	}
	run(0, "obs", "switch", "Just Chatting")
	run(0, "obs", "hide", "Just Chatting", "Webcam")
	if out := run(0, "obs", "sources", "Just Chatting"); !strings.Contains(out, "hidden  Webcam") {
		t.Fatalf("sources:\n%s", out)
	}
	run(1, "obs", "switch", "No Such Scene")

	// A snapshot switches back while StreamGremlin owns the scene…
	run(0, "obs", "snapshot", "take", "brb", "Be Right Back")
	time.Sleep(200 * time.Millisecond)
	if out := run(0, "obs", "snapshot", "restore", "brb"); !strings.Contains(out, "Switched back to Just Chatting") || m.Current() != "Just Chatting" {
		t.Fatalf("restore:\n%s", out)
	}
	// …and never after the streamer changed it by hand.
	run(0, "obs", "snapshot", "take", "brb", "Be Right Back")
	time.Sleep(200 * time.Millisecond)
	m.SwitchByHand("Gaming")
	time.Sleep(200 * time.Millisecond)
	if out := run(0, "obs", "snapshot", "restore", "brb"); !strings.Contains(out, "Didn't switch back: The scene was changed in OBS") || m.Current() != "Gaming" {
		t.Fatalf("manual change:\n%s", out)
	}

	// A snapshot open at a crash doesn't switch back after the restart.
	run(0, "obs", "snapshot", "take", "break", "Be Right Back")
	e.kill()
	e = startEngine(t, dir)
	eventually(t, "OBS reconnects", func() string {
		if out := run(0, "obs"); !strings.Contains(out, "connected (OBS") {
			return "not yet: " + out
		}
		return ""
	})
	if out := run(0, "obs", "snapshot"); !strings.Contains(out, "No open snapshots") || m.Current() != "Be Right Back" {
		t.Fatalf("after restart:\n%s", out)
	}
	if out := run(0, "events", "-types", "broadcast.*", "-limit", "50"); !strings.Contains(out, "broadcast.scene.restore_skipped") ||
		!strings.Contains(out, "broadcast.scene_item.visibility_changed") {
		t.Fatalf("events:\n%s", out)
	}
	// The password is readable nowhere but the encrypted secrets.
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, _ := os.ReadFile(path); bytes.Contains(b, []byte("obs-canary-pw-71c")) {
			t.Errorf("the OBS password is readable in %s", path)
		}
		return nil
	})
	if strings.Contains(e.stderr.String(), "canary") {
		t.Fatal("password on stderr")
	}

	run(0, "obs", "disconnect")
	eventually(t, "disconnected", func() string {
		if m.Clients() != 0 {
			return "still connected"
		}
		return ""
	})
}
