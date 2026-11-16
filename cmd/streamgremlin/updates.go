package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/updates"
)

const updatesUsage = `streamgremlin updates: whether a newer StreamGremlin is out (D-120).

  updates            what's running, the last check, and any newer version
  updates check      check GitHub now
  updates on|off     check at start and once a day, or not
  updates download   download the newer version and check its signature

Installing is in the desktop app (Settings, Updates): it closes
StreamGremlin, installs the update and opens it again.`

func updatesCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "check" && args[0] != "on" && args[0] != "off" && args[0] != "download") {
		fmt.Fprintln(stderr, updatesUsage)
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var st updates.Status
	var err error
	switch {
	case len(args) == 0:
		err = c.Do(ctx, "GET", "/v1/updates", nil, &st)
	case args[0] == "check":
		err = c.Do(ctx, "POST", "/v1/updates/check", nil, &st)
	case args[0] == "download":
		err = c.Do(ctx, "POST", "/v1/updates/download", nil, &st)
	default:
		var set updates.Settings
		if code := apiExit(c.Do(ctx, "PUT", "/v1/settings/updates", map[string]bool{"check": args[0] == "on"}, &set), stderr); code != 0 {
			return code
		}
		if set.Check {
			fmt.Fprintln(stdout, "StreamGremlin checks for updates at start and once a day.")
		} else {
			fmt.Fprintln(stdout, "StreamGremlin doesn't check for updates. Run `streamgremlin updates check` to check now.")
		}
		return 0
	}
	if code := apiExit(err, stderr); code != 0 {
		return code
	}
	printUpdates(stdout, st)
	return 0
}

func printUpdates(w io.Writer, st updates.Status) {
	fmt.Fprintf(w, "This is StreamGremlin %s.\n", st.Version)
	if st.TestServer {
		fmt.Fprintln(w, "Checking a test server, not GitHub.")
	}
	switch st.State {
	case updates.StateDev:
		fmt.Fprintln(w, "Development builds don't check for updates.")
	case updates.StateOff:
		fmt.Fprintln(w, "Checking for updates is off.")
	case updates.StateNotChecked:
		fmt.Fprintln(w, "Not checked yet.")
	case updates.StateChecking:
		fmt.Fprintln(w, "Checking…")
	case updates.StateUpToDate:
		fmt.Fprintln(w, "It's the newest version.")
	case updates.StateFailed:
		fmt.Fprintln(w, "The last check failed:", st.Error)
	}
	if l := st.Latest; l != nil {
		fmt.Fprintf(w, "StreamGremlin %s is out: %s\n", l.Version, l.URL)
		if !l.Installable {
			fmt.Fprintln(w, l.Why)
		}
	}
	switch d := st.Download; d.State {
	case updates.DownloadRunning:
		fmt.Fprintf(w, "Downloading %s: %d of %d bytes.\n", d.Version, d.Done, d.Total)
	case updates.DownloadReady:
		fmt.Fprintf(w, "%s is downloaded and its signature checked. Install it from Settings in the app.\n", d.Version)
	case updates.DownloadFailed:
		fmt.Fprintf(w, "Downloading %s failed: %s\n", d.Version, d.Error)
	case updates.DownloadInstalling:
		fmt.Fprintf(w, "Installing %s.\n", d.Version)
	}
}
