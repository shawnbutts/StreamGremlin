package twitch

import (
	"context"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// A workflow's message sent as you isn't stored as you chatting, so no
// workflow can answer it, and answer that, forever. What you type yourself,
// in Twitch or in the app, still is.
func TestAWorkflowsOwnMessagesAreNotStoredAsYouChatting(t *testing.T) {
	r, s := chatRig(t, false)
	ctx := context.Background()
	res, err := s.SendChat(ctx, SendRequest{Text: "!hello from a workflow", Workflow: "chat.loop"})
	if err != nil || res.SentAs != RoleStreamer {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := s.SendChat(ctx, SendRequest{Text: "typed in the app"}); err != nil {
		t.Fatal(err)
	}
	r.mock.Say("streamer", "streamer", "typed in Twitch")
	r.mock.Say("streamer", "viewer", "a viewer")
	// The workflow's message is kept only as chat.message.sent (D-087).
	recs := waitEvents(t, r.inbox, 4)
	var texts []string
	for _, rec := range recs {
		if rec.Type == events.ChatSent {
			if payload(t, rec)["sent_by"] != "workflow" || payload(t, rec)["text"] != "!hello from a workflow" {
				t.Fatalf("%+v", rec)
			}
			continue
		}
		texts = append(texts, payload(t, rec)["text"].(string))
	}
	if len(texts) != 3 || texts[0] != "typed in the app" || texts[1] != "typed in Twitch" || texts[2] != "a viewer" {
		t.Fatalf("%q", texts)
	}
	// The same text from a workflow again is skipped again, but only once per send.
	if _, err := s.SendChat(ctx, SendRequest{Text: "!hello from a workflow", Workflow: "chat.loop"}); err != nil {
		t.Fatal(err)
	}
	r.mock.Say("streamer", "streamer", "!hello from a workflow") // you, typing the same thing
	recs = waitEvents(t, r.inbox, 7)                             // the workflow's, kept as sent; then your message and its command
	if recs[4].Type != events.ChatSent || payload(t, recs[5])["text"] != "!hello from a workflow" ||
		recs[5].Type != "chat.message.received" || recs[6].Type != "chat.command.received" {
		t.Fatalf("%+v", recs[4:])
	}
}
