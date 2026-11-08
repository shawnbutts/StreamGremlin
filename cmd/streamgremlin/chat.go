package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

func chatCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "send" {
		fmt.Fprintln(stderr, "usage: streamgremlin chat send [-as bot|streamer] [-reply-to MESSAGE_ID] TEXT…\n\nTo read chat: streamgremlin events watch -types chat.*")
		return 2
	}
	fs := flag.NewFlagSet("chat send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	as := fs.String("as", "", "bot or streamer (default: the bot when one is signed in)")
	replyTo := fs.String("reply-to", "", "the chat message id to reply to")
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
	var res twitch.SendResult
	err := c.Do(ctx, "POST", "/v1/chat/messages", twitch.SendRequest{Text: text, As: twitch.Sender(*as), ReplyTo: *replyTo}, &res)
	if code := apiExit(err, stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "Sent as %s.\n", res.Login)
	if res.Note != "" {
		fmt.Fprintln(stdout, res.Note)
	}
	return 0
}
