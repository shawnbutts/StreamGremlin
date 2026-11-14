package youtube

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

func payloadMap(t *testing.T, r events.Record) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestYouTubeEventsFromChat(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "Stream")
	fan := livechat.Author{ChannelID: "UCfan", DisplayName: "Fan"}
	// From before StreamGremlin was reading: stored as backlog.
	r.mock.Add(b.ChatID, livechat.Message{Author: fan, Snippet: livechat.Snippet{Type: livechat.TypeSuperChat,
		SuperChat: &livechat.SuperChat{AmountMicros: 1_000_000, Currency: "USD", AmountDisplayString: "$1.00"}}})
	s.FindNow()
	waitChat(t, s, ChatConnected)
	add := func(sn livechat.Snippet, a livechat.Author) {
		r.mock.Add(b.ChatID, livechat.Message{Author: a, Snippet: sn})
	}
	add(livechat.Snippet{Type: livechat.TypeSuperChat, SuperChat: &livechat.SuperChat{AmountMicros: 5_000_000, Currency: "USD",
		AmountDisplayString: "$5.00", UserComment: "great stream", Tier: 2}}, fan)
	add(livechat.Snippet{Type: livechat.TypeSuperSticker, SuperSticker: &livechat.SuperSticker{AmountMicros: 2_000_000, Currency: "EUR",
		AmountDisplayString: "€2.00", Tier: 1, StickerID: "s", AltText: "A dancing cat"}}, fan)
	add(livechat.Snippet{Type: livechat.TypeGift, Gift: &livechat.Gift{Name: "Rose", JewelsCount: 10, ComboCount: 3}}, fan)
	add(livechat.Snippet{Type: livechat.TypeNewSponsor, NewSponsor: &livechat.NewSponsor{MemberLevelName: "Gold"}}, fan)
	add(livechat.Snippet{Type: livechat.TypeMemberMilestoneChat, MemberMilestone: &livechat.MemberMilestone{MemberLevelName: "Gold", MemberMonth: 12, UserComment: "a year"}}, fan)
	add(livechat.Snippet{Type: livechat.TypeMembershipGifting, MembershipGifting: &livechat.MembershipGifting{Count: 5, LevelName: "Gold"}}, fan)
	add(livechat.Snippet{Type: livechat.TypeGiftMembershipReceived, GiftMembershipReceived: &livechat.GiftMembershipReceived{MemberLevelName: "Gold", GifterChannelID: "UCfan"}},
		livechat.Author{ChannelID: "UClucky", DisplayName: "Lucky"})
	add(livechat.Snippet{Type: livechat.TypeSponsorOnlyModeStarted}, livechat.Author{ChannelID: "UCstreamer"})
	add(livechat.Snippet{Type: livechat.TypeUserBanned, UserBanned: &livechat.UserBanned{ChannelID: "UCspam", DisplayName: "Spammer", BanType: livechat.BanTemporary, DurationSeconds: 300}},
		livechat.Author{ChannelID: "UCmod", DisplayName: "Mod"})
	add(livechat.Snippet{Type: livechat.TypeUserBanned, UserBanned: &livechat.UserBanned{ChannelID: "UCspam", DisplayName: "Spammer", BanType: livechat.BanPermanent}},
		livechat.Author{ChannelID: "UCmod", DisplayName: "Mod"})

	want := map[string]int{TypeSuperChat: 2, TypeSuperSticker: 1, TypeJewels: 1, typeSubStarted: 2, typeSubRenewed: 1, typeSubGifted: 1,
		TypeMembersOnly: 1, TypeTimedOut: 1, TypeBanned: 1, typeOnline: 1}
	var got map[string][]events.Record
	deadline := time.Now().Add(5 * time.Second)
	for {
		recs, _ := r.inbox.After(context.Background(), 0, 1000, nil)
		got = map[string][]events.Record{}
		for _, x := range recs {
			if x.Source == events.SourceYouTube {
				got[x.Type] = append(got[x.Type], x)
			}
		}
		ok := true
		for typ, n := range want {
			if len(got[typ]) != n {
				ok = false
			}
		}
		if ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for typ, n := range want {
		if len(got[typ]) != n {
			t.Fatalf("%s: %d, want %d", typ, len(got[typ]), n)
		}
	}
	early, sc := payloadMap(t, got[TypeSuperChat][0]), payloadMap(t, got[TypeSuperChat][1])
	if early["backlog"] != true || !events.Backlog(got[TypeSuperChat][0]) || events.Backlog(got[TypeSuperChat][1]) {
		t.Fatalf("backlog: %+v %+v", early, sc)
	}
	if sc["amount"] != 5.0 || sc["currency"] != "USD" || sc["amount_display"] != "$5.00" || sc["text"] != "great stream" ||
		sc["from"] != "Fan" || sc["tier"] != 2.0 || got[TypeSuperChat][1].Actor.ID != "UCfan" {
		t.Fatalf("%+v", sc)
	}
	if p := payloadMap(t, got[TypeSuperSticker][0]); p["sticker"] != "A dancing cat" || p["amount_display"] != "€2.00" {
		t.Fatalf("%+v", p)
	}
	if p := payloadMap(t, got[TypeJewels][0]); p["gift"] != "Rose" || p["jewels"] != 10.0 || p["count"] != 3.0 {
		t.Fatalf("%+v", p)
	}
	member, gifted := payloadMap(t, got[typeSubStarted][0]), payloadMap(t, got[typeSubStarted][1])
	if member["level"] != "Gold" || member["is_gift"] != false || member["tier"] != 0.0 {
		t.Fatalf("%+v", member)
	}
	if gifted["is_gift"] != true || gifted["gifter_channel_id"] != "UCfan" || got[typeSubStarted][1].Actor.DisplayName != "Lucky" {
		t.Fatalf("%+v", gifted)
	}
	if p := payloadMap(t, got[typeSubRenewed][0]); p["months"] != 12.0 || p["text"] != "a year" {
		t.Fatalf("%+v", p)
	}
	if p := payloadMap(t, got[typeSubGifted][0]); p["total"] != 5.0 || p["from"] != "Fan" {
		t.Fatalf("%+v", p)
	}
	if p := payloadMap(t, got[TypeMembersOnly][0]); p["enabled"] != true || got[TypeMembersOnly][0].Actor != nil {
		t.Fatalf("%+v", p)
	}
	to := got[TypeTimedOut][0]
	if p := payloadMap(t, to); p["duration_seconds"] != 300.0 || to.Actor.ID != "UCspam" || p["moderator"].(map[string]any)["display_name"] != "Mod" {
		t.Fatalf("%+v %+v", p, to.Actor)
	}
	if p := payloadMap(t, got[typeOnline][0]); p["title"] != "Stream" || p["url"] != "https://www.youtube.com/watch?v="+b.ID {
		t.Fatalf("%+v", p)
	}
	if got[typeOnline][0].OccurredAt.IsZero() {
		t.Fatal("no time")
	}

	// A dropped stream doesn't record going live again; the end records offline.
	r.mock.DropStreams()
	time.Sleep(100 * time.Millisecond)
	waitChat(t, s, ChatConnected)
	r.mock.EndBroadcast(b.ID)
	waitChat(t, s, ChatLooking)
	waitRecords(t, r, typeOffline, 1)
	waitRecords(t, r, typeOnline, 1)
}

func TestAChatThatsOffNeverGoesLive(t *testing.T) {
	r := newRig(t)
	s := live(t, r)
	b := r.mock.GoLive("UCstreamer", "No chat")
	r.mock.TurnChatOff(b.ChatID)
	s.FindNow()
	waitChat(t, s, ChatLooking)
	time.Sleep(50 * time.Millisecond)
	if n := len(youtubeChat(t, r, typeOnline)) + len(youtubeChat(t, r, typeOffline)); n != 0 {
		t.Fatalf("%d go-live events for a chat that's off", n)
	}
}
