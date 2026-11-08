package twitch

import (
	"context"
	"testing"
	"time"
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
	recs := waitEvents(t, r.inbox, 3)
	var texts []string
	for _, rec := range recs {
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
	recs = waitEvents(t, r.inbox, 5)                             // your message and its command
	if payload(t, recs[3])["text"] != "!hello from a workflow" || recs[4].Type != "chat.command.received" {
		t.Fatalf("%+v", recs[3:])
	}
}

func TestOwnSends(t *testing.T) {
	var o ownSends
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	// The echo before Twitch answers: matched by text.
	o.expect("hi", now)
	if !o.claim("m1", "hi", now) || o.claim("m1", "hi", now) {
		t.Fatal("by text, once")
	}
	o.settle("hi", "m1", now) // late answer: nothing left to settle
	if o.claim("m1", "hi", now) {
		t.Fatal("claimed after settling")
	}
	// Twitch answers first: matched by id, not by text.
	o.expect(" hey ", now)
	o.settle("hey", "m2", now)
	if o.claim("other", "hey", now) || !o.claim("m2", "hey", now) {
		t.Fatal("by id")
	}
	// A failed send expects no echo.
	o.expect("nope", now)
	o.settle("nope", "", now)
	if o.claim("m3", "nope", now) {
		t.Fatal("failed send claimed")
	}
	// An echo that never came is forgotten.
	o.expect("lost", now)
	if o.claim("m4", "lost", now.Add(ownSendTTL)) {
		t.Fatal("expired send claimed")
	}
	if len(o.sent) != 0 {
		t.Fatalf("%+v", o.sent)
	}
}
