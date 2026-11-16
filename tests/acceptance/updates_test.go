//go:build acceptance

package acceptance

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockgithub"
)

// Updates through the real engine and CLI, against mockgithub (D-120): a
// check finds the newer beta, a download is checked against the signed
// SHA256SUMS.txt and lands in the data folder; a release signed with
// another key is shown but never downloaded as good; and the test-only
// flags are refused without a test server.
func TestUpdatesThroughTheCLI(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	gh := mockgithub.New()
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	gh.Base = srv.URL
	dmg := "StreamGremlin-0.1.0-beta.6-macos.dmg"
	exe := "StreamGremlin-0.1.0-beta.6-windows-x64-setup.exe"
	gh.Add(mockgithub.Release{Tag: "v0.1.0-beta.6", Prerelease: true, Notes: "Fixes.", Files: mockgithub.Signed(map[string][]byte{
		dmg: []byte("a disk image"), exe: []byte("an installer"), "StreamGremlin-0.1.0-beta.6-linux-x64.deb": []byte("a package")}, priv)})

	dir := t.TempDir()
	startEngine(t, dir, "-updates-url", srv.URL, "-updates-key", base64.StdEncoding.EncodeToString(pub), "-updates-version", "0.1.0-beta.5")
	if out, _, code := cli(t, dir, "updates"); code != 0 || !strings.Contains(out, "This is StreamGremlin 0.1.0-beta.5.") ||
		!strings.Contains(out, "Not checked yet.") || !strings.Contains(out, "test server") {
		t.Fatal(out)
	}
	out, errOut, code := cli(t, dir, "updates", "check")
	if code != 0 || !strings.Contains(out, "StreamGremlin 0.1.0-beta.6 is out: https://github.com/shawnbutts/StreamGremlin/releases/tag/v0.1.0-beta.6") {
		t.Fatal(out, errOut)
	}
	if out, errOut, code := cli(t, dir, "updates", "download"); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "the download is checked", func() string {
		out, _, _ := cli(t, dir, "updates")
		if strings.Contains(out, "is downloaded and its signature checked") {
			return ""
		}
		return out
	})
	// This system's download, and only it.
	entries, err := os.ReadDir(filepath.Join(dir, "updates"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	if n := entries[0].Name(); n != dmg && n != exe && !strings.Contains(out, "Linux") {
		t.Fatal(n)
	}
	if out, _, code := cli(t, dir, "updates", "off"); code != 0 || !strings.Contains(out, "doesn't check") {
		t.Fatal(out)
	}
	if out, _, _ := cli(t, dir, "updates"); !strings.Contains(out, "is out") {
		t.Fatal("turning checking off forgot the release:", out)
	}

	// Signed with a key the app doesn't trust.
	gh2 := mockgithub.New()
	srv2 := httptest.NewServer(gh2)
	t.Cleanup(srv2.Close)
	gh2.Base = srv2.URL
	gh2.Add(mockgithub.Release{Tag: "v0.1.0-beta.6", Prerelease: true, Files: mockgithub.Signed(map[string][]byte{
		dmg: []byte("a disk image"), exe: []byte("an installer")}, otherPriv)})
	dir2 := t.TempDir()
	startEngine(t, dir2, "-updates-url", srv2.URL, "-updates-key", base64.StdEncoding.EncodeToString(pub), "-updates-version", "0.1.0-beta.5")
	cli(t, dir2, "updates", "check")
	if out, errOut, code := cli(t, dir2, "updates", "download"); code != 0 {
		t.Fatal(out, errOut)
	}
	eventually(t, "the bad signature is refused", func() string {
		out, _, _ := cli(t, dir2, "updates")
		if strings.Contains(out, "failed: the release's signature doesn't check out") {
			return ""
		}
		return out
	})
	if entries, _ := os.ReadDir(filepath.Join(dir2, "updates")); len(entries) != 0 {
		t.Fatalf("left a download: %v", entries)
	}

	// The test-only flags need a test server on this computer.
	for _, args := range [][]string{
		{"-updates-key", base64.StdEncoding.EncodeToString(pub)},
		{"-updates-version", "9.9.9"},
		{"-updates-url", "https://api.github.com"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, bin, append([]string{"-data-dir", t.TempDir(), "engine", "-addr", "127.0.0.1:0", "-overlay-addr", "off"}, args...)...)
		cmd.Env = cleanEnv()
		out, err := cmd.CombinedOutput()
		cancel()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || !strings.Contains(string(out), "updates") {
			t.Errorf("%v: %v\n%s", args, err, out)
		}
	}
}
