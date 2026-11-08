//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

// One answer per chat command, through the real engine and CLI.
func TestOneAnswerPerCommandThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	files := t.TempDir()
	wf := func(id, name, extra string) string {
		return writeFile(t, files, id+".json", `{"schema":"streamgremlin.workflow/v1","id":"`+id+`","name":"`+name+`",
			"triggers":[{"type":"chat.command.received","where":{"command":"so"`+extra+`}}],
			"steps":[{"action":"note","with":{"text":"hi"}}]}`)
	}
	for _, f := range []string{
		wf("so.mods", "Shoutout for mods", `,"chatter.role":["moderator","broadcaster"]`),
		wf("so.viewers", "Shoutout for viewers", `,"chatter.role":["viewer","subscriber","vip"]`),
		wf("so.all", "Shoutout for all", ""),
	} {
		if out, errOut, code := cli(t, dir, "workflows", "save", f); code != 0 {
			t.Fatal(out, errOut)
		}
	}
	for _, id := range []string{"so.mods", "so.viewers"} {
		if out, errOut, code := cli(t, dir, "workflows", "on", id); code != 0 {
			t.Fatalf("%s: %s%s", id, out, errOut)
		}
	}
	_, errOut, code := cli(t, dir, "workflows", "on", "so.all")
	if code != 2 || !strings.Contains(errOut, "!so is already answered by “Shoutout for mods”") ||
		!strings.Contains(errOut, "“Shoutout for viewers”") {
		t.Fatalf("%d %s", code, errOut)
	}
	out, _, _ := cli(t, dir, "commands")
	if !strings.Contains(out, "!so") || !strings.Contains(out, "Shoutout for all (so.all) [off]") ||
		!strings.Contains(out, "for moderator, broadcaster") || strings.Contains(out, "More than one") {
		t.Fatalf("commands:\n%s", out)
	}
}
