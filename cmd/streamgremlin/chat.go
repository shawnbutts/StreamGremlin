package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/chatsend"
)

func chatCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "send" {
		fmt.Fprintln(stderr, "usage: streamgremlin chat send [-to twitch|youtube|both] [-as bot|streamer] [-reply-to MESSAGE_ID] TEXT…\n\nTo read chat: streamgremlin events watch -types chat.*")
		return 2
	}
	fs := flag.NewFlagSet("chat send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	as := fs.String("as", "", "bot or streamer (default: the bot when one is signed in)")
	replyTo := fs.String("reply-to", "", "the Twitch chat message id to reply to")
	to := fs.String("to", "twitch", "twitch, youtube or both (YouTube: 20 quota units a message)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	text := strings.Join(fs.Args(), " ")
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var res chatsend.Result
	err := c.Do(ctx, "POST", "/v1/chat/messages", chatsend.Request{Text: text, As: *as, To: chatsend.Where(*to), ReplyTo: *replyTo}, &res)
	if code := apiExit(err, stderr); code != 0 {
		return code
	}
	if t := res.Twitch; t != nil {
		fmt.Fprintf(stdout, "Sent to Twitch as %s.\n", t.Login)
		if t.Note != "" {
			fmt.Fprintln(stdout, t.Note)
		}
	}
	if y := res.YouTube; y != nil {
		fmt.Fprintf(stdout, "Sent to YouTube as %s.\n", y.Channel)
		if y.Note != "" {
			fmt.Fprintln(stdout, y.Note)
		}
	}
	for p, why := range res.Skipped {
		fmt.Fprintf(stdout, "Not sent to %s: %s\n", platformName(p), why)
	}
	for p, why := range res.Failed {
		fmt.Fprintf(stderr, "Not sent to %s: %s\n", platformName(p), why)
	}
	if len(res.Failed) > 0 {
		return 1
	}
	return 0
}

func platformName(p string) string {
	if p == "youtube" {
		return "YouTube"
	}
	return "Twitch"
}
