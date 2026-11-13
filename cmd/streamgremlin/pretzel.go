package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/pretzel"
)

const pretzelUsage = `usage:
  streamgremlin pretzel [-json]          ask Pretzel what's playing now

Pretzel is found by your Twitch account; there's nothing to set up. A Now playing
overlay (streamgremlin overlays add -song NAME) shows the song in OBS.`

func pretzelCmd(dir string, args []string, stdout, stderr io.Writer) int {
	asJSON := len(args) == 1 && args[0] == "-json"
	switch {
	case len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help"):
		fmt.Fprintln(stdout, pretzelUsage)
		return 0
	case len(args) > 1, len(args) == 1 && !asJSON:
		fmt.Fprintln(stderr, pretzelUsage)
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var s pretzel.Song
	if code := apiExit(c.Do(ctx, "POST", "/v1/pretzel/check", nil, &s), stderr); code != 0 {
		return code
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s)
		return 0
	}
	switch s.State {
	case pretzel.Playing:
		fmt.Fprintln(stdout, s.Chat())
	case pretzel.Other:
		fmt.Fprintf(stdout, "Pretzel says: %s\n", s.Text)
	default:
		fmt.Fprintf(stdout, "Unknown: %s\n", s.Why)
		return 1
	}
	return 0
}
