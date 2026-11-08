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
	"code.streamgremlin.com/streamgremlin/internal/secrets"
)

const usage = `Usage: streamgremlin [-data-dir DIR] <command> [flags]

Commands:
  status    Show whether StreamGremlin is running, and its version and uptime.
  secrets   List saved sign-ins and passwords (never their values).
            secrets [-check] [-json]      list; -check tries reading each
            secrets forget KEY            forget one
  twitch    Twitch sign-ins.
            twitch [status] [-json]       your and the bot's sign-in
            twitch sign-in [-bot]         sign in with a code at twitch.tv/activate
            twitch sign-out [-bot]        sign out
            twitch client-id ID           use your own Twitch app ("" for the built-in one)
  obs       OBS: connect, scenes, sources, snapshots (streamgremlin obs help).
  workflows Workflows: list, show, save FILE, on/off, delete, deleted, restore
            (streamgremlin workflows help).
  runs      Workflow runs and their traces: runs, runs show RUN, runs cancel RUN.
  commands  Chat commands and the workflow that answers each.
  vars      Saved variables (streamgremlin vars help).
            vars [-q TEXT] [WHERE]        list: global, viewer[:ID], workflow[:ID]
            vars set WHERE NAME VALUE     save one
            vars delete WHERE NAME        delete one
  chat      Send to your Twitch chat (read it with: events watch -types chat.*).
            chat send [-as bot|streamer] TEXT
  events    Show events as they arrive, and send test events.
            events [-limit N] [-types chat.*] [-json]      the newest
            events watch [-after SEQ] [-types ...] [-json] follow live
            events simulate TYPE [field=value ...]         send a test event
            events types                                   types and fields
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
	case "obs":
		return obsCmd(dir, rest, os.Stdin, stdout, stderr)
	case "workflows":
		return workflowsCmd(dir, rest, stdout, stderr)
	case "runs":
		return runsCmd(dir, rest, stdout, stderr)
	case "commands":
		return commandsCmd(dir, rest, stdout, stderr)
	case "vars":
		return varsCmd(dir, rest, stdout, stderr)
	case "chat":
		return chatCmd(dir, rest, stdout, stderr)
	case "twitch":
		return twitchCmd(dir, rest, stdout, stderr)
	case "events":
		return eventsCmd(dir, rest, stdout, stderr)
	case "secrets":
		return secretsCmd(dir, rest, stdout, stderr)
	case "engine":
		return engine(dir, rest, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

// connect finds the running engine, or explains that it isn't running.
func connect(dir string, stderr io.Writer) (*client.Client, bool) {
	c, err := client.ForDataDir(dir)
	if err != nil {
		reportErr(err, stderr)
		return nil, false
	}
	return c, true
}

func reportErr(err error, stderr io.Writer) {
	if errors.Is(err, client.ErrNotRunning) {
		fmt.Fprintf(stderr, "%s isn't running. Start the %s app.\n", brand.Name, brand.Name)
		return
	}
	fmt.Fprintln(stderr, err)
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
	twitchURL := fs.String("twitch-auth-url", os.Getenv(brand.Env("TWITCH_AUTH_URL")),
		"a loopback stand-in for id.twitch.tv, such as mocktwitch (default $"+brand.Env("TWITCH_AUTH_URL")+")")
	store := fs.String("secret-store", os.Getenv(brand.Env("SECRET_STORE")),
		"auto, keyring or file (default auto, or $"+brand.Env("SECRET_STORE")+")")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	kind, err := secrets.ParseKind(*store)
	if err != nil {
		fmt.Fprintln(stderr, err)
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
	h, err := host.Start(ctx, host.Options{DataDir: dir, Addr: *addr, SecretStore: kind, TwitchAuthURL: *twitchURL, Logger: log})
	if err != nil {
		log.Error("engine failed to start", "err", err)
		fmt.Fprintln(stderr, err)
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
