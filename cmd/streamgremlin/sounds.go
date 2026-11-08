package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/sounds"
)

const soundsUsage = `usage:
  streamgremlin sounds [-json]                     list the sound library
  streamgremlin sounds add [-name N] [-volume V] FILE   add an MP3, WAV, OGG or M4A (up to 5 MB)
  streamgremlin sounds play [-volume V] NAME      play it in the app's window
  streamgremlin sounds volume NAME V              set its volume, 0 to 100
  streamgremlin sounds delete NAME                delete it
  streamgremlin sounds stop                       stop every sound playing

Sounds play in the StreamGremlin app's window: the app must be open. OBS
hears them through Desktop Audio.`

type soundList struct {
	Sounds []sounds.Sound `json:"sounds"`
	Player bool           `json:"player"`
}

func soundsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, soundsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	path := func(name string) string { return "/v1/sounds/" + url.PathEscape(name) }
	usage := func() int { fmt.Fprintln(stderr, soundsUsage); return 2 }
	if len(args) > 0 {
		switch args[0] {
		case "add":
			fs := flag.NewFlagSet("sounds add", flag.ContinueOnError)
			fs.SetOutput(stderr)
			name := fs.String("name", "", "its name (default: from the file's name)")
			volume := fs.Int("volume", sounds.DefaultVolume, "0 to 100")
			if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 1 {
				return usage()
			}
			fi, err := os.Stat(fs.Arg(0))
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if fi.Size() > sounds.MaxBytes {
				fmt.Fprintf(stderr, "%s is over 5 MB\n", fs.Arg(0))
				return 1
			}
			data, err := os.ReadFile(fs.Arg(0))
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if *name == "" {
				*name = sounds.NameFromFile(fs.Arg(0))
			}
			var s sounds.Sound
			q := url.Values{"volume": {fmt.Sprint(*volume)}}
			if code := apiExit(c.DoRaw(ctx, "POST", path(*name)+"?"+q.Encode(), data, &s), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Added %s (%s, %d KB, volume %d).\n", s.Name, s.Format, max(1, s.Size/1000), s.Volume)
			return 0
		case "play":
			fs := flag.NewFlagSet("sounds play", flag.ContinueOnError)
			fs.SetOutput(stderr)
			volume := fs.Int("volume", -1, "0 to 100 (default: its own)")
			if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 1 {
				return usage()
			}
			var body any
			if *volume >= 0 {
				body = map[string]int{"volume": *volume}
			}
			var res sounds.Result
			if code := apiExit(c.Do(ctx, "POST", path(fs.Arg(0))+"/play", body, &res), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Playing %s at volume %d.\n", res.Sound, res.Volume)
			return 0
		case "volume":
			if len(args) != 3 {
				return usage()
			}
			var v int
			if _, err := fmt.Sscan(args[2], &v); err != nil {
				return usage()
			}
			var s sounds.Sound
			if code := apiExit(c.Do(ctx, "PUT", path(args[1]), map[string]int{"volume": v}, &s), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "%s plays at volume %d.\n", s.Name, s.Volume)
			return 0
		case "delete":
			if len(args) != 2 {
				return usage()
			}
			if code := apiExit(c.Do(ctx, "DELETE", path(args[1]), nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Deleted %s.\n", args[1])
			return 0
		case "stop":
			if code := apiExit(c.Do(ctx, "POST", "/v1/player/stop", nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintln(stdout, "Stopped.")
			return 0
		}
	}
	fs := flag.NewFlagSet("sounds", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return usage()
	}
	var list soundList
	if code := apiExit(c.Do(ctx, "GET", "/v1/sounds", nil, &list), stderr); code != 0 {
		return code
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return 0
	}
	if len(list.Sounds) == 0 {
		fmt.Fprintln(stdout, "No sounds yet. Add one with: streamgremlin sounds add FILE")
	}
	for _, s := range list.Sounds {
		fmt.Fprintf(stdout, "%-24s %-4s %6d KB  volume %d\n", s.Name, s.Format, max(1, s.Size/1000), s.Volume)
	}
	if list.Player {
		fmt.Fprintln(stdout, "\nThe app is open: sounds play in its window.")
	} else {
		fmt.Fprintln(stdout, "\nNo StreamGremlin window is open, so sounds can't play. Open the app.")
	}
	return 0
}
