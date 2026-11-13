package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

var errNoTwitch = errors.New("twitch isn't set up: sign in on Connections")

// plain drops the package's "twitch: " from an error, so a template can
// say it in chat.
func plain(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.TrimPrefix(err.Error(), "twitch: "))
}

// recordClip adds "A clip is made" (D-089), once per clip, so any workflow
// can share it, whoever made it. The clip is made either way.
func recordClip(ctx context.Context, inbox *events.Inbox, c workflow.Call, clip map[string]any, title string) {
	if inbox == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"id": clip["id"], "url": clip["url"], "edit_url": clip["edit_url"], "title": title})
	_, _ = inbox.Accept(ctx, events.Envelope{Type: events.ClipCreated, Source: "twitch", SourceEventID: "clip:" + fmt.Sprint(clip["id"]),
		OccurredAt: time.Now(), Actor: c.Actor, Payload: payload, DeliveryQuality: events.Confirmed})
}

// registerChannel adds the channel's own steps (D-086): the title and
// category, clips, stream markers, and reading the stream and when a
// viewer followed. Changing steps are External: a test event never
// reaches Twitch. Reading steps run for test events too; then a reading
// Twitch can't give is unknown, not a failure.
func registerChannel(reg *workflow.Registry, tw *twitch.Service, inbox *events.Inbox) {
	str := func(c workflow.Call, name string) string { s, _ := c.String(name, ""); return strings.TrimSpace(s) }
	reg.Add(workflow.Action{
		Name: "twitch.title.set", Label: "Change the stream title", Group: "Twitch", External: true,
		Description: "Changes your stream's title on Twitch (1 to 140 characters). Safe to repeat.",
		Params:      []workflow.Param{{Name: "title", Label: "Title", Type: "text", Required: true, Editor: "message"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if tw == nil {
				return nil, errNoTwitch
			}
			t := str(c, "title")
			return map[string]any{"title": t}, plain(tw.SetTitle(ctx, t))
		},
	})
	reg.Add(workflow.Action{
		Name: "twitch.category.set", Label: "Change the category", Group: "Twitch", External: true,
		Description: "Changes your stream's category on Twitch: the one with exactly that name, or else the first Twitch " +
			"finds for it (\"elden\" finds Elden Ring). Its result says which. Safe to repeat.",
		Params: []workflow.Param{{Name: "category", Label: "Category", Type: "text", Required: true, Editor: "message"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if tw == nil {
				return nil, errNoTwitch
			}
			cat, err := tw.SetCategory(ctx, str(c, "category"))
			if err != nil {
				return nil, plain(err)
			}
			return map[string]any{"category": cat.Name, "id": cat.ID, "exact": cat.Exact, "asked": str(c, "category")}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "twitch.clip.create", Label: "Make a clip", Group: "Twitch", External: true,
		Description: "Clips the last seconds of your stream, as you, while you're live. Twitch makes it after a moment, so " +
			"the step waits until it's there (up to a minute) and gives its link. Never sent twice.",
		Params: []workflow.Param{
			{Name: "title", Label: "Title", Type: "text", Editor: "message", DefaultHint: "Your stream's title"},
			{Name: "seconds", Label: "Length (5 to 60 seconds)", Type: "number", DefaultHint: "30"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if tw == nil {
				return nil, errNoTwitch
			}
			secs := 0
			if v, ok := c.Args["seconds"]; ok && v != nil {
				n, ok := number(v)
				if !ok {
					return nil, errors.New("the length is a number of seconds, 5 to 60")
				}
				secs = n
			}
			clip, err := tw.CreateClip(ctx, str(c, "title"), float64(secs))
			if err != nil {
				return nil, plain(err)
			}
			out := map[string]any{"id": clip.ID, "url": clip.URL, "edit_url": clip.EditURL}
			recordClip(ctx, inbox, c, out, str(c, "title"))
			return out, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "twitch.marker.create", Label: "Add a stream marker", Group: "Twitch", External: true, More: true,
		Description: "Marks this moment in your past broadcast, to find it later, while you're live with past broadcasts " +
			"saved. Never sent twice.",
		Params: []workflow.Param{{Name: "description", Label: "Description (up to 140 characters)", Type: "text", Editor: "message"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if tw == nil {
				return nil, errNoTwitch
			}
			m, err := tw.CreateMarker(ctx, str(c, "description"))
			if err != nil {
				return nil, plain(err)
			}
			at := time.Duration(m.PositionSeconds) * time.Second
			return map[string]any{"position_seconds": m.PositionSeconds, "position": clock(at), "description": m.Description}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "twitch.stream.read", Label: "Read the stream", Group: "Twitch",
		Description: "Asks Twitch whether you're live and since when, with your title, category, viewers, channel link and " +
			"(when live) a preview picture of the stream. Changes nothing, " +
			"so it runs for test events too. Its state is live, offline, or unknown when Twitch didn't say.",
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			var st twitch.StreamInfo
			err := errNoTwitch
			if tw != nil {
				st, err = tw.Stream(ctx)
			}
			if err != nil {
				if c.DryRun {
					return map[string]any{"state": "unknown", "why": plain(err).Error(), "test": true}, nil
				}
				return nil, plain(err)
			}
			out := map[string]any{"state": "offline", "title": st.Title, "category": st.Category, "login": st.Login, "link": st.Link, "picture": st.Picture}
			if st.Live && st.StartedAt != nil {
				up := time.Since(*st.StartedAt)
				out["state"], out["started_at"], out["viewers"] = "live", st.StartedAt.UTC().Format(time.RFC3339), st.Viewers
				out["uptime"], out["uptime_seconds"] = twitch.DurationWords(up), int(up.Seconds())
			}
			return out, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "twitch.follow.read", Label: "Read when a viewer followed", Group: "Twitch", More: true,
		Description: "Asks Twitch whether a viewer follows you and since when. Changes nothing, so it runs for test events " +
			"too. Its state is following, not_following, or unknown when Twitch didn't say.",
		Params: []workflow.Param{{Name: "viewer", Label: "Viewer's name", Type: "text", Editor: "message",
			DefaultHint: "Whoever started the workflow"}},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			login := str(c, "viewer")
			if login == "" && c.Actor != nil {
				login = c.Actor.Login
			}
			if login == "" {
				return nil, errors.New("name a viewer: nobody started this workflow")
			}
			var f twitch.FollowAge
			err := errNoTwitch
			if tw != nil {
				f, err = tw.FollowerByLogin(ctx, login)
			}
			if err != nil {
				if c.DryRun {
					return map[string]any{"state": "unknown", "viewer": strings.TrimPrefix(login, "@"), "why": plain(err).Error(), "test": true}, nil
				}
				return nil, plain(err)
			}
			out := map[string]any{"state": "not_following", "viewer": f.DisplayName, "login": f.Login}
			if f.Follows {
				out["state"], out["followed_at"] = "following", f.FollowedAt.UTC().Format(time.RFC3339)
				out["follow_age"] = twitch.SinceWords(f.FollowedAt, time.Now())
			}
			return out, nil
		},
	})
}

// clock writes a stream position as 1:02:03, or 2:03 under an hour.
func clock(d time.Duration) string {
	h, m, sec := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}
