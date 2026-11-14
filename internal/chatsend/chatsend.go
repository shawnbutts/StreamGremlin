// Package chatsend sends one chat message to Twitch, YouTube or both
// (M6 slice 3, D-098): the send box on Live, workflows' "Send a chat
// message" and the API all use it. Where a message goes is decided here:
// an answer goes back to the platform its message came from.
package chatsend

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

// Where says which chats a message goes to.
type Where string

const (
	// Origin answers on the platform the event came from: YouTube for a
	// YouTube viewer's message, Twitch for everything else.
	Origin  Where = ""
	Twitch  Where = "twitch"
	YouTube Where = "youtube"
	Both    Where = "both"
)

// Platforms are the chat platforms, in the order Both sends to them. A
// later platform is added here, so everything sending "everywhere" (Both)
// reaches it (D-103).
var Platforms = []string{"twitch", events.SourceYouTube}

// IsPlatform reports whether a source is a chat platform, as opposed to
// StreamGremlin itself, OBS, a timer or an add-on.
func IsPlatform(source string) bool {
	for _, p := range Platforms {
		if p == source {
			return true
		}
	}
	return false
}

// Names says platforms in words: "Twitch and YouTube".
func Names(ps []string) string {
	words := map[string]string{"twitch": "Twitch", events.SourceYouTube: "YouTube"}
	var out []string
	for _, p := range ps {
		if w, ok := words[p]; ok {
			out = append(out, w)
		} else {
			out = append(out, p)
		}
	}
	return strings.Join(out, " and ")
}

// ErrInvalid wraps bad input.
var ErrInvalid = errors.New("chatsend: invalid")

// Valid reports whether w is a known choice.
func (w Where) Valid() bool {
	switch w {
	case Origin, Twitch, YouTube, Both:
		return true
	}
	return false
}

// TwitchSender and YouTubeSender are the platforms' senders.
type TwitchSender interface {
	SendChat(ctx context.Context, req twitch.SendRequest) (twitch.SendResult, error)
}

// YouTubeSender sends to the YouTube chat being read.
type YouTubeSender interface {
	SendChat(ctx context.Context, req youtube.SendRequest) (youtube.SendResult, error)
	ChatStatus() youtube.ChatStatus
}

// Request is one message to send.
type Request struct {
	Text string `json:"text"`
	// As is "", "bot" or "streamer", on each platform.
	As string `json:"as,omitempty"`
	To Where  `json:"to,omitempty"`
	// Origin is the source of the event being answered ("youtube",
	// "twitch"), for To = Origin and replies.
	Origin string `json:"-"`
	// ReplyTo is the message being answered: a reply on Twitch, and
	// "@ReplyName " first on YouTube, which has no replies.
	ReplyTo   string `json:"reply_to,omitempty"`
	ReplyName string `json:"-"`
	// Workflow is the workflow sending it, if any (never from the API).
	Workflow string `json:"-"`
}

// Result says what each platform did. A platform in Skipped wasn't sent to
// (YouTube while you're not live, for "both"); in Failed, it refused while
// another platform sent.
type Result struct {
	Twitch  *twitch.SendResult  `json:"twitch,omitempty"`
	YouTube *youtube.SendResult `json:"youtube,omitempty"`
	Skipped map[string]string   `json:"skipped,omitempty"`
	Failed  map[string]string   `json:"failed,omitempty"`
}

// PlatformError is a refusal from one platform, when it was the only one.
type PlatformError struct {
	Platform string
	Err      error
}

func (e *PlatformError) Error() string { return e.Err.Error() }
func (e *PlatformError) Unwrap() error { return e.Err }

// Targets returns the platforms a message goes to.
func Targets(to Where, origin string) []string {
	switch to {
	case Twitch:
		return []string{"twitch"}
	case YouTube:
		return []string{"youtube"}
	case Both:
		return append([]string(nil), Platforms...)
	}
	if origin == events.SourceYouTube {
		return []string{"youtube"}
	}
	return []string{"twitch"}
}

// Send sends req. With one platform, its refusal is the error. With both,
// YouTube is skipped while you're not live there, and the error comes only
// when every platform tried refused.
func Send(ctx context.Context, tw TwitchSender, yt YouTubeSender, req Request) (Result, error) {
	if !req.To.Valid() {
		return Result{}, fmt.Errorf("%w: to is twitch, youtube or both", ErrInvalid)
	}
	switch req.As {
	case "", "bot", "streamer":
	default:
		return Result{}, fmt.Errorf("%w: send as bot or streamer", ErrInvalid)
	}
	targets := Targets(req.To, req.Origin)
	var res Result
	var errs []*PlatformError
	for _, p := range targets {
		var err error
		switch p {
		case "twitch":
			r := twitch.SendRequest{Text: req.Text, As: twitch.Sender(req.As), Workflow: req.Workflow}
			if req.Origin != events.SourceYouTube {
				r.ReplyTo = req.ReplyTo // a Twitch message's id
			}
			var out twitch.SendResult
			if out, err = tw.SendChat(ctx, r); err == nil {
				res.Twitch = &out
			}
		case "youtube":
			if yt == nil && len(targets) == 1 {
				errs = append(errs, &PlatformError{Platform: p, Err: youtube.ErrNotSignedIn})
				continue
			}
			if yt == nil || (len(targets) > 1 && yt.ChatStatus().Broadcast == nil) {
				if res.Skipped == nil {
					res.Skipped = map[string]string{}
				}
				res.Skipped["youtube"] = "Not live on YouTube."
				continue
			}
			text := req.Text
			if req.ReplyTo != "" && req.ReplyName != "" && req.Origin == events.SourceYouTube {
				text = "@" + strings.TrimPrefix(req.ReplyName, "@") + " " + strings.TrimSpace(text)
			}
			// YouTube's names are often handles already: "@${name}" in a
			// message written for Twitch would say "@@name" (D-101).
			text = strings.ReplaceAll(text, "@@", "@")
			var out youtube.SendResult
			if out, err = yt.SendChat(ctx, youtube.SendRequest{Text: text, As: youtube.Sender(req.As), Workflow: req.Workflow}); err == nil {
				res.YouTube = &out
			}
		}
		if err != nil {
			errs = append(errs, &PlatformError{Platform: p, Err: err})
		}
	}
	if len(errs) == 0 {
		return res, nil
	}
	if res.Twitch == nil && res.YouTube == nil {
		return res, errs[0] // nothing sent: the first refusal says why
	}
	res.Failed = map[string]string{}
	for _, e := range errs {
		res.Failed[e.Platform] = e.Err.Error()
	}
	return res, nil
}
