package livechat

import (
	"bytes"
	"reflect"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestRoundTrip(t *testing.T) {
	req := Request{LiveChatID: "chat1", Hl: "en", ProfileImageSize: 88, PageToken: "tok", Part: []string{"id", "snippet", "authorDetails"}}
	got, err := ParseRequest(req.Marshal())
	if err != nil || !reflect.DeepEqual(got, req) {
		t.Fatalf("%+v %v", got, err)
	}
	resp := Response{OfflineAt: "2026-11-14T01:02:03Z", NextPageToken: "next", Items: []Message{{
		ID: "m1",
		Snippet: Snippet{Type: TypeText, PublishedAt: "2026-11-14T01:00:00Z", DisplayMessage: "hi", HasDisplayContent: true,
			Text: "hi", LiveChatID: "chat1", AuthorChannelID: "UCa"},
		Author: Author{IsVerified: true, IsChatOwner: true, IsChatSponsor: true, IsChatModerator: true,
			ChannelURL: "u", DisplayName: "A", ProfileImageURL: "p", ChannelID: "UCa"},
	}, {ID: "m2", Snippet: Snippet{Type: TypeChatEnded}}}}
	back, err := ParseResponse(resp.Marshal())
	if err != nil || !reflect.DeepEqual(back, resp) {
		t.Fatalf("%+v\n%+v %v", back, resp, err)
	}
}

// TestFieldNumbers builds a response field by field from Google's
// stream_list.proto numbers, independently of Marshal.
func TestFieldNumbers(t *testing.T) {
	str := func(b []byte, n protowire.Number, s string) []byte {
		b = protowire.AppendTag(b, n, protowire.BytesType)
		return protowire.AppendString(b, s)
	}
	msg := func(b []byte, n protowire.Number, m []byte) []byte {
		b = protowire.AppendTag(b, n, protowire.BytesType)
		return protowire.AppendBytes(b, m)
	}
	vint := func(b []byte, n protowire.Number, v uint64) []byte {
		b = protowire.AppendTag(b, n, protowire.VarintType)
		return protowire.AppendVarint(b, v)
	}
	var text []byte
	text = str(text, 1, "hello")
	var sn []byte
	sn = vint(sn, 1, 1) // TEXT_MESSAGE_EVENT
	sn = str(sn, 4, "2026-11-14T00:00:00Z")
	sn = str(sn, 16, "hello")
	sn = vint(sn, 17, 1)
	sn = msg(sn, 19, text)
	sn = str(sn, 201, "chat")
	sn = str(sn, 301, "UCx")
	sn = vint(sn, 999, 7) // unknown fields are skipped
	var au []byte
	au = vint(au, 5, 1)
	au = vint(au, 7, 1)
	au = str(au, 103, "X")
	au = str(au, 10101, "UCx")
	var m []byte
	m = str(m, 101, "id1")
	m = msg(m, 2, sn)
	m = msg(m, 3, au)
	var r []byte
	r = str(r, 200, "youtube#liveChatMessageListResponse")
	r = msg(r, 1007, m)
	r = str(r, 100602, "p1")
	r = str(r, 2, "offline")
	got, err := ParseResponse(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.NextPageToken != "p1" || got.OfflineAt != "offline" || len(got.Items) != 1 {
		t.Fatalf("%+v", got)
	}
	it := got.Items[0]
	if it.ID != "id1" || it.Snippet.Type != TypeText || it.Snippet.Text != "hello" || it.Snippet.AuthorChannelID != "UCx" ||
		!it.Snippet.HasDisplayContent || it.Snippet.LiveChatID != "chat" || !it.Author.IsChatOwner || !it.Author.IsChatModerator ||
		it.Author.IsChatSponsor || it.Author.DisplayName != "X" || it.Author.ChannelID != "UCx" {
		t.Fatalf("%+v", it)
	}
	// The request, likewise.
	var want []byte
	want = str(want, 1, "chat")
	want = str(want, 99, "p1")
	want = str(want, 100, "snippet")
	if b := (Request{LiveChatID: "chat", PageToken: "p1", Part: []string{"snippet"}}).Marshal(); !bytes.Equal(b, want) {
		t.Fatalf("%x\n%x", b, want)
	}
}

func TestMalformed(t *testing.T) {
	for _, b := range [][]byte{{0xff}, {0x0a, 0x05, 'a'}, {0xba, 0xc1, 0x3d}} {
		if _, err := ParseResponse(b); err == nil {
			t.Fatalf("%x parsed", b)
		}
	}
	if TypeText.String() != "textMessageEvent" || Type(99).String() != "type99" {
		t.Fatal("names")
	}
}

func TestDetailsRoundTrip(t *testing.T) {
	for _, sn := range []Snippet{
		{Type: TypeSuperChat, SuperChat: &SuperChat{AmountMicros: 5_000_000, Currency: "USD", AmountDisplayString: "$5.00", UserComment: "gg", Tier: 2}},
		{Type: TypeSuperSticker, SuperSticker: &SuperSticker{AmountMicros: 2_000_000, Currency: "EUR", AmountDisplayString: "€2.00", Tier: 1, StickerID: "s1", AltText: "A happy cat"}},
		{Type: TypeNewSponsor, NewSponsor: &NewSponsor{MemberLevelName: "Gold", IsUpgrade: true}},
		{Type: TypeMemberMilestoneChat, MemberMilestone: &MemberMilestone{MemberLevelName: "Gold", MemberMonth: 12, UserComment: "a year!"}},
		{Type: TypeMembershipGifting, MembershipGifting: &MembershipGifting{Count: 5, LevelName: "Gold"}},
		{Type: TypeGiftMembershipReceived, GiftMembershipReceived: &GiftMembershipReceived{MemberLevelName: "Gold", GifterChannelID: "UCg", GiftingID: "m9"}},
		{Type: TypeUserBanned, UserBanned: &UserBanned{ChannelID: "UCb", ChannelURL: "u", DisplayName: "Spammer", BanType: BanTemporary, DurationSeconds: 300}},
		{Type: TypeGift, Gift: &Gift{Name: "Rose", JewelsCount: 10, AltText: "a rose", ComboCount: 3}},
	} {
		m := Message{ID: "x", Snippet: sn}
		back, err := ParseResponse(Response{Items: []Message{m}}.Marshal())
		if err != nil || len(back.Items) != 1 || !reflect.DeepEqual(back.Items[0].Snippet, sn) {
			t.Fatalf("%s: %+v %v", sn.Type, back, err)
		}
	}
}

// Super Chat and a ban, built field by field from Google's numbers.
func TestDetailFieldNumbers(t *testing.T) {
	var sc []byte
	sc = protowire.AppendTag(sc, 1, protowire.VarintType)
	sc = protowire.AppendVarint(sc, 1_500_000)
	sc = protowire.AppendTag(sc, 3, protowire.BytesType)
	sc = protowire.AppendString(sc, "£1.50")
	var ban []byte
	var prof []byte
	prof = protowire.AppendTag(prof, 101, protowire.BytesType)
	prof = protowire.AppendString(prof, "UCb")
	ban = protowire.AppendTag(ban, 1, protowire.BytesType)
	ban = protowire.AppendBytes(ban, prof)
	ban = protowire.AppendTag(ban, 2, protowire.VarintType)
	ban = protowire.AppendVarint(ban, 1)
	var sn []byte
	sn = protowire.AppendTag(sn, 27, protowire.BytesType)
	sn = protowire.AppendBytes(sn, sc)
	sn = protowire.AppendTag(sn, 22, protowire.BytesType)
	sn = protowire.AppendBytes(sn, ban)
	s, err := parseSnippet(sn)
	if err != nil || s.SuperChat == nil || s.SuperChat.AmountMicros != 1_500_000 || s.SuperChat.AmountDisplayString != "£1.50" ||
		s.UserBanned == nil || s.UserBanned.ChannelID != "UCb" || s.UserBanned.BanType != BanPermanent {
		t.Fatalf("%+v %v", s, err)
	}
}
