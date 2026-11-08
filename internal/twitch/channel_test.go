package twitch

import (
	"slices"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

func TestChannelEventsReachTheInbox(t *testing.T) {
	r, s := chatRig(t, false)
	st := s.ChatStatus()
	if len(st.Feeds) != len(Feeds) {
		t.Fatalf("%+v", st.Feeds)
	}
	for _, f := range st.Feeds {
		if !f.OK {
			t.Errorf("%s: %s", f.Type, f.Problem)
		}
	}
	if subs := r.mock.Subscriptions("streamer"); len(subs) != len(Feeds)+1 {
		t.Fatalf("subscribed to %v", subs)
	}

	r.mock.Follow("streamer", "NewFan")
	r.mock.Subscribe("streamer", "SubFan", "2000", false)
	r.mock.Resub("streamer", "OldFan", "1000", 14, 3, "two years!")
	r.mock.Gift("streamer", "Giver", "1000", 5)
	r.mock.Gift("streamer", "", "3000", 1)
	r.mock.Cheer("streamer", "Cheerer", 500, "Cheer500 go go")
	r.mock.Cheer("streamer", "", 100, "Cheer100")
	r.mock.Raid("streamer", "FriendlyStreamer", 42)
	redemption := r.mock.Redeem("streamer", "Redeemer", "Hydrate", 300, "water please")
	r.mock.GoLive("streamer")
	r.mock.UpdateChannel("streamer", "Speedrun night", "Celeste")
	r.mock.GoOffline("streamer")
	recs := waitEvents(t, r.inbox, 12)

	type want struct {
		typ, actor string
		fields     map[string]any
	}
	for i, w := range []want{
		{"audience.follow.created", "NewFan", nil},
		{"monetization.subscription.started", "SubFan", map[string]any{"tier": float64(2), "is_gift": false}},
		{"monetization.subscription.renewed", "OldFan", map[string]any{"months": float64(14), "streak_months": float64(3), "text": "two years!"}},
		{"monetization.subscription.gifted", "Giver", map[string]any{"total": float64(5), "is_anonymous": false}},
		{"monetization.subscription.gifted", "", map[string]any{"tier": float64(3), "is_anonymous": true, "from": "Anonymous"}},
		{"monetization.bits.cheered", "Cheerer", map[string]any{"bits": float64(500), "text": "Cheer500 go go", "from": "Cheerer"}},
		{"monetization.bits.cheered", "", map[string]any{"bits": float64(100), "is_anonymous": true}},
		{"audience.raid.received", "FriendlyStreamer", map[string]any{"viewers": float64(42)}},
		{"rewards.channel_points.redeemed", "Redeemer", map[string]any{"user_input": "water please", "redemption_id": redemption}},
		{"broadcast.channel.online", "", map[string]any{"stream_type": "live"}},
		{"broadcast.channel.updated", "", map[string]any{"title": "Speedrun night", "category": "Celeste"}},
		{"broadcast.channel.offline", "", nil},
	} {
		rec := recs[i]
		if rec.Type != w.typ || rec.Source != "twitch" || rec.DeliveryQuality != events.Confirmed || rec.ChannelID == "" {
			t.Errorf("%d: %+v", i, rec.Envelope)
			continue
		}
		name := ""
		if rec.Actor != nil {
			name = rec.Actor.DisplayName
			if rec.Actor.Platform != "twitch" || rec.Actor.ID == "" || rec.Actor.Login != strings.ToLower(name) {
				t.Errorf("%s: actor %+v", w.typ, rec.Actor)
			}
		}
		if name != w.actor {
			t.Errorf("%s: actor %q, want %q", w.typ, name, w.actor)
		}
		p := payload(t, rec)
		for k, v := range w.fields {
			if p[k] != v {
				t.Errorf("%s: %s = %v, want %v (%v)", w.typ, k, p[k], v, p)
			}
		}
	}
	reward := payload(t, recs[8])["reward"].(map[string]any)
	if reward["title"] != "Hydrate" || reward["cost"] != float64(300) || recs[8].SourceEventID != "redemption:"+redemption {
		t.Fatalf("%v %s", reward, recs[8].SourceEventID)
	}
	if ch := s.Channel(); !ch.Known || ch.Category != "Celeste" || ch.Title != "Speedrun night" {
		t.Fatalf("%+v", ch)
	}
}

func TestARedeliveredEventIsStoredOnce(t *testing.T) {
	r, _ := chatRig(t, false)
	id := r.mock.Follow("streamer", "Fan")
	r.mock.Notify("streamer", "channel.follow", map[string]any{"user_id": r.mock.UserID("fan"), "user_login": "fan",
		"user_name": "Fan", "followed_at": time.Now().UTC().Format(time.RFC3339Nano)}, id)
	r.mock.Follow("streamer", "Other")
	recs := waitEvents(t, r.inbox, 2)
	if recs[0].Actor.Login != "fan" || recs[1].Actor.Login != "other" {
		t.Fatal(recs)
	}
}

func TestMissingPermissionsAndRefusalsAreListedAndChatGoesOn(t *testing.T) {
	r := newRig(t)
	r.mock.GrantScopes = []string{"user:read:chat", "user:write:chat", "bits:read"} // an old sign-in
	r.mock.RefuseType("channel.cheer")                                              // not an affiliate, say
	s := r.configured()
	r.signIn(s, RoleStreamer, "Streamer")
	waitChat(t, s, ChatConnected)
	byType := map[string]FeedStatus{}
	for _, f := range s.ChatStatus().Feeds {
		byType[f.Type] = f
	}
	if f := byType["channel.follow"]; f.OK || !strings.Contains(f.Problem, "Sign in to Twitch again") || !strings.Contains(f.Problem, "moderator:read:followers") {
		t.Errorf("%+v", f)
	}
	if f := byType["channel.cheer"]; f.OK || !strings.Contains(f.Problem, "Twitch refused it") {
		t.Errorf("%+v", f)
	}
	for _, typ := range []string{"channel.raid", "stream.online", "stream.offline", "channel.update"} {
		if !byType[typ].OK {
			t.Errorf("%s needs no permission: %+v", typ, byType[typ])
		}
	}
	if slices.Contains(r.mock.Subscriptions("streamer"), "channel.follow") {
		t.Error("asked for follows without the permission")
	}
	r.mock.Say("streamer", "viewer", "chat still works")
	waitEvents(t, r.inbox, 1)
}

func TestARevokedEventStopsOnlyThatEvent(t *testing.T) {
	r, s := chatRig(t, false)
	r.mock.RevokeType("streamer", "channel.cheer", "authorization_revoked")
	deadline := time.Now().Add(5 * time.Second)
	for {
		var f FeedStatus
		for _, x := range s.ChatStatus().Feeds {
			if x.Type == "channel.cheer" {
				f = x
			}
		}
		if !f.OK && strings.Contains(f.Problem, "authorization_revoked") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%+v", s.ChatStatus().Feeds)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.ChatStatus().State != ChatConnected {
		t.Fatal(s.ChatStatus())
	}
	r.mock.Say("streamer", "viewer", "still here")
	waitEvents(t, r.inbox, 1)
}

func TestTheChannelIsReadAtConnect(t *testing.T) {
	r := newRig(t)
	r.mock.UpdateChannel("streamer", "Cozy stream", "Stardew Valley") // before anyone listens
	s := r.configured()
	r.signIn(s, RoleStreamer, "Streamer")
	waitChat(t, s, ChatConnected)
	if ch := s.Channel(); !ch.Known || ch.Category != "Stardew Valley" || ch.Title != "Cozy stream" {
		t.Fatalf("%+v", ch)
	}
}
