package actions

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// "Sum up the stream" (D-089) counts real events since the last time
// Twitch said you went live, up to going offline: test events, the
// broadcaster's own chat and an earlier stream don't count.
func TestStreamSummary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	inbox := events.NewInbox(db, nil)
	start := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	n := 0
	source := "twitch"
	add := func(typ string, at time.Duration, who string, payload map[string]any, test bool) {
		t.Helper()
		n++
		raw, _ := json.Marshal(payload)
		e := events.Envelope{Type: typ, Source: source, SourceEventID: "e" + strconv.Itoa(n), OccurredAt: start.Add(at), Payload: raw,
			DeliveryQuality: events.Confirmed}
		if test {
			e.DeliveryQuality = events.Synthetic
		}
		if who != "" {
			e.Actor = &events.Actor{Platform: source, ID: who, Login: who, DisplayName: who}
		}
		if _, err := inbox.Accept(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := summarize(ctx, inbox, start); err != nil || out["state"] != "unknown" {
		t.Fatal("never live:", out, err)
	}
	// An earlier stream.
	add("broadcast.channel.online", -48*time.Hour, "", map[string]any{}, false)
	add("audience.follow.created", -47*time.Hour, "old", map[string]any{}, false)
	add("broadcast.channel.offline", -46*time.Hour, "", map[string]any{}, false)

	add("broadcast.channel.online", 0, "", map[string]any{}, false)
	add("audience.follow.created", time.Minute, "a", map[string]any{}, false)
	add("audience.follow.created", 2*time.Minute, "b", map[string]any{}, false)
	add("audience.follow.created", 2*time.Minute, "test", map[string]any{}, true)
	add("monetization.subscription.started", 3*time.Minute, "a", map[string]any{"tier": 1}, false)
	add("monetization.subscription.renewed", 3*time.Minute, "c", map[string]any{"tier": 1}, false)
	add("monetization.subscription.gifted", 4*time.Minute, "d", map[string]any{"total": 5}, false)
	add("monetization.bits.cheered", 5*time.Minute, "e", map[string]any{"bits": 100}, false)
	add("monetization.bits.cheered", 5*time.Minute, "e", map[string]any{"bits": 250}, false)
	add("audience.raid.received", 6*time.Minute, "r", map[string]any{"viewers": 42}, false)
	add("chat.message.received", 7*time.Minute, "a", map[string]any{"chatter": map[string]any{"role": "viewer"}}, false)
	add("chat.message.received", 7*time.Minute, "a", map[string]any{"chatter": map[string]any{"role": "viewer"}}, false)
	add("chat.command.received", 7*time.Minute, "b", map[string]any{"chatter": map[string]any{"role": "vip"}}, false)
	add("chat.message.received", 8*time.Minute, "me", map[string]any{"chatter": map[string]any{"role": "broadcaster"}}, false)
	// YouTube, alongside (D-101): its own counts, its chatters with Twitch's;
	// its going offline doesn't end the summary.
	source = "youtube"
	add("chat.message.received", 9*time.Minute, "a", map[string]any{"chatter": map[string]any{"role": "viewer"}}, false)
	add("chat.message.received", 9*time.Minute, "y", map[string]any{"chatter": map[string]any{"role": "viewer"}, "backlog": true}, false)
	add("monetization.subscription.started", 9*time.Minute, "m", map[string]any{"tier": 0}, false)
	add("monetization.subscription.gifted", 9*time.Minute, "m", map[string]any{"total": 3}, false)
	add("monetization.super_chat.received", 9*time.Minute, "s", map[string]any{"amount": 5}, false)
	add("broadcast.channel.offline", 10*time.Minute, "", map[string]any{}, false)
	source = "twitch"

	out, err := summarize(ctx, inbox, start.Add(90*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"state": "live", "duration": "1 hour 30 minutes", "follows": int64(2), "subs": int64(2), "gifted_subs": int64(5),
		"bits": int64(350), "raids": int64(1), "raiders": int64(42), "chatters": 3, "messages": int64(4),
		"youtube_chatters": 1, "members": int64(1), "gifted_memberships": int64(3), "super_chats": int64(1)}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("%s: %v, want %v", k, out[k], v)
		}
	}

	add("broadcast.channel.offline", 2*time.Hour, "", map[string]any{}, false)
	add("audience.follow.created", 3*time.Hour, "late", map[string]any{}, false)
	out, _ = summarize(ctx, inbox, start.Add(5*time.Hour))
	if out["state"] != "ended" || out["duration"] != "2 hours" || out["follows"] != int64(2) {
		t.Fatal("ended:", out)
	}
}
