// Command streamgremlin is StreamGremlin's command line. It talks to the
// running engine through the same local API as the desktop app.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/buildinfo"
	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/host"
	"code.streamgremlin.com/streamgremlin/internal/logging"
)

const usage = `Usage: streamgremlin [-data-dir DIR] <command> [flags]

Commands:
  status    Show whether StreamGremlin is running, and its version and uptime.
  version   Print this program's version.
  engine    Run the engine without a window. For development and tests only;
            it is not a supported server (D-008).

The data folder defaults to $STREAMGREMLIN_DATA_DIR, else the per-user folder
for this system.
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	global := flag.NewFlagSet(brand.Slug, flag.ContinueOnError)
	global.SetOutput(stderr)
	global.Usage = func() { fmt.Fprint(stderr, usage) }
	dataDir := global.String("data-dir", "", "data folder")
	if err := global.Parse(args); err != nil {
		return 2
	}
	if global.NArg() == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := global.Arg(0), global.Args()[1:]
	if cmd == "version" {
		fmt.Fprintf(stdout, "%s %s (%s)\n", brand.Slug, buildinfo.Version, buildinfo.Commit)
		return 0
	}
	dir := *dataDir
	if dir == "" {
		d, err := datadir.Default()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		dir = d
	}
	switch cmd {
	case "status":
		return status(dir, rest, stdout, stderr)
	case "engine":
		return engine(dir, rest, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func status(dir string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var st api.Status
	c, err := client.ForDataDir(dir)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = c.Do(ctx, "GET", "/v1/status", nil, &st)
	}
	if errors.Is(err, client.ErrNotRunning) {
		fmt.Fprintf(stderr, "%s isn't running. Start the %s app.\n", brand.Name, brand.Name)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(st)
		return 0
	}
	fmt.Fprintf(stdout, "%s is running.\n", brand.Name)
	fmt.Fprintf(stdout, "  Version:      %s (%s)\n", st.Version, st.Commit)
	fmt.Fprintf(stdout, "  Running for:  %s\n", time.Duration(st.UptimeSeconds)*time.Second)
	fmt.Fprintf(stdout, "  Data folder:  %s\n", st.DataDir)
	fmt.Fprintf(stdout, "  Schema:       %d\n", st.SchemaVersion)
	return 0
}

func engine(dir string, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("engine", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "", "loopback address to listen on (default "+brand.DefaultAPIAddr+", or a free port)")
	debug := fs.Bool("debug", false, "log every API request")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := datadir.Ensure(dir); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log, closeLog, err := logging.Open(datadir.LogDir(dir), "engine", level)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = closeLog() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h, err := host.Start(ctx, host.Options{DataDir: dir, Addr: *addr, Logger: log})
	if err != nil {
		log.Error("engine failed to start", "err", err)
		return 1
	}
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-h.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Close(sctx); err != nil && runErr == nil {
		runErr = err
	}
	if runErr != nil {
		log.Error("engine stopped with an error", "err", runErr)
		return 1
	}
	return 0
}
