package livechat

import (
	"reflect"
	"testing"
)

// FuzzParseResponse: YouTube's chat stream is outside input. Decoding
// never panics, and what's decoded, written again, decodes to the same.
func FuzzParseResponse(f *testing.F) {
	f.Add(Response{OfflineAt: "2026-11-14T01:02:03Z", NextPageToken: "next", Items: []Message{{
		ID:      "m1",
		Snippet: Snippet{Type: TypeText, PublishedAt: "2026-11-14T01:00:00Z", DisplayMessage: "hi", HasDisplayContent: true, Text: "hi", AuthorChannelID: "UCa"},
		Author:  Author{IsChatModerator: true, DisplayName: "A", ChannelID: "UCa"},
	}, {ID: "m2", Snippet: Snippet{Type: TypeChatEnded}}}}.Marshal())
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := ParseResponse(b)
		if err != nil {
			return
		}
		again, err := ParseResponse(r.Marshal())
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Fatalf("written again:\n%+v\n%+v %v", r, again, err)
		}
	})
}
