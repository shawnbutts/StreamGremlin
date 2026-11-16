package updates

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/release"
)

// makeApp writes a stand-in StreamGremlin.app whose program says what.
func makeApp(t *testing.T, at, version, what string) {
	t.Helper()
	for _, d := range []string{"Contents/MacOS", "Contents/Resources"} {
		if err := os.MkdirAll(filepath.Join(at, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plist, err := release.InfoPlist(version)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(at, "Contents/Info.plist"), plist, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(at, "Contents/MacOS/StreamGremlin"), []byte(what), 0o700); err != nil { //nolint:gosec // a program, as in an app
		t.Fatal(err)
	}
}

func TestSwapAppFromADiskImage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("makes a disk image")
	}
	tmp := t.TempDir()
	stage := filepath.Join(tmp, "stage")
	makeApp(t, filepath.Join(stage, "StreamGremlin.app"), "0.1.0-beta.6", "new")
	dmg := filepath.Join(tmp, "new.dmg")
	// STREAMGREMLIN_TEST_DMG swaps in a real release's disk image instead,
	// to check its layout: go test -run SwapApp ./internal/updates.
	real := os.Getenv("STREAMGREMLIN_TEST_DMG")
	if real != "" {
		dmg = real
	} else if out, err := exec.Command("/usr/bin/hdiutil", "create", "-quiet", "-volname", "StreamGremlin test", "-srcfolder", stage, //nolint:gosec // A fixed tool on the test's own folders.
		"-fs", "HFS+", "-format", "UDZO", "-o", dmg).CombinedOutput(); err != nil {
		t.Skipf("hdiutil can't make a disk image here: %s", out)
	}
	apps := filepath.Join(tmp, "Applications")
	bundle := filepath.Join(apps, "StreamGremlin.app")
	makeApp(t, bundle, "0.1.0-beta.5", "old")

	// The running app: a process the helper waits for.
	running := exec.Command("/bin/sleep", "30")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = running.Wait(); close(done) }()
	t.Cleanup(func() { _ = running.Process.Kill() })

	opened := filepath.Join(tmp, "opened")
	script := filepath.Join(tmp, "open.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$1\" > "+opened+"\n"), 0o700); err != nil { //nolint:gosec // a script the test runs
		t.Fatal(err)
	}
	defer func(old string) { openCmd = old }(openCmd)
	openCmd = script

	if err := swapApp(dmg, bundle, running.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bundle, "Contents/MacOS/StreamGremlin")); real == "" && string(b) != "new" {
		t.Fatalf("the installed app says %q", b)
	} else if real != "" && !bytes.HasPrefix(b, []byte{0xca, 0xfe, 0xba, 0xbe}) {
		t.Fatalf("the installed app isn't a universal program: % x", b[:min(4, len(b))])
	}
	if real != "" {
		// Signed as the release built it.
		if out, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", bundle).CombinedOutput(); err != nil { //nolint:gosec // A fixed tool on the test's own folder.
			t.Fatalf("codesign: %v\n%s", err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(apps, oldName)); err != nil {
		t.Fatalf("the old app went before the running one quit: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(opened); err == nil {
		t.Fatal("the new app opened before the old one quit")
	}
	_ = running.Process.Kill()
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(opened)
		if err == nil && string(b) == bundle+"\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the new app wasn't opened: %q %v", b, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(apps, oldName)); !os.IsNotExist(err) {
		t.Fatalf("the old app is still there: %v", err)
	}
	if entries, _ := os.ReadDir(apps); len(entries) != 1 {
		t.Fatalf("left in Applications: %v", entries)
	}
}

func TestSwapAppRefusesOtherDiskImages(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("makes a disk image")
	}
	tmp := t.TempDir()
	stage := filepath.Join(tmp, "stage")
	if err := os.MkdirAll(filepath.Join(stage, "StreamGremlin.app/Contents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "StreamGremlin.app/Contents/Info.plist"),
		[]byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.other</string></dict></plist>`), 0o600); err != nil {
		t.Fatal(err)
	}
	dmg := filepath.Join(tmp, "other.dmg")
	//nolint:gosec // A fixed tool on the test's own folders.
	if out, err := exec.Command("/usr/bin/hdiutil", "create", "-quiet", "-srcfolder", stage, "-fs", "HFS+", "-format", "UDZO", "-o", dmg).CombinedOutput(); err != nil {
		t.Skipf("hdiutil can't make a disk image here: %s", out)
	}
	bundle := filepath.Join(tmp, "Applications", "StreamGremlin.app")
	makeApp(t, bundle, "0.1.0-beta.5", "old")
	if err := swapApp(dmg, bundle, os.Getpid()); err == nil {
		t.Fatal("installed another app")
	}
	if b, _ := os.ReadFile(filepath.Join(bundle, "Contents/MacOS/StreamGremlin")); string(b) != "old" {
		t.Fatalf("the installed app changed: %q", b)
	}
}

func TestInstalledBundle(t *testing.T) {
	for exe, ok := range map[string]bool{
		"/Applications/StreamGremlin.app/Contents/MacOS/StreamGremlin":                               true,
		"/Users/x/Applications/StreamGremlin.app/Contents/MacOS/StreamGremlin":                       true,
		"/Volumes/StreamGremlin 0.1.0/StreamGremlin.app/Contents/MacOS/StreamGremlin":                false,
		"/private/var/folders/x/AppTranslocation/1/d/StreamGremlin.app/Contents/MacOS/StreamGremlin": false,
		"/Users/x/code/StreamGremlin/bin/app/StreamGremlin":                                          false,
	} {
		if _, err := installedBundle(exe); (err == nil) != ok {
			t.Errorf("%s: %v", exe, err)
		}
	}
}
