package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

func twitchCmd(dir string, args []string, stdout, stderr io.Writer) int {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("twitch "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	bot := fs.Bool("bot", false, "the bot account instead of yours")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	role := twitch.RoleStreamer
	if *bot {
		role = twitch.RoleBot
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch sub {
	case "status":
		var conn api.Connections
		if err := c.Do(ctx, "GET", "/v1/connections", nil, &conn); err != nil {
			reportErr(err, stderr)
			return 1
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(conn.Twitch)
			return 0
		}
		printTwitch(stdout, conn.Twitch)
		return 0
	case "client-id":
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: streamgremlin twitch client-id ID   (or \"\" to use the built-in one)")
			return 2
		}
		var conn api.Connections
		err := c.Do(ctx, "PUT", "/v1/connections/twitch/client-id", api.ClientIDRequest{ClientID: fs.Arg(0)}, &conn)
		if code := apiExit(err, stderr); code != 0 {
			return code
		}
		printTwitch(stdout, conn.Twitch)
		return 0
	case "sign-in":
		return twitchSignIn(ctx, c, role, stdout, stderr)
	case "sign-out":
		if err := c.Do(ctx, "POST", "/v1/connections/twitch/"+string(role)+"/sign-out", nil, nil); err != nil {
			reportErr(err, stderr)
			return 1
		}
		fmt.Fprintf(stdout, "Signed out the %s account.\n", role)
		return 0
	}
	fmt.Fprintf(stderr, "unknown twitch command %q; use status, sign-in, sign-out or client-id\n", sub)
	return 2
}

func apiExit(err error, stderr io.Writer) int {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && (apiErr.Code == api.CodeInvalidRequest || apiErr.Code == api.CodeConflict) {
		fmt.Fprintln(stderr, apiErr.Message)
		return 2
	}
	if err != nil {
		reportErr(err, stderr)
		return 1
	}
	return 0
}

func twitchSignIn(ctx context.Context, c *client.Client, role twitch.Role, stdout, stderr io.Writer) int {
	var p twitch.PendingSignIn
	if code := apiExit(c.Do(ctx, "POST", "/v1/connections/twitch/"+string(role)+"/sign-in", nil, &p), stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "Go to %s and enter the code %s\n", p.VerificationURI, p.UserCode)
	if role == twitch.RoleBot {
		fmt.Fprintln(stdout, "Sign in with the bot's account: use a private browser window, or sign out of twitch.tv first.")
	}
	fmt.Fprintln(stdout, "Waiting… (Ctrl+C stops waiting; the sign-in carries on in StreamGremlin)")
	for {
		select {
		case <-ctx.Done():
			return 1
		case <-time.After(time.Second):
		}
		var conn api.Connections
		if err := c.Do(ctx, "GET", "/v1/connections", nil, &conn); err != nil {
			if ctx.Err() != nil {
				return 1
			}
			reportErr(err, stderr)
			return 1
		}
		st := conn.Twitch.Streamer
		if role == twitch.RoleBot {
			st = conn.Twitch.Bot
		}
		if st.State == twitch.StateSigningIn {
			continue
		}
		if st.State == twitch.StateConnected && st.SignIn == nil && st.Detail == "" {
			fmt.Fprintf(stdout, "Signed in as %s.\n", st.Login)
			return 0
		}
		fmt.Fprintln(stderr, describeAccount(st))
		return 1
	}
}

func printTwitch(w io.Writer, s twitch.Status) {
	switch s.ClientIDSource {
	case twitch.ClientIDNone:
		fmt.Fprintln(w, "Client ID:  none. Paste yours with `streamgremlin twitch client-id ID`.")
	default:
		fmt.Fprintf(w, "Client ID:  %s (%s)\n", s.ClientID, strings.ReplaceAll(string(s.ClientIDSource), "_", "-"))
	}
	if s.TestServer != "" {
		fmt.Fprintf(w, "Test server: %s (not the real Twitch)\n", s.TestServer)
	}
	fmt.Fprintf(w, "You:        %s\n", describeAccount(s.Streamer))
	fmt.Fprintf(w, "Bot:        %s\n", describeAccount(s.Bot))
}

func describeAccount(a twitch.AccountStatus) string {
	var out string
	switch a.State {
	case twitch.StateConnected:
		out = "signed in as " + a.Login
	case twitch.StateSigningIn:
		out = "waiting for the code " + a.SignIn.UserCode + " at " + a.SignIn.VerificationURI
	case twitch.StateSignedOut:
		out = "signed out"
	case twitch.StateNotConfigured:
		out = "needs a Client ID"
	case twitch.StateNeedsSignIn:
		out = "needs signing in again"
	case twitch.StateUnknown:
		out = "unknown"
		if a.Login != "" {
			out += " (" + a.Login + ")"
		}
	}
	if a.Detail != "" {
		out += ". " + a.Detail
	}
	return out
}
