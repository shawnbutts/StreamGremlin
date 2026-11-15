package twitch

import (
	"encoding/json"
	"testing"
	"time"
)

// FuzzChannelEnvelope: EventSub's notifications are outside input. Any
// event that's turned into an inbox event is one the inbox accepts.
func FuzzChannelEnvelope(f *testing.F) {
	for _, s := range [][2]string{
		{"channel.follow", `{"user_id":"1","user_login":"a","user_name":"A","broadcaster_user_id":"9","followed_at":"2026-11-15T00:00:00Z"}`},
		{"channel.raid", `{"from_broadcaster_user_id":"2","from_broadcaster_user_login":"b","from_broadcaster_user_name":"B","to_broadcaster_user_id":"9","viewers":5}`},
		{"channel.cheer", `{"user_id":"1","user_login":"a","bits":100,"message":"hi","is_anonymous":false}`},
		{"channel.subscribe", `{"user_id":"1","tier":"1000","is_gift":false}`},
		{"channel.shield_mode.begin", `{"broadcaster_user_id":"9","moderator_user_id":"3","moderator_user_login":"m"}`},
		{"stream.online", `{"id":"s1","broadcaster_user_id":"9","type":"live"}`},
		{"channel.ban", `{"user_id":"1","moderator_user_id":"3","reason":"x","is_permanent":true}`},
		{"channel.follow", `{"user_id":"","broadcaster_user_id":"9"}`},
	} {
		f.Add(s[0], s[1])
	}
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, typ, raw string) {
		env, ok := channelEnvelope(typ, "msg-1", json.RawMessage(raw), at)
		if !ok {
			return
		}
		env.Prepare(at)
		if err := env.Validate(); err != nil {
			t.Fatalf("%s %s gave an event the inbox refuses: %v", typ, raw, err)
		}
	})
}
