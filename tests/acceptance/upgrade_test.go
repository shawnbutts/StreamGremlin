//go:build acceptance

package acceptance

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Upgrades (M9 slice 3): a data folder from each released beta, kept in
// tests/testdata/upgrade/VERSION, opens in this build with everything in
// it. Each was made by that beta's own engine and CLI with the same
// made-up data (TestMakeUpgradeFixture, `make upgrade-fixture`), so a
// changed migration, a setting read differently or a lost file fails here
// before a tester's update does.

const upgradeDir = "../testdata/upgrade"

// The made-up data: the Twitch app ID and the stand-in's tokens are fake.
const fixtureClientID = "abcdefghij0123456789"

// TestMakeUpgradeFixture fills a data folder through the CLI of the binary
// under test (STREAMGREMLIN_ACCEPTANCE_BIN, a released beta's), and copies
// it to STREAMGREMLIN_MAKE_FIXTURE. Skipped otherwise.
func TestMakeUpgradeFixture(t *testing.T) {
	out := os.Getenv("STREAMGREMLIN_MAKE_FIXTURE")
	if out == "" {
		t.Skip("set STREAMGREMLIN_MAKE_FIXTURE (make upgrade-fixture does)")
	}
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	must := func(args ...string) string {
		t.Helper()
		o, errOut, code := cli(t, dir, args...)
		if code != 0 {
			t.Fatalf("%v: %s %s", args, o, errOut)
		}
		return o
	}
	must("twitch", "client-id", fixtureClientID)
	if o, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(o)
	}
	eventually(t, "chat", func() string {
		if tw.Subscribed("streamer") == 0 {
			return "not reading chat"
		}
		return ""
	})
	tw.Say("streamer", "ann", "hello!")
	tw.Say("streamer", "bo", "hi all")
	eventually(t, "viewers recorded", func() string {
		o, _, _ := cli(t, dir, "viewers")
		if !strings.Contains(o, "ann") || !strings.Contains(o, "bo") {
			return "not yet: " + o
		}
		return ""
	})
	must("points", "rename", "Gold")
	must("points", "give", "@ann", "50", "-reason", "welcome")
	must("points", "give", "@bo", "20")
	must("points", "take", "@bo", "5", "-reason", "oops")

	for _, f := range []string{"hello.json", "lurk.json"} {
		must("workflows", "save", filepath.Join("..", "..", "examples", "workflows", f))
	}
	must("workflows", "on", workflowID(t, "hello.json"))
	must("quotes", "add", "-by", "ann", "The gremlin is loose.")
	must("quotes", "add", "Second quote")
	must("vars", "set", "global", "deaths", "7")
	must("timers", "set", "break", "-name", "Take a break", "-every", "30m")
	must("wheels", "add", "snacks", "-title", "Snack wheel", "-choice", "Chips:2", "-choice", "Fruit")
	must("overlays", "add", "alerts")
	must("sounds", "add", "-name", "cheer", filepath.Join("..", "..", "examples", "themes", "paper-wheel", "cheer.wav"))
	must("packages", "install", filepath.Join("..", "..", "examples", "plugins", "dice-tray.zip"), "-yes", "-allow", "chat.send,events.publish,points.rounds")
	if code, body := call(t, e, "PUT", "/v1/settings/ui", e.token, `{"theme":"midnight","text_size":"large"}`, nil); code != 200 {
		t.Fatal(code, body)
	}
	must("backups", "create")
	stopEngine(t, e)

	if err := os.RemoveAll(out); err != nil {
		t.Fatal(err)
	}
	// The folder as it was left, less what's made at each start or is
	// this run's own: logs, the lock, the API's address and token.
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch rel {
		case "logs", "lock", "api-addr", "api-token":
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(out, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(p) //nolint:gosec // the test's own folder
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}

// TestUpgradeFromEarlierBetas opens each saved data folder with this build.
func TestUpgradeFromEarlierBetas(t *testing.T) {
	entries, err := os.ReadDir(upgradeDir)
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	// Every release since the first on GitHub has one, but the newest, whose
	// folder is made once it's out.
	released := regexp.MustCompile(`(?m)^## (\d+\.\d+\.\d+[^ ]*) \(`).FindAllStringSubmatch(read(t, "../../CHANGELOG.md"), -1)
	for i, m := range released {
		if i == 0 {
			continue
		}
		if !contains(versions, m[1]) {
			t.Errorf("no data folder from %s in %s: make upgrade-fixture VERSION=%s", m[1], upgradeDir, m[1])
		}
	}
	for _, v := range versions {
		t.Run(v, func(t *testing.T) { checkUpgrade(t, filepath.Join(upgradeDir, v)) })
	}
}

func checkUpgrade(t *testing.T, fixture string) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	// As the app keeps a data folder: git doesn't keep file modes, and the
	// secret store refuses a file others can read.
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o700)
		}
		return os.Chmod(p, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	// Twitch is a dead address: sign-ins stay, unchecked.
	e := startEngine(t, dir)
	has := func(args []string, wants ...string) {
		t.Helper()
		o, errOut, code := cli(t, dir, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s\n%s", args, code, o, errOut)
		}
		for _, w := range wants {
			if !strings.Contains(o, w) {
				t.Errorf("%v doesn't say %q:\n%s", args, w, o)
			}
		}
	}
	has([]string{"viewers"}, "ann", "bo")
	has([]string{"points"}, "2 viewers hold 65 Gold")
	has([]string{"points", "audit"}, "✓")
	has([]string{"workflows"}, workflowID(t, "hello.json"), workflowID(t, "lurk.json"))
	has([]string{"quotes"}, "The gremlin is loose.", "Second quote")
	has([]string{"vars", "global"}, "deaths", "7")
	has([]string{"timers"}, "Take a break")
	has([]string{"wheels"}, "Snack wheel", "Chips", "Fruit")
	has([]string{"overlays"}, "alerts")
	has([]string{"sounds"}, "cheer")
	has([]string{"packages"}, "Dice")
	has([]string{"twitch"}, "streamer")
	has([]string{"secrets", "-check"}, "twitch")
	code, body := call(t, e, "GET", "/v1/settings/ui", e.token, "", nil)
	if code != 200 || !strings.Contains(body, `"theme":"midnight"`) || !strings.Contains(body, `"text_size":"large"`) {
		t.Errorf("settings: %d %s", code, body)
	}
	// The beta's own backup still checks out, and could be restored.
	o, _, _ := cli(t, dir, "backups")
	name := regexp.MustCompile(`streamgremlin-manual-\S+\.zip`).FindString(o)
	if name == "" {
		t.Fatalf("the beta's backup is gone:\n%s", o)
	}
	has([]string{"backups", "verify", name})
	// Nothing left behind for a downgrade to trip on but the backups made.
	if _, err := os.Stat(filepath.Join(dir, "updates")); err == nil {
		t.Error("an updates folder survived the start")
	}
}

// workflowID is an example workflow's id.
func workflowID(t *testing.T, file string) string {
	t.Helper()
	var w struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join("..", "..", "examples", "workflows", file))), &w); err != nil || w.ID == "" {
		t.Fatalf("%s: %v", file, err)
	}
	return w.ID
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // the repository's own files
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// stopEngine asks the engine to stop as a streamer's quit would, so the
// database is closed properly, and waits.
func stopEngine(t *testing.T, e *engine) {
	t.Helper()
	if runtime.GOOS == "windows" {
		e.kill()
		return
	}
	if err := e.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.exited:
	case <-time.After(20 * time.Second):
		t.Fatal("the engine didn't stop")
	}
}
