package actions

import (
	"context"

	"code.streamgremlin.com/streamgremlin/internal/pretzel"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// registerPretzel adds reading what Pretzel is playing (D-090), for a
// !song command.
func registerPretzel(reg *workflow.Registry, pz *pretzel.Service) {
	reg.Add(workflow.Action{
		Name: "pretzel.song", Label: "Get the current song (Pretzel)", Group: "Music",
		Description: "Asks Pretzel what's playing on your stream. Its chat output is ready for chat: \"Now playing: Title by " +
			"Artist → link\", or why it can't say. Its state is playing, other, not_using (Pretzel doesn't know your Twitch " +
			"account), not_signed_in or unknown. Changes nothing, so it runs for test events too. Pretzel's own chat " +
			"attribution still posts as Pretzel requires.",
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if pz == nil {
				return map[string]any{"state": pretzel.Unknown, "chat": pretzel.Song{State: pretzel.Unknown}.Chat()}, nil
			}
			s := pz.Now(ctx)
			return map[string]any{"state": s.State, "title": s.Title, "artist": s.Artist, "text": s.Text, "link": s.Link,
				"chat": s.Chat(), "why": s.Why}, nil
		},
	})
}
