package updates

import (
	"os/exec"
	"syscall"
)

// On Windows the download is the release's installer. It runs silently
// with /update: it waits for StreamGremlin to quit, installs over it where
// it is, keeps the desktop shortcut only if there was one, and opens
// StreamGremlin again (packaging/windows/StreamGremlin.nsi). It installs
// for the current user, with no administrator prompt.

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

func apply(file string) error {
	cmd := exec.Command(file, "/S", "/update")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func cleanup() {}
