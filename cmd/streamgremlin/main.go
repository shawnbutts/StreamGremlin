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
	"code.streamgremlin.com/streamgremlin/internal/lua"
	"code.streamgremlin.com/streamgremlin/internal/overview"
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
  templates Ready-made workflows: templates, templates show ID.
  backups   Backups: list, create, verify, restore, delete (streamgremlin backups help).
  timers    Timers that start workflows every so often (streamgremlin timers help).
            timers [-json]                list, with when each goes off next
            timers run|on|off|delete ID   run now, turn on or off, delete
            timers set ID -name N -every 15m [-lines N] [-live] [-once]
  buttons   Your buttons, which run workflows (streamgremlin buttons help).
            buttons [-json]               list, with the workflows each starts
            buttons press|delete ID       press one (its workflows run), delete
            buttons set ID -label TEXT [-color NAME], buttons move ID...
  streamdeck Pair the Stream Deck plugin (streamgremlin streamdeck help).
            streamdeck [-json]            list paired Stream Decks
            streamdeck pair               show a code to type into the plugin
            streamdeck forget ID          unpair one
  mod       Moderate your Twitch chat as you, each recorded (streamgremlin mod help).
            mod timeout @NAME 10m, mod ban @NAME -yes, mod unban|warn|vip|mod @NAME
            mod delete MESSAGE_ID, mod modes [slow=30 ...], mod history [@NAME]
  filters   The chat filters: settings and what they caught lately.
            filters check [-role vip] TEXT   which filter would catch it
  discord   Discord webhooks workflows post to (streamgremlin discord help).
  quotes    Numbered quotes (streamgremlin quotes help).
            quotes [-q TEXT]              list, newest first
            quotes add [-by NAME] TEXT    add one
            quotes show|delete N, quotes edit N TEXT
  sounds    The sound library (streamgremlin sounds help).
            sounds                        list
            sounds add [-name N] FILE     add one
            sounds play|delete NAME, sounds volume NAME V, sounds stop
  overlays  Alert overlays for OBS (streamgremlin overlays help).
            overlays                      list
            overlays add NAME             add one, and print its link
            overlays link|new-link|test|delete NAME, overlays look NAME [flags]
  packages  Add-ons: wheel looks installed from a ZIP (streamgremlin packages help).
            packages                      list
            packages install FILE [-yes]  say what it is; -yes installs it
            packages remove ID [-yes], packages history
  lua       Try a Lua script as a "Run a Lua script" step would (streamgremlin lua help).
            lua try FILE [-arg NAME=VALUE] [-time-limit S]
  rewards   Channel point rewards (streamgremlin rewards help).
            rewards                       list
            rewards add -cost N TITLE     make one
            rewards set|queue|delete TITLE, rewards done|refund TITLE ID
  first-words  Viewers' first messages of the stream: first-words, first-words reset.
  polls     Polls and predictions (streamgremlin polls help).
  ads       Ad breaks: ads, ads run SECONDS, ads snooze (streamgremlin ads help).
  giveaways Chat giveaways: open, close, draw, end, verify (streamgremlin giveaways help).
  wheels    Wheels of choices: add, spin, verify (streamgremlin wheels help).
  viewers   Viewers you've seen: find, show, export, forget (streamgremlin viewers help).
  points    Viewers' points: give, take, undo, rename, audit (streamgremlin points help).
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

func main() {
	lua.RunWorkerIfAsked() // the engine runs Lua scripts in copies of this binary
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

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
	case "backups":
		return backupsCmd(dir, rest, stdout, stderr)
	case "templates":
		return templatesCmd(dir, rest, stdout, stderr)
	case "commands":
		return commandsCmd(dir, rest, stdout, stderr)
	case "sounds":
		return soundsCmd(dir, rest, stdout, stderr)
	case "overlays":
		return overlaysCmd(dir, rest, stdout, stderr)
	case "packages":
		return packagesCmd(dir, rest, stdout, stderr)
	case "lua":
		return luaCmd(dir, rest, stdout, stderr)
	case "rewards":
		return rewardsCmd(dir, rest, stdout, stderr)
	case "first-words":
		return firstWordsCmd(dir, rest, stdout, stderr)
	case "polls":
		return pollsCmd(dir, rest, stdout, stderr)
	case "ads":
		return adsCmd(dir, rest, stdout, stderr)
	case "giveaways":
		return giveawaysCmd(dir, rest, stdout, stderr)
	case "wheels":
		return wheelsCmd(dir, rest, stdout, stderr)
	case "viewers":
		return viewersCmd(dir, rest, stdout, stderr)
	case "points":
		return pointsCmd(dir, rest, stdout, stderr)
	case "quotes":
		return quotesCmd(dir, rest, stdout, stderr)
	case "timers":
		return timersCmd(dir, rest, stdout, stderr)
	case "buttons":
		return buttonsCmd(dir, rest, stdout, stderr)
	case "streamdeck":
		return streamdeckCmd(dir, rest, stdout, stderr)
	case "mod":
		return modCmd(dir, rest, stdout, stderr)
	case "filters":
		return filtersCmd(dir, rest, stdout, stderr)
	case "discord":
		return discordCmd(dir, rest, os.Stdin, stdout, stderr)
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
	// What Home shows: anything that needs you, and setup left to do.
	var ov overview.Overview
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c.Do(ctx, "GET", "/v1/overview", nil, &ov) != nil {
		return 0
	}
	if len(ov.Attention) > 0 {
		fmt.Fprintln(stdout, "\nNeeds you:")
		for _, a := range ov.Attention {
			mark := map[string]string{overview.Problem: "!", overview.Unknown: "?", overview.Tip: "-"}[a.Level]
			fmt.Fprintf(stdout, "  %s %s\n", mark, a.Message)
		}
	}
	if !ov.SetUp {
		fmt.Fprintln(stdout, "\nGet set up (in the app's Home):")
		for _, s := range ov.Checklist {
			box := "[ ]"
			if s.Done {
				box = "[x]"
			}
			opt := ""
			if s.Optional {
				opt = " (optional)"
			}
			fmt.Fprintf(stdout, "  %s %s%s\n", box, s.Label, opt)
		}
	}
	return 0
}

func engine(dir string, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("engine", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "", "loopback address to listen on (default "+brand.DefaultAPIAddr+", or a free port)")
	overlayAddr := fs.String("overlay-addr", "", "loopback address for OBS overlays (default "+brand.DefaultOverlayAddr+", or a free port); off for none")
	debug := fs.Bool("debug", false, "log every API request")
	twitchURL := fs.String("twitch-auth-url", os.Getenv(brand.Env("TWITCH_AUTH_URL")),
		"a loopback stand-in for id.twitch.tv, such as mocktwitch (default $"+brand.Env("TWITCH_AUTH_URL")+")")
	discordURL := fs.String("discord-api-url", os.Getenv(brand.Env("DISCORD_API_URL")),
		"a loopback stand-in for discord.com's webhooks, such as mockdiscord (default $"+brand.Env("DISCORD_API_URL")+")")
	minute := fs.Duration("test-minute", 0, "for tests against a stand-in Twitch only: how long a minute of time in chat lasts")
	store := fs.String("secret-store", os.Getenv(brand.Env("SECRET_STORE")),
		"auto, keyring or file (default auto, or $"+brand.Env("SECRET_STORE")+")")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *minute != 0 && (*twitchURL == "" || *minute < 100*time.Millisecond || *minute > time.Minute) {
		fmt.Fprintln(stderr, "-test-minute is for tests against a stand-in Twitch (-twitch-auth-url), from 100ms to 1m.")
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
	h, err := host.Start(ctx, host.Options{DataDir: dir, Addr: *addr, SecretStore: kind, TwitchAuthURL: *twitchURL, DiscordAPIURL: *discordURL, OverlayAddr: *overlayAddr,
		TestMinute: *minute, Logger: log})
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
