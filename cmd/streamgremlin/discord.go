package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/discord"
)

const discordUsage = `usage:
  streamgremlin discord                  the Discord webhooks workflows can post to
  streamgremlin discord add NAME         add a webhook under NAME: paste its link on standard input
  streamgremlin discord test NAME        post a test message through it
  streamgremlin discord forget NAME      forget it here (it stays in Discord)

In Discord: Server Settings → Integrations → Webhooks → New Webhook → Copy Webhook URL.
The link is kept with your passwords and never shown again. Post with the step "Post to Discord".`

func discordCmd(dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch {
	case sub == "help" || sub == "-h" || sub == "--help":
		fmt.Fprintln(stdout, discordUsage)
		return 0
	case sub == "" && len(args) == 0, (sub == "add" || sub == "test" || sub == "forget") && len(args) == 2:
	default:
		fmt.Fprintln(stderr, discordUsage)
		return 2
	}
	link := ""
	if sub == "add" {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintln(stderr, err)
			return 1
		}
		link = strings.TrimSpace(line)
		if link == "" {
			fmt.Fprintln(stderr, "Paste the webhook's link on standard input, like: pbpaste | streamgremlin discord add announcements")
			return 2
		}
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch sub {
	case "add":
		var wh discord.Webhook
		if code := apiExit(c.Do(ctx, "PUT", "/v1/discord/webhooks/"+url.PathEscape(args[1]), map[string]string{"link": link}, &wh), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Added %s: Discord's webhook %q. Try it with: streamgremlin discord test %s\n", wh.Name, wh.DiscordName, wh.Name)
		return 0
	case "test":
		var sent discord.Sent
		if code := apiExit(c.Do(ctx, "POST", "/v1/discord/webhooks/"+url.PathEscape(args[1])+"/test", nil, &sent), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "Posted. Look in the webhook's Discord channel.")
		return 0
	case "forget":
		if code := apiExit(c.Do(ctx, "DELETE", "/v1/discord/webhooks/"+url.PathEscape(args[1]), nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "Forgotten here. It still exists in Discord until you delete it there.")
		return 0
	}
	var out struct {
		Webhooks []discord.Webhook `json:"webhooks"`
	}
	if code := apiExit(c.Do(ctx, "GET", "/v1/discord/webhooks", nil, &out), stderr); code != 0 {
		return code
	}
	if len(out.Webhooks) == 0 {
		fmt.Fprintln(stdout, "No Discord webhooks yet. Add one: streamgremlin discord add NAME (paste its link).")
		return 0
	}
	for _, w := range out.Webhooks {
		fmt.Fprintf(stdout, "  %-20s Discord's %q, added %s\n", w.Name, w.DiscordName, w.AddedAt.Local().Format("Jan 2 2006"))
	}
	return 0
}
