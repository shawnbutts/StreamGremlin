package updates

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// On macOS the download is the release's .dmg. Installing opens it
// read-only, copies its StreamGremlin.app beside the running one, swaps
// the two, and leaves a small shell to wait for this process to quit,
// remove the old app and open the new one. Nothing is installed where the
// streamer didn't put StreamGremlin.

const bundleID = "com.streamgremlin.desktop"

// The swapped-out and incoming apps, hidden beside the installed one.
const (
	oldName    = ".StreamGremlin-old.app"
	stagedName = ".StreamGremlin-update.app"
)

// openCmd opens the new app; tests replace it.
var openCmd = "/usr/bin/open"

func apply(file string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	bundle, err := installedBundle(exe)
	if err != nil {
		return err
	}
	return swapApp(file, bundle, os.Getpid())
}

// installedBundle finds the .app the program runs from, and refuses places
// it can't or shouldn't replace itself.
func installedBundle(exe string) (string, error) {
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	switch {
	case filepath.Base(filepath.Dir(exe)) != "MacOS" || filepath.Ext(bundle) != ".app":
		return "", errors.New("StreamGremlin isn't running from its app, so it can't replace itself; install it from the release page")
	case strings.HasPrefix(bundle, "/Volumes/"):
		return "", errors.New("StreamGremlin is running from its disk image: drag it to Applications, open it from there, then install the update")
	case strings.Contains(bundle, "/AppTranslocation/"):
		return "", errors.New("macOS is running StreamGremlin from a temporary copy: move it to Applications, open it from there, then install the update")
	}
	return bundle, nil
}

func swapApp(dmg, bundle string, pid int) error {
	dir := filepath.Dir(bundle)
	mnt, err := os.MkdirTemp("", "streamgremlin-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(mnt) }()
	//nolint:gosec // A fixed tool; the paths are the checked download and a temporary folder.
	if out, err := exec.Command("/usr/bin/hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-noverify", "-mountpoint", mnt, dmg).CombinedOutput(); err != nil {
		return fmt.Errorf("couldn't open the disk image: %s", strings.TrimSpace(string(out)))
	}
	defer func() { _ = exec.Command("/usr/bin/hdiutil", "detach", "-quiet", "-force", mnt).Run() }() //nolint:gosec // as above

	src := filepath.Join(mnt, "StreamGremlin.app")
	//nolint:gosec // A fixed tool reading the mounted image.
	id, err := exec.Command("/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", filepath.Join(src, "Contents", "Info.plist")).Output()
	if err != nil || strings.TrimSpace(string(id)) != bundleID {
		return errors.New("the disk image doesn't hold StreamGremlin")
	}
	staged, old := filepath.Join(dir, stagedName), filepath.Join(dir, oldName)
	for _, p := range []string{staged, old} {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("can't replace StreamGremlin in %s: %w", dir, err)
		}
	}
	//nolint:gosec // A fixed tool; from the mounted image to beside the installed app.
	if out, err := exec.Command("/usr/bin/ditto", src, staged).CombinedOutput(); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("can't copy the new version into %s (%s); install it from the release page", dir, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(bundle, old); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("can't replace StreamGremlin in %s: %w", dir, err)
	}
	if err := os.Rename(staged, bundle); err != nil {
		if rerr := os.Rename(old, bundle); rerr != nil {
			return fmt.Errorf("replacing StreamGremlin failed, and the old version is at %s: %w", old, err)
		}
		_ = os.RemoveAll(staged)
		return fmt.Errorf("can't replace StreamGremlin in %s: %w", dir, err)
	}
	// Waits for this process to quit, then removes the old app and opens
	// the new one. Its own process group, so quitting doesn't stop it.
	const script = `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; rm -rf "$2"; "$4" "$3"`
	// The paths are arguments, never part of the script.
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(pid), old, bundle, openCmd) //nolint:gosec // as above
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		// The new app is in place; it opens next time.
		return fmt.Errorf("the new version is installed, but couldn't be reopened: %w", err)
	}
	return cmd.Process.Release()
}

// cleanup removes an old app a reopen didn't (the helper was stopped).
func cleanup() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return
	}
	if bundle, err := installedBundle(exe); err == nil {
		_ = os.RemoveAll(filepath.Join(filepath.Dir(bundle), oldName))
		_ = os.RemoveAll(filepath.Join(filepath.Dir(bundle), stagedName))
	}
}
