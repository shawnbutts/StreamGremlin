//go:build acceptance

package acceptance

import (
	"bufio"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEventsSimulateListAndSurviveACrash(t *testing.T) {
	dir := t.TempDir()
	e := startEngine(t, dir)

	out, errOut, code := cli(t, dir, "events", "simulate", "chat.command.received", "user=Gremlin_Fan", "command=!lurk")
	if code != 0 || !strings.Contains(out, "chat.command.received") || !strings.Contains(out, "synthetic") {
		t.Fatalf("simulate: %d\n%s%s", code, out, errOut)
	}
	if _, errOut, code := cli(t, dir, "events", "simulate", "chat.command.received", "command=lurk"); code != 2 || !strings.Contains(errOut, "starts with !") {
		t.Fatalf("bad command: %d %s", code, errOut)
	}
	// A retried request through the API stores one event.
	for i := 0; i < 2; i++ {
		code, body := call(t, e, "POST", "/v1/events/simulate", e.token,
			`{"type":"broadcast.scene.changed","request_id":"retry-1","fields":{"scene_name":"BRB"}}`, nil)
		if code != 201 && code != 200 {
			t.Fatalf("api simulate: %d %s", code, body)
		}
	}
	out, _, _ = cli(t, dir, "events")
	if strings.Count(out, "\n") != 2 || !strings.Contains(out, "Gremlin_Fan: !lurk") || !strings.Contains(out, "BRB") {
		t.Fatalf("list:\n%s", out)
	}

	e.kill() // crash
	e = startEngine(t, dir)
	out, _, _ = cli(t, dir, "events", "-json")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("after restart:\n%s", out)
	}
	var last struct{ Seq int64 }
	_ = json.Unmarshal([]byte(lines[1]), &last)
	// Numbering carries on after the restart.
	code, body := call(t, e, "POST", "/v1/events/simulate", e.token, `{"type":"chat.message.received","request_id":"after-crash"}`, nil)
	if code != 201 || !strings.Contains(body, `"seq":3`) || last.Seq != 2 {
		t.Fatalf("after restart: %d %s (last %d)", code, body, last.Seq)
	}
}

func TestEventsWatchFollowsLiveFromTheCursor(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	cli(t, dir, "events", "simulate", "chat.message.received", "text=before")

	cmd := exec.Command(bin, "-data-dir", dir, "events", "watch", "-after", "0", "-types", "chat.message.*")
	cmd.Env = cleanEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	lines := make(chan string, 10)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func() string {
		t.Helper()
		select {
		case l := <-lines:
			return l
		case <-time.After(10 * time.Second):
			t.Fatal("watch printed nothing")
		}
		return ""
	}
	if l := next(); !strings.Contains(l, "before") {
		t.Fatalf("first line %q", l)
	}
	cli(t, dir, "events", "simulate", "broadcast.scene.changed") // filtered out
	cli(t, dir, "events", "simulate", "chat.message.received", "text=live one")
	if l := next(); !strings.Contains(l, "live one") {
		t.Fatalf("live line %q", l)
	}
}
