package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/workflow"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

var errNoYouTube = errors.New("YouTube isn't part of StreamGremlin here")

// ytPlain drops the package's "youtube: " from an error's words.
func ytPlain(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.TrimPrefix(strings.TrimPrefix(err.Error(), "youtube: invalid: "), "youtube: "))
}

// registerYouTube adds the steps that change your YouTube broadcast (M6
// slice 7, D-102): its title and description, and polls. They reach
// YouTube, so they're External: a test event never changes anything. Each
// says its quota cost.
func registerYouTube(reg *workflow.Registry, yt *youtube.Service) {
	str := func(c workflow.Call, name string) (string, bool) {
		v, ok := c.Args[name]
		if !ok || v == nil {
			return "", false
		}
		s, _ := c.String(name, "")
		return s, true
	}
	reg.Add(workflow.Action{
		Name: "youtube.stream.set", Label: "Change the YouTube title or description", Group: "YouTube", External: true, More: true,
		Description: fmt.Sprintf("Changes your live YouTube broadcast's title (1 to %d characters), description, or both: "+
			"%d of the day's %d YouTube quota units. Only while YouTube chat is being read. Leave one empty to keep it.",
			youtube.MaxTitleRunes, youtube.CostVideosList+youtube.CostVideosUpdate, youtube.DailyQuota),
		Params: []workflow.Param{
			{Name: "title", Label: "Title", Type: "text", Editor: "message", DefaultHint: "Keep it"},
			{Name: "description", Label: "Description", Type: "text", Editor: "message", DefaultHint: "Keep it"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if yt == nil {
				return nil, errNoYouTube
			}
			var ch youtube.StreamChange
			if t, ok := str(c, "title"); ok && strings.TrimSpace(t) != "" {
				ch.Title = &t
			}
			if d, ok := str(c, "description"); ok && d != "" {
				ch.Description = &d
			}
			info, err := yt.SetStream(ctx, ch)
			if err != nil {
				return nil, ytPlain(err)
			}
			return map[string]any{"title": info.Title, "description": info.Description}, nil
		},
	})

	pollParams := []workflow.Param{{Name: "question", Label: "Question", Type: "text", Required: true, Editor: "message"}}
	for i := 1; i <= youtube.MaxPollOptions; i++ {
		pollParams = append(pollParams, workflow.Param{Name: fmt.Sprintf("choice_%d", i), Label: fmt.Sprintf("Choice %d", i), Type: "text",
			Required: i <= youtube.MinPollOptions})
	}
	reg.Add(workflow.Action{
		Name: "youtube.poll.start", Label: "Start a YouTube poll", Group: "YouTube", External: true,
		Description: fmt.Sprintf("Starts a poll in your YouTube chat, as your channel: 2 to 4 choices, one poll at a time. It runs "+
			"until it's ended (YouTube polls have no timer): use “End the YouTube poll”. %d quota units. Never retried.", youtube.CostMessageInsert),
		Params: pollParams,
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if yt == nil {
				return nil, errNoYouTube
			}
			q, _ := c.String("question", "")
			var opts []string
			for i := 1; i <= youtube.MaxPollOptions; i++ {
				if s, _ := c.String(fmt.Sprintf("choice_%d", i), ""); s != "" {
					opts = append(opts, s)
				}
			}
			p, err := yt.StartPoll(ctx, youtube.NewPoll{Question: q, Options: opts})
			if err != nil {
				return nil, ytPlain(err)
			}
			return map[string]any{"poll_id": p.ID, "question": p.Question, "choices": len(p.Choices)}, nil
		},
	})
	reg.Add(workflow.Action{
		Name: "youtube.poll.end", Label: "End the YouTube poll", Group: "YouTube", External: true,
		Description: fmt.Sprintf("Ends the poll running in your YouTube chat, with its votes. %d quota units. Nothing running isn't a failure.",
			youtube.CostPollClose),
		Params: []workflow.Param{},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if yt == nil {
				return nil, errNoYouTube
			}
			p, err := yt.EndPoll(ctx)
			if errors.Is(err, youtube.ErrNoPoll) {
				return map[string]any{"ended": false, "why": "no poll was running"}, nil
			}
			if err != nil {
				return nil, ytPlain(err)
			}
			votes := map[string]any{}
			for _, ch := range p.Choices {
				votes[ch.Title] = ch.Votes
			}
			return map[string]any{"ended": true, "question": p.Question, "votes": votes}, nil
		},
	})
}
