//go:build acceptance

package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const brbWorkflow = `{"schema":"streamgremlin.workflow/v1","id":"chat.brb","name":"Be right back",
	"triggers":[{"type":"chat.command.received","where":{"command":"brb"}}],
	"steps":[
		{"id":"away","action":"obs.scene.snapshot_and_switch","with":{"key":"brb","scene":"Be Right Back"}},
		{"action":"chat.send","with":{"message":"Back soon! (asked by ${event.actor.display_name})","as":"streamer"}}
	]}`

const backWorkflow = `{"schema":"streamgremlin.workflow/v1","id":"chat.back","name":"Back",
	"triggers":[{"type":"chat.command.received","where":{"command":"back"}}],
	"steps":[{"id":"restore","action":"obs.scene.restore","with":{"key":"brb"}},
	         {"action":"chat.send","with":{"message":"restore: ${steps.restore.output.status}","as":"streamer"}}]}`

const slowWorkflow = `{"schema":"streamgremlin.workflow/v1","id":"chat.slow","name":"Slow",
	"triggers":[{"type":"chat.command.received","where":{"command":"slow"}}],
	"steps":[{"wait":"30s"},{"action":"chat.send","with":{"message":"slow done","as":"streamer"}}]}`

func TestWorkflowsDriveTwitchAndOBS(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	ob, obURL := startMockOBS(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	if out, errOut, code := cli(t, dir, "obs", "connect", "-url", obURL); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "chat", func() string {
		if out, _, _ := cli(t, dir, "twitch"); !strings.Contains(out, "Chat:       connected") {
			return "not yet: " + out
		}
		return ""
	})

	files := t.TempDir()
	save := func(name, body string) string {
		p := filepath.Join(files, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		out, errOut, code := cli(t, dir, "workflows", "save", p)
		if code != 0 {
			t.Fatalf("save %s: %s%s", name, out, errOut)
		}
		return out
	}
	// New workflows start on (D-077); these are turned off to show an off
	// one never runs.
	if out := save("brb.json", brbWorkflow); !strings.Contains(out, "It's on") {
		t.Fatal(out)
	}
	save("back.json", backWorkflow)
	save("slow.json", slowWorkflow)
	for _, id := range []string{"chat.brb", "chat.back", "chat.slow"} {
		cli(t, dir, "workflows", "off", id)
	}
	if _, errOut, code := cli(t, dir, "workflows", "save", writeFile(t, files, "bad.json", strings.Replace(brbWorkflow, "obs.scene.snapshot_and_switch", "obs.teleport", 1))); code != 2 || !strings.Contains(errOut, `no action "obs.teleport"`) {
		t.Fatalf("invalid: %d %s", code, errOut)
	}
	tw.Say("streamer", "viewer", "!brb") // off: nothing happens
	time.Sleep(300 * time.Millisecond)
	if ob.Current() != "Starting Soon" {
		t.Fatal("a workflow ran while off")
	}
	for _, id := range []string{"chat.brb", "chat.back", "chat.slow"} {
		cli(t, dir, "workflows", "on", id)
	}

	// A test event runs the workflow without touching OBS or Twitch.
	cli(t, dir, "events", "simulate", "chat.command.received", "command=!brb")
	eventually(t, "the test run", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.brb"); !strings.Contains(out, "succeeded (test)") {
			return "not yet: " + out
		}
		return ""
	})
	if ob.Current() != "Starting Soon" || len(tw.ChatLog()) != 1 { // the log holds only the viewer's !brb
		t.Fatalf("a test run reached OBS or Twitch: %s, %d chat lines", ob.Current(), len(tw.ChatLog()))
	}

	// A real command switches OBS and answers in chat.
	tw.Say("streamer", "SomeViewer", "!brb")
	eventually(t, "the switch", func() string {
		if ob.Current() != "Be Right Back" {
			return ob.Current()
		}
		for _, m := range tw.ChatLog() {
			if m.Text == "Back soon! (asked by SomeViewer)" {
				return ""
			}
		}
		return "no chat reply"
	})
	time.Sleep(200 * time.Millisecond)
	tw.Say("streamer", "SomeViewer", "!back")
	eventually(t, "the switch back", func() string {
		if ob.Current() != "Starting Soon" {
			return ob.Current()
		}
		return ""
	})
	out, _, _ := cli(t, dir, "runs", "-workflow", "chat.back")
	id := strings.Fields(out)[0]
	if trace, _, _ := cli(t, dir, "runs", "show", id); !strings.Contains(trace, "obs.scene.restore") || !strings.Contains(trace, `"status":"restored"`) {
		t.Fatalf("trace:\n%s", trace)
	}

	// A crash in the middle of a run: it's interrupted, never run again.
	tw.Say("streamer", "viewer", "!slow")
	eventually(t, "the slow run starts", func() string {
		// Empty output (no run yet) must mean "not yet", not "done".
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.slow"); !strings.Contains(out, "running") {
			return "not running yet: " + out
		}
		return ""
	})
	e.kill()
	startEngine(t, dir, "-twitch-auth-url", twURL)
	eventually(t, "interrupted", func() string {
		if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.slow"); !strings.Contains(out, "interrupted") {
			return "not yet: " + out
		}
		return ""
	})
	time.Sleep(300 * time.Millisecond)
	if out, _, _ := cli(t, dir, "runs", "-workflow", "chat.slow"); strings.Count(out, "\n") != 1 {
		t.Fatalf("the run was repeated:\n%s", out)
	}
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
