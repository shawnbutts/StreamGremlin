package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/obs"
)

const obsUsage = `usage:
  streamgremlin obs [status] [-json]
  streamgremlin obs connect [-url ws://127.0.0.1:4455] [-password-stdin] [-clear-password]
  streamgremlin obs disconnect
  streamgremlin obs scenes
  streamgremlin obs switch SCENE
  streamgremlin obs sources SCENE
  streamgremlin obs show|hide SCENE SOURCE
  streamgremlin obs snapshot [list] | take KEY SCENE | restore KEY | release KEY`

func obsCmd(dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	do := func(method, path string, body, out any) int {
		return apiExit(c.Do(ctx, method, path, body, out), stderr)
	}
	need := func(n int) bool {
		if len(args) != n {
			fmt.Fprintln(stderr, obsUsage)
			return false
		}
		return true
	}
	switch sub {
	case "status":
		fs := flag.NewFlagSet("obs status", flag.ContinueOnError)
		fs.SetOutput(stderr)
		asJSON := fs.Bool("json", false, "print JSON")
		if fs.Parse(args) != nil {
			return 2
		}
		var conn api.Connections
		if code := do("GET", "/v1/connections", nil, &conn); code != 0 {
			return code
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(conn.OBS)
			return 0
		}
		printOBS(stdout, conn.OBS)
		return 0
	case "connect", "disconnect":
		fs := flag.NewFlagSet("obs "+sub, flag.ContinueOnError)
		fs.SetOutput(stderr)
		var conn api.Connections
		if code := do("GET", "/v1/connections", nil, &conn); code != 0 {
			return code
		}
		u := fs.String("url", conn.OBS.URL, "OBS's WebSocket address (OBS: Tools → WebSocket Server Settings)")
		pwStdin := fs.Bool("password-stdin", false, "read the password from standard input")
		clear := fs.Bool("clear-password", false, "forget the saved password")
		if fs.Parse(args) != nil {
			return 2
		}
		req := api.OBSConfigRequest{URL: *u, Enabled: sub == "connect", ClearPassword: *clear}
		if *pwStdin {
			line, err := bufio.NewReader(stdin).ReadString('\n')
			if err != nil && err != io.EOF {
				fmt.Fprintln(stderr, err)
				return 1
			}
			pw := strings.TrimRight(line, "\r\n")
			req.Password = &pw
		}
		var st obs.Status
		if code := do("PUT", "/v1/connections/obs", req, &st); code != 0 {
			return code
		}
		if sub == "connect" {
			// Give it a moment, then say how it went.
			for i := 0; i < 20 && (st.State == obs.StateConnecting || st.State == obs.StateOff); i++ {
				time.Sleep(250 * time.Millisecond)
				var conn api.Connections
				if do("GET", "/v1/connections", nil, &conn) != 0 {
					return 1
				}
				st = conn.OBS
			}
		}
		printOBS(stdout, st)
		if sub == "connect" && st.State != obs.StateConnected {
			return 1
		}
		return 0
	case "scenes":
		var sc api.OBSScenes
		if code := do("GET", "/v1/obs/scenes", nil, &sc); code != 0 {
			return code
		}
		for _, name := range sc.Scenes {
			mark := "  "
			if name == sc.Current {
				mark = "▶ "
			}
			fmt.Fprintln(stdout, mark+name)
		}
		return 0
	case "switch":
		if !need(1) {
			return 2
		}
		if code := do("POST", "/v1/obs/scene", map[string]string{"scene": args[0]}, nil); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Switched to %s.\n", args[0])
		return 0
	case "sources":
		if !need(1) {
			return 2
		}
		var out struct{ Items []obs.SceneItem }
		if code := do("GET", "/v1/obs/scene-items?scene="+url.QueryEscape(args[0]), nil, &out); code != 0 {
			return code
		}
		for _, it := range out.Items {
			state := "shown"
			if !it.Visible {
				state = "hidden"
			}
			fmt.Fprintf(stdout, "%-7s %s\n", state, it.Source)
		}
		return 0
	case "show", "hide":
		if !need(2) {
			return 2
		}
		body := map[string]any{"scene": args[0], "source": args[1], "visible": sub == "show"}
		if code := do("PUT", "/v1/obs/scene-items/visibility", body, nil); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "%s is %s in %s.\n", args[1], map[bool]string{true: "shown", false: "hidden"}[sub == "show"], args[0])
		return 0
	case "snapshot":
		return obsSnapshot(args, do, stdout, stderr)
	}
	fmt.Fprintln(stderr, obsUsage)
	return 2
}

func obsSnapshot(args []string, do func(string, string, any, any) int, stdout, stderr io.Writer) int {
	op := "list"
	if len(args) > 0 {
		op, args = args[0], args[1:]
	}
	var snap obs.Snapshot
	switch {
	case op == "list" && len(args) == 0:
		var out struct{ Snapshots []obs.Snapshot }
		if code := do("GET", "/v1/obs/snapshots", nil, &out); code != 0 {
			return code
		}
		if len(out.Snapshots) == 0 {
			fmt.Fprintln(stdout, "No open snapshots.")
		}
		for _, s := range out.Snapshots {
			line := fmt.Sprintf("%s: on %s, back to %s", s.Key, s.TargetScene, s.PreviousScene)
			if s.WouldRestore != nil && !*s.WouldRestore {
				line += " (wouldn't switch back: " + s.Message + ")"
			}
			fmt.Fprintln(stdout, line)
		}
		return 0
	case op == "take" && len(args) == 2:
		if code := do("POST", "/v1/obs/snapshots", map[string]string{"key": args[0], "scene": args[1]}, &snap); code != 0 {
			return code
		}
	case (op == "restore" || op == "release") && len(args) == 1:
		if code := do("POST", "/v1/obs/snapshots/"+url.PathEscape(args[0])+"/"+op, nil, &snap); code != 0 {
			return code
		}
	default:
		fmt.Fprintln(stderr, obsUsage)
		return 2
	}
	switch snap.Status {
	case obs.SnapOpen:
		fmt.Fprintf(stdout, "On %s; %s remembers %s.\n", snap.TargetScene, snap.Key, snap.PreviousScene)
	case obs.SnapRestored:
		fmt.Fprintf(stdout, "Switched back to %s.\n", snap.PreviousScene)
	case obs.SnapReleased:
		fmt.Fprintln(stdout, "Released; staying on the current scene.")
	default:
		fmt.Fprintf(stdout, "Didn't switch back: %s\n", snap.Message)
	}
	return 0
}

func printOBS(w io.Writer, s obs.Status) {
	state := string(s.State)
	if s.State == obs.StateConnected {
		state += fmt.Sprintf(" (OBS %s, obs-websocket %s), on %s", s.OBSVersion, s.WebSocketVersion, s.Scene)
	}
	if s.Detail != "" {
		state += ". " + s.Detail
	}
	fmt.Fprintf(w, "OBS:       %s\nAddress:   %s\nPassword:  %s\n", state, s.URL, map[bool]string{true: "saved", false: "none"}[s.HasPassword])
}
