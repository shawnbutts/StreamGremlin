package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/moderation"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

const modUsage = `usage:
  streamgremlin mod timeout @NAME DURATION [-reason R]   time out: 30s, 10m, 1h, 2d, up to 2 weeks
  streamgremlin mod ban @NAME [-reason R] [-yes]         ban for good; without -yes, only says what it would do
  streamgremlin mod unban @NAME                          lift a ban or timeout
  streamgremlin mod warn @NAME REASON                    a warning they must acknowledge before chatting
  streamgremlin mod delete MESSAGE_ID                    delete one chat message (from events -types chat.*)
  streamgremlin mod shoutout @NAME                       Twitch's shoutout
  streamgremlin mod vip|unvip|mod|unmod @NAME            make or stop being a VIP or moderator
  streamgremlin mod monitor|restrict|unsuspect @NAME     how Twitch treats a suspicious user
  streamgremlin mod modes                                the chat modes now
  streamgremlin mod modes [emote=on|off] [followers=off|MINUTES] [slow=off|SECONDS] [subs=on|off] [unique=on|off]
  streamgremlin mod history [@NAME]                      what was done from StreamGremlin, newest first

  streamgremlin mod -youtube timeout|ban|unban|mod|unmod CHANNEL_ID [DURATION]
  streamgremlin mod -youtube delete MESSAGE_ID
  streamgremlin mod -youtube history [CHANNEL_ID]

Each is sent once, as you, and recorded with the answer. @NAME is someone
StreamGremlin has seen in chat. If there's no answer, it's not sent again:
check chat. On YouTube (your live chat, by channel id: UC…) a delete costs 20
of the day's quota units and the rest 200; only a ban or timeout made from
StreamGremlin can be undone, and YouTube takes no reason.`

func modCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, modUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	usage := func() int { fmt.Fprintln(stderr, modUsage); return 2 }
	platform := moderation.PlatformTwitch
	if args[0] == "-youtube" {
		platform, args = moderation.PlatformYouTube, args[1:]
		if len(args) == 0 {
			return usage()
		}
		switch args[0] {
		case "timeout", "ban", "unban", "mod", "unmod", "delete", "history":
		default:
			fmt.Fprintf(stderr, "YouTube can't %s: it can time out, ban, unban, delete, and add or remove moderators.\n", args[0])
			return 2
		}
	}
	sub, args := args[0], args[1:]
	send := func(req moderation.Request, what string) int {
		req.RequestID, req.Platform, req.From = events.NewID(), platform, moderation.FromCLI
		var res api.ModerationResult
		if code := apiExit(c.Do(ctx, "POST", "/v1/moderation", req, &res), stderr); code != 0 {
			return code
		}
		switch res.Record.State {
		case moderation.Done:
			fmt.Fprintln(stdout, what)
			return 0
		case moderation.Unknown:
			fmt.Fprintln(stderr, "Unknown: "+res.Record.Answer)
		default:
			fmt.Fprintln(stderr, "Not done: "+res.Record.Answer)
		}
		return 1
	}
	who := func(name string) (api.ViewerView, int) {
		if platform == moderation.PlatformYouTube {
			// YouTube viewers are named by channel id until they have records (M6 slice 6).
			v := api.ViewerView{}
			v.UserID, v.DisplayName = strings.TrimPrefix(name, "@"), strings.TrimPrefix(name, "@")
			return v, 0
		}
		return findViewer(ctx, c, name, stderr)
	}
	switch sub {
	case "timeout", "ban":
		fs := flag.NewFlagSet(sub, flag.ContinueOnError)
		fs.SetOutput(stderr)
		reason := fs.String("reason", "", "why, shown to them")
		yes := fs.Bool("yes", false, "ban, rather than only saying what it would do")
		if fs.Parse(reorder(args)) != nil || (sub == "timeout" && fs.NArg() != 2) || (sub == "ban" && fs.NArg() != 1) {
			return usage()
		}
		v, code := who(fs.Arg(0))
		if code != 0 {
			return code
		}
		req := moderation.Request{Action: sub, UserID: v.UserID, Login: v.Login, Reason: *reason}
		if sub == "timeout" {
			d, err := time.ParseDuration(strings.Replace(fs.Arg(1), "d", "h", 1))
			if strings.HasSuffix(fs.Arg(1), "d") && err == nil {
				d *= 24
			}
			if err != nil || d < time.Second || d > twitch.MaxTimeout*time.Second {
				fmt.Fprintln(stderr, "A timeout is 1s to 2 weeks, like 30s, 10m, 1h or 2d.")
				return 2
			}
			req.Seconds = int(d / time.Second)
			return send(req, fmt.Sprintf("Timed out %s for %s.", v.DisplayName, d))
		}
		if !*yes {
			fmt.Fprintf(stdout, "This bans %s from your chat until you unban them. Add -yes to ban.\n", v.DisplayName)
			return 0
		}
		return send(req, "Banned "+v.DisplayName+".")
	case "unban", "shoutout", "vip", "unvip", "mod", "unmod", "monitor", "restrict", "unsuspect":
		if len(args) != 1 {
			return usage()
		}
		v, code := who(args[0])
		if code != 0 {
			return code
		}
		done := map[string]string{"unban": "Unbanned %s.", "shoutout": "Shouted out %s.", "vip": "%s is a VIP.", "unvip": "%s isn't a VIP now.",
			"mod": "%s is a moderator.", "unmod": "%s isn't a moderator now.", "monitor": "Twitch is monitoring %s.",
			"restrict": "Twitch restricts %s: only you and your mods see their messages.", "unsuspect": "Twitch treats %s as usual now."}[sub]
		return send(moderation.Request{Action: sub, UserID: v.UserID, Login: v.Login}, fmt.Sprintf(done, v.DisplayName))
	case "warn":
		if len(args) < 2 {
			return usage()
		}
		v, code := who(args[0])
		if code != 0 {
			return code
		}
		return send(moderation.Request{Action: "warn", UserID: v.UserID, Login: v.Login, Reason: strings.Join(args[1:], " ")}, "Warned "+v.DisplayName+".")
	case "delete":
		if len(args) != 1 {
			return usage()
		}
		return send(moderation.Request{Action: "delete", MessageID: args[0]}, "Deleted the message.")
	case "modes":
		if len(args) == 0 {
			var v api.ChatModesView
			if code := apiExit(c.Do(ctx, "GET", "/v1/chat/settings", nil, &v), stderr); code != 0 {
				return code
			}
			if v.Settings == nil {
				fmt.Fprintln(stderr, "Unknown: "+v.Problem)
				return 1
			}
			fmt.Fprintln(stdout, modesWords(*v.Settings))
			return 0
		}
		var ch twitch.ChatSettingsChange
		on, off := true, false
		for _, a := range args {
			k, val, ok := strings.Cut(a, "=")
			if !ok {
				return usage()
			}
			flagOf := func() (*bool, bool) {
				switch val {
				case "on":
					return &on, true
				case "off":
					return &off, true
				}
				return nil, false
			}
			switch k {
			case "emote", "subs", "unique":
				b, ok := flagOf()
				if !ok {
					return usage()
				}
				switch k {
				case "emote":
					ch.EmoteOnly = b
				case "subs":
					ch.SubscribersOnly = b
				default:
					ch.UniqueChat = b
				}
			case "followers", "slow":
				var b *bool
				var n *int
				if val == "off" {
					b = &off
				} else if x, err := strconv.Atoi(val); err == nil {
					b, n = &on, &x
				} else {
					return usage()
				}
				if k == "followers" {
					ch.FollowersOnly, ch.FollowerMinutes = b, n
				} else {
					ch.Slow, ch.SlowSeconds = b, n
				}
			default:
				return usage()
			}
		}
		req := moderation.Request{Action: moderation.ChatModes, Chat: &ch, RequestID: events.NewID(), Platform: "twitch", From: moderation.FromCLI}
		var res api.ModerationResult
		if code := apiExit(c.Do(ctx, "POST", "/v1/moderation", req, &res), stderr); code != 0 {
			return code
		}
		if res.Record.State != moderation.Done {
			fmt.Fprintln(stderr, res.Record.Answer)
			return 1
		}
		if res.Record.Settings != nil {
			fmt.Fprintln(stdout, modesWords(*res.Record.Settings))
		}
		return 0
	case "history":
		path := "/v1/moderation"
		if len(args) == 1 {
			v, code := who(args[0])
			if code != 0 {
				return code
			}
			path += "?platform=" + platform + "&user_id=" + url.QueryEscape(v.UserID)
		} else if len(args) > 1 {
			return usage()
		}
		var out struct {
			Moderation []moderation.Record `json:"moderation"`
		}
		if code := apiExit(c.Do(ctx, "GET", path, nil, &out), stderr); code != 0 {
			return code
		}
		if len(out.Moderation) == 0 {
			fmt.Fprintln(stdout, "Nothing yet.")
		}
		for _, r := range out.Moderation {
			target := r.Login
			if target == "" {
				target = r.UserID
			}
			if r.Action == moderation.ChatModes {
				b, _ := json.Marshal(r.Chat)
				target = string(b)
			}
			answer := ""
			if r.Answer != "" {
				answer = ": " + r.Answer
			}
			fmt.Fprintf(stdout, "%s  %-10s %-20s %s%s\n", r.CreatedAt.Local().Format("Jan 2 15:04"), r.Action, target, r.State, answer)
		}
		return 0
	}
	return usage()
}

// reorder puts flags before arguments, so "-reason" can come last.
func reorder(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && args[i] != "-yes" && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, args[i])
	}
	return append(flags, rest...)
}

// modesWords says the chat modes in a line.
func modesWords(cs twitch.ChatSettings) string {
	var on []string
	if cs.EmoteOnly {
		on = append(on, "emote-only")
	}
	if cs.FollowersOnly {
		w := "followers-only"
		if cs.FollowerMinutes != nil && *cs.FollowerMinutes > 0 {
			w += fmt.Sprintf(" (followed %d minutes)", *cs.FollowerMinutes)
		}
		on = append(on, w)
	}
	if cs.Slow {
		w := "slow"
		if cs.SlowSeconds != nil {
			w += fmt.Sprintf(" (%d seconds)", *cs.SlowSeconds)
		}
		on = append(on, w)
	}
	if cs.SubscribersOnly {
		on = append(on, "subscribers-only")
	}
	if cs.UniqueChat {
		on = append(on, "unique chat")
	}
	if len(on) == 0 {
		return "Chat modes: none on."
	}
	return "Chat modes on: " + strings.Join(on, ", ") + "."
}
