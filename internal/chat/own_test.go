package chat

import (
	"testing"
	"time"
)

func TestOwnSends(t *testing.T) {
	var o OwnSends
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	// The echo before Twitch answers: matched by text.
	o.Expect("hi", now)
	if !o.Claim("m1", "hi", now) || o.Claim("m1", "hi", now) {
		t.Fatal("by text, once")
	}
	o.Settle("hi", "m1", now) // late answer: nothing left to settle
	if o.Claim("m1", "hi", now) {
		t.Fatal("claimed after settling")
	}
	// Twitch answers first: matched by id, not by text.
	o.Expect(" hey ", now)
	o.Settle("hey", "m2", now)
	if o.Claim("other", "hey", now) || !o.Claim("m2", "hey", now) {
		t.Fatal("by id")
	}
	// A failed send expects no echo.
	o.Expect("nope", now)
	o.Settle("nope", "", now)
	if o.Claim("m3", "nope", now) {
		t.Fatal("failed send claimed")
	}
	// An echo that never came is forgotten.
	o.Expect("lost", now)
	if o.Claim("m4", "lost", now.Add(OwnSendTTL)) {
		t.Fatal("expired send claimed")
	}
	if len(o.sent) != 0 {
		t.Fatalf("%+v", o.sent)
	}
}
