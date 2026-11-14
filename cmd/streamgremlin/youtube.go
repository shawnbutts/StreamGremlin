package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

const youtubeUsage = `usage:
  streamgremlin youtube [status] [-json]      your and the bot's YouTube sign-in, and today's quota
  streamgremlin youtube client ID [-secret]   use your own Google project's client ID ("" for the built-in one);
                                              -secret reads its client secret from standard input
  streamgremlin youtube sign-in [-bot]        sign in: open the link it prints in your browser
  streamgremlin youtube sign-out [-bot]       sign out`

func youtubeCmd(dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	if sub == "help" {
		fmt.Fprintln(stdout, youtubeUsage)
		return 0
	}
	fs := flag.NewFlagSet("youtube "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	bot := fs.Bool("bot", false, "the bot account instead of yours")
	asJSON := fs.Bool("json", false, "print JSON")
	withSecret := fs.Bool("secret", false, "read the client secret from standard input")
	// The client ID comes before or after the flags.
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return 2
		}
		args = fs.Args()
		if len(args) > 0 {
			positional, args = append(positional, args[0]), args[1:]
		}
	}
	role := youtube.RoleStreamer
	if *bot {
		role = youtube.RoleBot
	}
	in := api.YouTubeClientRequest{}
	switch sub {
	case "status", "sign-in", "sign-out":
		if len(positional) != 0 {
			fmt.Fprintln(stderr, youtubeUsage)
			return 2
		}
	case "client":
		if len(positional) != 1 {
			fmt.Fprintln(stderr, youtubeUsage)
			return 2
		}
		in.ClientID = positional[0]
		if *withSecret {
			line, err := bufio.NewReader(stdin).ReadString('\n')
			if err != nil && err != io.EOF {
				fmt.Fprintln(stderr, err)
				return 1
			}
			in.ClientSecret = strings.TrimSpace(line)
			if in.ClientSecret == "" {
				fmt.Fprintln(stderr, "Paste the client secret on standard input, like: pbpaste | streamgremlin youtube client ID -secret")
				return 2
			}
		}
	default:
		fmt.Fprintf(stderr, "unknown youtube command %q\n%s\n", sub, youtubeUsage)
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch sub {
	case "status":
		var st youtube.Status
		if err := c.Do(ctx, "GET", "/v1/connections/youtube", nil, &st); err != nil {
			reportErr(err, stderr)
			return 1
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(st)
			return 0
		}
		printYouTube(stdout, st)
	case "client":
		var st youtube.Status
		if code := apiExit(c.Do(ctx, "PUT", "/v1/connections/youtube/client", in, &st), stderr); code != 0 {
			return code
		}
		printYouTube(stdout, st)
	case "sign-in":
		return youtubeSignIn(ctx, c, role, stdout, stderr)
	case "sign-out":
		if err := c.Do(ctx, "POST", "/v1/connections/youtube/"+string(role)+"/sign-out", nil, nil); err != nil {
			reportErr(err, stderr)
			return 1
		}
		fmt.Fprintf(stdout, "Signed out the %s's YouTube account.\n", role)
	}
	return 0
}

func youtubeSignIn(ctx context.Context, c *client.Client, role youtube.Role, stdout, stderr io.Writer) int {
	var p youtube.PendingSignIn
	if code := apiExit(c.Do(ctx, "POST", "/v1/connections/youtube/"+string(role)+"/sign-in", nil, &p), stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "Open this in your browser, on this computer, and sign in with Google:\n%s\n", p.AuthURL)
	if role == youtube.RoleBot {
		fmt.Fprintln(stdout, "Pick the bot's channel in Google's list, not your own.")
	}
	fmt.Fprintln(stdout, "Waiting… (Ctrl+C stops waiting; the sign-in carries on in StreamGremlin)")
	for {
		select {
		case <-ctx.Done():
			return 1
		case <-time.After(500 * time.Millisecond):
		}
		var st youtube.Status
		if err := c.Do(ctx, "GET", "/v1/connections/youtube", nil, &st); err != nil {
			if ctx.Err() != nil {
				return 1
			}
			reportErr(err, stderr)
			return 1
		}
		a := st.Streamer
		if role == youtube.RoleBot {
			a = st.Bot
		}
		if a.State == youtube.StateSigningIn {
			continue
		}
		if a.State == youtube.StateConnected && a.Detail == "" && a.Channel != nil {
			fmt.Fprintf(stdout, "Signed in as %s.\n", a.Channel.Title)
			return 0
		}
		fmt.Fprintln(stderr, describeYouTube(a))
		return 1
	}
}

func printYouTube(w io.Writer, s youtube.Status) {
	switch s.ClientSource {
	case youtube.ClientNone:
		fmt.Fprintln(w, "Google project: none. Add yours with `streamgremlin youtube client ID` (Connections has a guide).")
	case youtube.ClientBuiltIn:
		fmt.Fprintln(w, "Google project: StreamGremlin's")
	default:
		secret := "no client secret"
		if s.HasSecret {
			secret = "with its client secret"
		}
		fmt.Fprintf(w, "Google project: yours, %s (%s)\n", s.ClientID, secret)
	}
	if s.TestServer != "" {
		fmt.Fprintf(w, "Test server:    %s (not the real Google or YouTube)\n", s.TestServer)
	}
	fmt.Fprintf(w, "You:            %s\n", describeYouTube(s.Streamer))
	fmt.Fprintf(w, "Bot:            %s\n", describeYouTube(s.Bot))
	q := s.Quota
	quota := fmt.Sprintf("about %d of %d units used today", q.Used, q.Limit)
	if q.Exhausted {
		quota = "used up: YouTube said so"
	}
	fmt.Fprintf(w, "Quota:          %s; resets %s\n", quota, q.ResetsAt.Local().Format("Mon 15:04"))
}

func describeYouTube(a youtube.AccountStatus) string {
	var out string
	switch a.State {
	case youtube.StateConnected:
		out = "signed in as " + a.Channel.Title
		if a.Channel.Handle != "" {
			out += " (" + a.Channel.Handle + ")"
		}
	case youtube.StateSigningIn:
		out = "waiting for Google in your browser"
	case youtube.StateSignedOut:
		out = "signed out"
	case youtube.StateNotConfigured:
		out = "needs a Google project's client ID"
	case youtube.StateNeedsSignIn:
		out = "needs signing in again"
	case youtube.StateUnknown:
		out = "unknown"
		if a.Channel != nil {
			out += " (" + a.Channel.Title + ")"
		}
	}
	if a.Detail != "" {
		out += ". " + a.Detail
	}
	return out
}
