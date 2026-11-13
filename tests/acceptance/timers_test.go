//go:build acceptance

package acceptance

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// Clock and timer overlays through the real engine and CLI (D-092): a
// countdown set up, opened as OBS would, started, paused, given more
// time; it reaches zero and says so as an event; it's still ended after a
// restart; a clock has nothing to start; the Flip clock example installs
// and is the countdown's look, but never a wheel's.
func TestCountdownsThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	e := startEngine(t, dir)
	out, errOut, code := cli(t, dir, "overlays", "add", "-timer", "soon")
	if code != 0 {
		t.Fatal(out, errOut)
	}
	p := openPage(t, linkIn(t, out))
	if out, errOut, code := cli(t, dir, "overlays", "look", "soon", "-mode", "countdown", "-length", "1m", "-label", "Starting soon", "-end-text", "Live!"); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "the look in the hello", func() string { return p.has("hello", `"timer_label":"Starting soon"`) })

	if out, errOut, code := cli(t, dir, "overlays", "timer", "soon", "start"); code != 0 || !strings.Contains(out, "soon is running, from 1m0s") {
		t.Fatal(out, errOut)
	}
	eventually(t, "running on the page", func() string { return p.has("timer", `"running":true`) })
	if out, _, code := cli(t, dir, "overlays", "timer", "soon", "pause"); code != 0 || !strings.Contains(out, "stopped at") {
		t.Fatal(out)
	}
	if out, _, code := cli(t, dir, "overlays", "timer", "soon", "add", "-minutes", "1"); code != 0 || !(strings.Contains(out, "stopped at 1m59s") || strings.Contains(out, "stopped at 2m0s")) {
		t.Fatal(out)
	}
	// Two seconds, then it ends and says so.
	if out, errOut, code := cli(t, dir, "overlays", "timer", "soon", "start", "-minutes", "0.034"); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "the end", func() string {
		out, _, _ := cli(t, dir, "events", "-types", "overlays.timer.ended", "-json")
		if strings.Contains(out, `"overlay":"soon"`) && strings.Contains(out, "Starting soon") {
			return ""
		}
		return out
	})
	eventually(t, "ended on the page", func() string { return p.has("timer", `"ended":true`) })

	e.kill()
	startEngine(t, dir)
	if out, _, _ := cli(t, dir, "overlays", "-json"); !strings.Contains(out, `"ended": true`) {
		t.Fatal("after a restart:", out)
	}

	// Auto-start: a page that says it loaded starts it from the top.
	if out, errOut, code := cli(t, dir, "overlays", "look", "soon", "-length", "3m", "-auto-start"); code != 0 {
		t.Fatal(out, errOut)
	}
	link, _, _ := cli(t, dir, "overlays", "link", "soon")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(linkIn(t, link), "http")+"ws",
		&websocket.DialOptions{Subprotocols: []string{"streamgremlin.overlay.v1"}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"loaded"}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "started as it loaded", func() string {
		if out, _, _ := cli(t, dir, "overlays"); !strings.Contains(out, "countdown, running") {
			return out
		}
		return ""
	})

	if out, errOut, code := cli(t, dir, "overlays", "add", "-timer", "wall-clock"); code != 0 {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "overlays", "timer", "wall-clock", "start"); code == 0 || !strings.Contains(errOut, "isn't a countdown or a stopwatch") {
		t.Fatal("a clock started:", errOut)
	}

	zip := filepath.Join(t.TempDir(), "flip-clock.zip")
	zipFolder(t, filepath.Join("..", "..", "examples", "themes", "flip-clock"), zip)
	if out, _, _ := cli(t, dir, "packages", "install", zip); !strings.Contains(out, "a clock and timer look") || strings.Contains(out, "result StreamGremlin drew") {
		t.Fatal("described as a wheel look:", out)
	}
	if out, errOut, code := cli(t, dir, "packages", "install", zip, "-yes"); code != 0 || !strings.Contains(out, "Installed Flip clock 1.1.0") {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "overlays", "look", "soon", "-look", "flip-clock", "-set", "cards=Midnight"); code != 0 || !strings.Contains(out, "the Flip clock look") {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "overlays", "add", "-wheel", "wheel"); code != 0 {
		t.Fatal(out, errOut)
	}
	if _, errOut, code := cli(t, dir, "overlays", "look", "wheel", "-look", "flip-clock"); code == 0 || !strings.Contains(errOut, "a look for timer overlays") {
		t.Fatal("a wheel took a timer's look:", errOut)
	}
}
