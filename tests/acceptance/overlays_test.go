//go:build acceptance

package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeOBS opens an overlay's link as OBS's browser source would (or, with
// preview, as the app's preview does): it loads
// the page, connects, reports each alert as started and, at once, done.
// It returns the alerts it showed.
func fakeOBS(t *testing.T, link string, preview bool) (shown func() []map[string]any, closed <-chan struct{}) {
	t.Helper()
	res, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("the page: %d %q", res.StatusCode, res.Header.Get("Content-Security-Policy"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ws := "ws" + strings.TrimPrefix(link, "http") + "ws"
	if preview {
		ws += "?preview=1" // as the app's preview connects
	}
	c, resp, err := websocket.Dial(ctx, ws,
		&websocket.DialOptions{Subprotocols: []string{"streamgremlin.overlay.v1"}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []map[string]any
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m struct {
				Type string
				Body struct{ Alert map[string]any }
			}
			_ = json.Unmarshal(b, &m)
			if m.Type != "alert" {
				continue
			}
			mu.Lock()
			got = append(got, m.Body.Alert)
			mu.Unlock()
			for _, st := range []string{"started", "done"} {
				ack, _ := json.Marshal(map[string]any{"type": "ack", "id": m.Body.Alert["id"], "status": st})
				_ = c.Write(ctx, websocket.MessageText, ack)
			}
		}
	}()
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), got...)
	}, done
}

// linkIn finds an overlay link in the CLI's output.
func linkIn(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "http://127.0.0.1:") && strings.Contains(line, "/o/") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no link in:\n%s", out)
	return ""
}

// token is a link's secret part.
func token(link string) string {
	_, after, _ := strings.Cut(link, "/o/")
	return strings.Trim(after, "/")
}

// Alert overlays through the real engine and CLI: added with a private
// link, opened as OBS would, shown by a follow with the follower's
// picture, never by a test event, kept across a crash; a new link stops
// the old one; the link never reaches the log.
func TestAlertOverlaysThroughTheCLI(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)

	out, errOut, code := cli(t, dir, "overlays", "add", "alerts")
	if code != 0 || !strings.Contains(out, "Keep it private") {
		t.Fatal(out, errOut)
	}
	link := linkIn(t, out)
	if out, _, _ := cli(t, dir, "overlays"); !strings.Contains(out, "alerts") || !strings.Contains(out, "not open in OBS") || strings.Contains(out, "/o/") {
		t.Fatalf("list:\n%s", out)
	}
	if _, errOut, code := cli(t, dir, "overlays", "test", "alerts"); code == 0 || !strings.Contains(errOut, "isn't open in OBS") {
		t.Fatalf("a sample with OBS closed: %d %s", code, errOut)
	}
	if out, errOut, code := cli(t, dir, "overlays", "look", "alerts", "-position", "bottom-left", "-animation", "pop", "-seconds", "3"); code != 0 ||
		!strings.Contains(out, "bottom-left, pop, 3 seconds") {
		t.Fatal(out, errOut)
	}

	if out, errOut, code := cli(t, dir, "overlays", "look", "alerts", "-gradient", "#ff0080", "-gradient-angle", "90", "-border", "2",
		"-font-name", "Comic Neue", "-size", "120", "-outline", "2", "-text-shadow", "40"); code != 0 {
		t.Fatal(out, errOut)
	}
	if out, _, _ := cli(t, dir, "overlays", "-json"); !strings.Contains(out, `"font": "custom"`) || !strings.Contains(out, `"font_name": "Comic Neue"`) ||
		!strings.Contains(out, `"background2": "#ff0080"`) || !strings.Contains(out, `"position": "bottom-left"`) {
		t.Fatalf("the look:\n%s", out)
	}
	if _, errOut, code := cli(t, dir, "overlays", "look", "alerts", "-font-name", `x"; }`); code == 0 || !strings.Contains(errOut, "font's name") {
		t.Fatalf("a bad font name: %d %s", code, errOut)
	}
	shown, _ := fakeOBS(t, link, false)
	eventually(t, "OBS", func() string {
		if out, _, _ := cli(t, dir, "overlays"); !strings.Contains(out, "open in 1 page") {
			return out
		}
		return ""
	})
	if out, errOut, code := cli(t, dir, "overlays", "test", "alerts"); code != 0 || !strings.Contains(out, "showing in OBS") {
		t.Fatal(out, errOut)
	}

	// A follow shows an alert with the follower's picture; a test doesn't.
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "events", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return out
		}
		return ""
	})
	wf := writeFile(t, t.TempDir(), "alert.json", `{"schema":"streamgremlin.workflow/v1","id":"twitch.follow-alert","name":"Follow alert",
		"triggers":[{"type":"audience.follow.created"}],
		"steps":[{"action":"overlays.alert","with":{"overlay":"alerts","title":"New follower","text":"${event.actor.display_name} followed!","picture":true}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", wf); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "twitch.follow-alert")
	cli(t, dir, "events", "simulate", "audience.follow.created")
	eventually(t, "the test run", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "twitch.follow-alert"); !strings.Contains(out, "succeeded (test)") {
			return out
		}
		return ""
	})
	// Nobody had a preview open: the trace says nobody saw it. With the
	// app's preview open, the next test shows there, and says so.
	lastTrace := func() string {
		_, body := call(t, e, "GET", "/v1/jobs?workflow=twitch.follow-alert", e.token, "", nil)
		var js struct{ Jobs []struct{ ID string } }
		_ = json.Unmarshal([]byte(body), &js)
		if len(js.Jobs) == 0 {
			return ""
		}
		_, trace := call(t, e, "GET", "/v1/jobs/"+js.Jobs[0].ID, e.token, "", nil)
		return trace
	}
	if tr := lastTrace(); !strings.Contains(tr, "nobody saw it: no preview of alerts was open") || !strings.Contains(tr, "never reaches OBS") {
		t.Fatalf("trace without a preview:\n%s", tr)
	}
	previewShown, _ := fakeOBS(t, link, true)
	eventually(t, "the preview", func() string {
		if out, _, _ := cli(t, dir, "overlays", "-json"); !strings.Contains(out, `"previews": 1`) {
			return out
		}
		return ""
	})
	cli(t, dir, "events", "simulate", "audience.follow.created")
	eventually(t, "the test in the preview", func() string {
		if tr := lastTrace(); !strings.Contains(tr, "shown in 1 preview(s) in the app, never in OBS") {
			return tr
		}
		return ""
	})
	if got := previewShown(); len(got) != 1 || !strings.Contains(got[0]["text"].(string), "followed!") {
		t.Fatalf("the preview showed %v", got)
	}
	tw.Follow("streamer", "Fan")
	eventually(t, "the alert", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "twitch.follow-alert"); strings.Count(out, "succeeded") < 3 {
			return out
		}
		return ""
	})
	got := shown()
	if len(got) != 2 || got[0]["title"] != "Sample alert" || got[1]["text"] != "Fan followed!" ||
		!strings.HasPrefix(got[1]["picture"].(string), "https://static-cdn.jtvnw.net/") {
		t.Fatalf("OBS showed %v", got)
	}

	// A crash: the overlay and its link stay.
	e.kill()
	e = startEngine(t, dir, "-twitch-auth-url", twURL)
	out, _, _ = cli(t, dir, "overlays", "link", "alerts")
	// The test's engine takes any free port, so only the token must match.
	relink := linkIn(t, out)
	if token(relink) != token(link) {
		t.Fatalf("after a crash the link changed: %s, was %s", relink, link)
	}

	// A new link: the old one stops, and its page is closed.
	_, closed := fakeOBS(t, relink, false)
	if out, errOut, code := cli(t, dir, "overlays", "new-link", "alerts"); code != 0 || !strings.Contains(out, "old link stopped working") {
		t.Fatal(out, errOut)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the old link's page is still open")
	}
	if res, err := http.Get(relink); err != nil || res.StatusCode != 404 {
		t.Fatal("the old link still works", err)
	} else {
		_ = res.Body.Close()
	}

	// The link's token never reaches the log.
	tok := token(link)
	logFile, err := os.ReadFile(filepath.Join(dir, "logs", "engine.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logFile), tok) || strings.Contains(e.stderr.String(), tok) {
		t.Fatal("an overlay link is in the log")
	}
	if out, errOut, code := cli(t, dir, "overlays", "delete", "alerts"); code != 0 || !strings.Contains(out, "no longer works") {
		t.Fatal(out, errOut)
	}
}
