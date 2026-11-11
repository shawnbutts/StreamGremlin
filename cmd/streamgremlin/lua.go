package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const luaUsage = `usage:
  streamgremlin lua try FILE [-arg NAME=VALUE ...] [-time-limit SECONDS] [-json]
      run a Lua script once, as a "Run a Lua script" step would, and print
      what it returned and the lines it logged. "-" reads the script from
      standard input. It sees a made-up test chat message as sg.event.

In a script: sg.args holds the values passed, sg.event the event that
started the run; sg.log(...) or print(...) writes a line; what it returns
is the step's result. It runs in a separate process with a time limit
(1 second unless set; at most 30) and 64 MiB of memory, and can't reach
chat, OBS, files or the internet. math.random uses a secure random source.`

// argFlags collects -arg NAME=VALUE: a number or true/false stays one,
// anything else is text.
type argFlags map[string]any

func (a argFlags) String() string { return "" }

func (a argFlags) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok || name == "" {
		return fmt.Errorf("-arg takes NAME=VALUE")
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		a[name] = f
	} else if b, err := strconv.ParseBool(value); err == nil {
		a[name] = b
	} else {
		a[name] = value
	}
	return nil
}

func luaCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, luaUsage)
		return 0
	}
	usage := func() int { fmt.Fprintln(stderr, luaUsage); return 2 }
	if len(args) == 0 || args[0] != "try" {
		return usage()
	}
	args = args[1:]
	fs := flag.NewFlagSet("lua try", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	limit := fs.Float64("time-limit", 0, "seconds, 0.1 to 30 (default 1)")
	values := argFlags{}
	fs.Var(values, "arg", "a value to pass, NAME=VALUE (again for more)")
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return usage()
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	if len(pos) != 1 {
		return usage()
	}
	var script []byte
	var err error
	if pos[0] == "-" {
		script, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	} else {
		script, err = os.ReadFile(pos[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	body := map[string]any{"script": string(script)}
	if len(values) > 0 {
		body["args"] = map[string]any(values)
	}
	if *limit != 0 {
		body["time_limit"] = *limit
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out struct {
		OK         bool     `json:"ok"`
		Result     any      `json:"result"`
		Log        []string `json:"log"`
		LogDropped int      `json:"log_dropped"`
		Error      string   `json:"error"`
		Kind       string   `json:"kind"`
		TookMS     int64    `json:"took_ms"`
	}
	if code := apiExit(c.Do(ctx, "POST", "/v1/lua/try", body, &out), stderr); code != 0 {
		return code
	}
	if *asJSON {
		if code := printJSON(stdout, out); code != 0 || out.OK {
			return code
		}
		return 1
	}
	for _, l := range out.Log {
		fmt.Fprintln(stdout, "log:", l)
	}
	if out.LogDropped > 0 {
		fmt.Fprintf(stdout, "log: … %d more lines not kept\n", out.LogDropped)
	}
	if !out.OK {
		fmt.Fprintf(stderr, "The script failed (%s): %s\n", out.Kind, out.Error)
		return 1
	}
	b, _ := json.MarshalIndent(out.Result, "", "  ")
	fmt.Fprintf(stdout, "result: %s\n(took %d ms)\n", b, out.TookMS)
	return 0
}
