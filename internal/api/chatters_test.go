package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
)

// The viewer card through the API: signed out, every Twitch fact is
// unknown (never "no") but StreamGremlin's record and messages still show;
// signed in, Twitch's facts; a test chatter isn't looked up on Twitch;
// bad input is refused.
func TestChatterCardThroughTheAPI(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	card := func(path string) (int, ChatterCard) {
		w := do(t, s, req{method: "GET", path: path, token: testToken})
		var c ChatterCard
		_ = json.Unmarshal(w.Body.Bytes(), &c)
		return w.Code, c
	}
	fan := testMock.UserID("cardfan")
	chat := func(id, text string, quality events.Quality) {
		t.Helper()
		_, err := testEvents.Accept(ctx, events.Envelope{Type: "chat.message.received", Source: "twitch", SourceEventID: id,
			OccurredAt: time.Now(), Actor: &events.Actor{Platform: "twitch", ID: fan, Login: "cardfan", DisplayName: "CardFan"},
			Payload: json.RawMessage(`{"text":"` + text + `"}`), DeliveryQuality: quality})
		if err != nil {
			t.Fatal(err)
		}
	}
	chat("c1", "hello", events.Confirmed)
	chat("c2", "again", events.Confirmed)
	chat("c3", "a test", events.Synthetic)
	if err := testViewers.CatchUp(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := testViewers.ByAccount(ctx, "twitch", fan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testLedger.Credit(ctx, points.Op{ID: "card-1", Viewer: v.ID, Amount: 25, By: "test"}); err != nil {
		t.Fatal(err)
	}

	code, c := card("/v1/chatters/twitch/" + fan)
	if code != 200 || c.Twitch == nil || len(c.Twitch.Unknown) != 6 || c.Twitch.Follow != nil {
		t.Fatalf("signed out: %d %+v", code, c.Twitch)
	}
	if c.Viewer == nil || c.Viewer.Balance != 25 || c.Viewer.Messages != 2 || len(c.Messages) != 2 || c.Currency == "" {
		t.Fatalf("record: %+v %d", c.Viewer, len(c.Messages))
	}

	do(t, s, req{method: "PUT", path: "/v1/connections/twitch/client-id", token: testToken, body: `{"client_id":"abcdefghij0123456789"}`})
	w := do(t, s, req{method: "POST", path: "/v1/connections/twitch/streamer/sign-in", token: testToken})
	var p struct {
		UserCode string `json:"user_code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	_ = testMock.Approve(p.UserCode, "cardstreamer")
	testMock.SetVIP("cardstreamer", "cardfan", true)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, c = card("/v1/chatters/twitch/" + fan)
		if len(c.Twitch.Unknown) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("signed in: %+v", c.Twitch)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if c.Twitch.Login != "cardfan" || c.Twitch.VIP == nil || !*c.Twitch.VIP || c.Twitch.Follow == nil || c.Twitch.Follow.Follows {
		t.Fatalf("facts: %+v", c.Twitch)
	}

	// A test chatter: no Twitch, no record, the test messages.
	if code, c := card("/v1/chatters/twitch/" + fan + "?test=1"); code != 200 || c.Twitch != nil || c.Viewer != nil || len(c.Messages) != 1 {
		t.Fatalf("test: %d %+v", code, c)
	}
	// Someone StreamGremlin has no record of.
	if _, c := card("/v1/chatters/twitch/" + testMock.UserID("stranger")); c.Viewer != nil || len(c.Messages) != 0 || c.Twitch == nil {
		t.Fatalf("stranger: %+v", c)
	}
	// A YouTube chatter (D-098): their messages only; Twitch isn't asked.
	if code, c := card("/v1/chatters/youtube/UCnobody"); code != 200 || c.Twitch != nil || c.Viewer != nil || len(c.Messages) != 0 {
		t.Fatalf("youtube: %d %+v", code, c)
	}
	for _, bad := range []string{"/v1/chatters/kick/1", "/v1/chatters/twitch/abc"} {
		if code, _ := card(bad); code != 400 {
			t.Fatal(bad, code)
		}
	}
}
