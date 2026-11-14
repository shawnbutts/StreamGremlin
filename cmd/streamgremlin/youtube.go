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
  streamgremlin youtube [status] [-json]      your and the bot's YouTube sign-in, chat, and today's quota
  streamgremlin youtube client ID [-secret]   use your own Google project's client ID ("" for the built-in one);
                                              -secret reads its client secret from standard input
  streamgremlin youtube sign-in [-bot]        sign in: open the link it prints in your browser
  streamgremlin youtube sign-out [-bot]       sign out
  streamgremlin youtube find                  ask YouTube now whether you're live, and read that chat (1 unit)
  streamgremlin youtube title TEXT            change your live broadcast's title (51 units)
  streamgremlin youtube description TEXT      change its description (51 units)
  streamgremlin youtube poll QUESTION A B [C D]  start a poll in your YouTube chat (20 units; runs until ended)
  streamgremlin youtube poll end              end it, with its votes (20 units)`

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
	case "title", "description":
		if len(positional) == 0 {
			fmt.Fprintln(stderr, youtubeUsage)
			return 2
		}
	case "poll":
		end := len(positional) == 1 && positional[0] == "end"
		if !end && (len(positional) < 3 || len(positional) > 5) {
			fmt.Fprintln(stderr, youtubeUsage)
			return 2
		}
	case "status", "sign-in", "sign-out", "find":
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
	case "find":
		return youtubeFind(ctx, c, stdout, stderr)
	case "title", "description":
		text := strings.Join(positional, " ")
		var ch youtube.StreamChange
		if sub == "title" {
			ch.Title = &text
		} else {
			ch.Description = &text
		}
		var info youtube.StreamInfo
		if code := apiExit(c.Do(ctx, "PUT", "/v1/connections/youtube/stream", ch, &info), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Changed on YouTube. Title: “%s”\n", info.Title)
	case "poll":
		var p youtube.PollInfo
		if positional[0] == "end" {
			if code := apiExit(c.Do(ctx, "POST", "/v1/youtube/polls/end", nil, &p), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Ended “%s”: %s\n", p.Question, pollVotes(p))
			return 0
		}
		req := api.YouTubePollRequest{Question: positional[0], Options: positional[1:]}
		if code := apiExit(c.Do(ctx, "POST", "/v1/youtube/polls", req, &p), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Started “%s” in your YouTube chat: %d choices. End it with `streamgremlin youtube poll end`.\n", p.Question, len(p.Choices))
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
	fmt.Fprintf(w, "Chat:           %s\n", describeYouTubeChat(s.Chat))
	if st := s.Chat.Stream; st != nil {
		viewers := "unknown"
		if st.Viewers != nil {
			viewers = fmt.Sprint(*st.Viewers)
		}
		if st.Problem != "" {
			viewers += " (" + st.Problem + ")"
		}
		fmt.Fprintf(w, "Viewers:        %s\n", viewers)
		fmt.Fprintf(w, "Title:          %s\n", st.Title)
	}
	if p := s.Chat.Poll; p != nil {
		state := "running"
		if p.Status != "active" {
			state = "ended"
		}
		fmt.Fprintf(w, "Poll:           “%s”, %s: %s\n", p.Question, state, pollVotes(*p))
	}
	fmt.Fprintf(w, "Quota:          %s; resets %s\n", quota, q.ResetsAt.Local().Format("Mon 15:04"))
}

func describeYouTubeChat(c youtube.ChatStatus) string {
	var out string
	switch c.State {
	case youtube.ChatOff:
		out = "off"
	case youtube.ChatLooking:
		out = "not live on YouTube"
		if c.NextCheck != nil {
			out += "; checking again at " + c.NextCheck.Local().Format("15:04:05")
		}
	case youtube.ChatConnecting:
		out = "connecting"
	case youtube.ChatConnected:
		out = "reading chat"
	case youtube.ChatDisconnected:
		out = "unknown: not reading chat right now"
	case youtube.ChatQuota:
		out = "paused: today's quota is used up"
	}
	if c.Broadcast != nil {
		out += " of “" + c.Broadcast.Title + "”"
	}
	if c.Detail != "" {
		out += ". " + c.Detail
	}
	return out
}

// youtubeFind asks YouTube now, and waits a few seconds for the answer.
func youtubeFind(ctx context.Context, c *client.Client, stdout, stderr io.Writer) int {
	var st youtube.Status
	if code := apiExit(c.Do(ctx, "POST", "/v1/connections/youtube/chat/find", nil, &st), stderr); code != 0 {
		return code
	}
	asked := st.Chat.CheckedAt
	answered := func() bool {
		switch st.Chat.State {
		case youtube.ChatConnected, youtube.ChatOff, youtube.ChatQuota:
			return true
		case youtube.ChatConnecting:
			return false
		}
		return st.Chat.CheckedAt != nil && (asked == nil || st.Chat.CheckedAt.After(*asked))
	}
	for range 40 {
		if answered() {
			break
		}
		select {
		case <-ctx.Done():
			return 1
		case <-time.After(250 * time.Millisecond):
		}
		if err := c.Do(ctx, "GET", "/v1/connections/youtube", nil, &st); err != nil {
			reportErr(err, stderr)
			return 1
		}
	}
	fmt.Fprintf(stdout, "Chat: %s\n", describeYouTubeChat(st.Chat))
	return 0
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

// pollVotes says a YouTube poll's votes in a line.
func pollVotes(p youtube.PollInfo) string {
	var parts []string
	for _, c := range p.Choices {
		parts = append(parts, fmt.Sprintf("%s %d", c.Title, c.Votes))
	}
	return strings.Join(parts, ", ")
}
