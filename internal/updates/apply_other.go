//go:build !darwin && !windows

package updates

import "errors"

// Linux packages need the system's package manager, usually as root, so
// the app only says a new version is out (D-120).
func apply(string) error {
	return errors.New("on this system, install the update from the release page")
}

func cleanup() {}
