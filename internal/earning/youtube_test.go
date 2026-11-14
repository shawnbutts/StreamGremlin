package earning

import (
	"context"
	"fmt"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

func (f *fixture) ytEvent(t *testing.T, typ, id string, sec int, payload string) {
	t.Helper()
	f.n++
	env := events.Envelope{Type: typ, Source: events.SourceYouTube, SourceEventID: fmt.Sprintf("y%d", f.n), Payload: []byte(payload),
		DeliveryQuality: events.Confirmed, ChannelID: "UCme", ReceivedAt: f.t0.Add(time.Duration(sec) * time.Second)}
	if id != "" {
		env.Actor = &events.Actor{Platform: events.SourceYouTube, ID: id, DisplayName: "@" + id}
	}
	if _, err := f.in.Accept(context.Background(), env); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) ytBalance(t *testing.T, id string) int64 {
	t.Helper()
	v, err := f.vs.ByAccount(context.Background(), events.SourceYouTube, id)
	if err != nil {
		return 0
	}
	b, _ := f.ledger.Balance(context.Background(), v.ID, "")
	return b
}

// YouTube viewers earn (D-101): chat, members as tier 1 subs, gifted
// memberships for the gifter, and a flat amount per Super Chat or
// Sticker; backlog never pays; YouTube going live isn't "live".
func TestYouTubeEarns(t *testing.T) {
	f := newFixture(t)
	f.event(t, "broadcast.channel.online", "", 0, `{}`, events.Confirmed) // Twitch live
	f.catchUp(t)
	f.on(t, func(r *Rules) { r.Chat.EverySeconds = 0; r.SuperChat = 7 })
	f.ytEvent(t, "chat.message.received", "UCa", 1, `{"chatter":{"role":"viewer"}}`)
	f.ytEvent(t, "chat.message.received", "UCa", 2, `{"chatter":{"role":"viewer"},"backlog":true}`)
	f.ytEvent(t, "monetization.subscription.started", "UCa", 3, `{"tier":0,"level":"Gold","is_gift":false}`)
	f.ytEvent(t, "monetization.super_chat.received", "UCa", 4, `{"amount":5,"currency":"USD"}`)
	f.ytEvent(t, "monetization.super_sticker.received", "UCa", 5, `{"amount":2,"currency":"EUR"}`)
	f.ytEvent(t, "monetization.subscription.started", "UCb", 6, `{"tier":0,"is_gift":true}`)
	f.ytEvent(t, "monetization.subscription.gifted", "UCc", 7, `{"tier":0,"total":2}`)
	f.catchUp(t)
	d := Defaults()
	if got, want := f.ytBalance(t, "UCa"), d.Chat.Amount+d.Sub.Tier1+7+7; got != want {
		t.Fatalf("UCa %d, want %d", got, want)
	}
	if f.ytBalance(t, "UCb") != 0 || f.ytBalance(t, "UCc") != 2*d.Gift {
		t.Fatal("gifted memberships pay the gifter only")
	}
	v, _ := f.vs.ByAccount(context.Background(), events.SourceYouTube, "UCa")
	hist, _ := f.ledger.History(context.Background(), v.ID, 10)
	found := false
	for _, h := range hist {
		found = found || h.Memo == "becoming a member"
	}
	if !found {
		t.Fatalf("%+v", hist)
	}
	// YouTube going offline doesn't end "live": Twitch's stream goes on.
	f.ytEvent(t, "broadcast.channel.offline", "", 8, `{}`)
	f.catchUp(t)
	if f.s.LiveState() != Live {
		t.Fatal(f.s.LiveState())
	}
}
