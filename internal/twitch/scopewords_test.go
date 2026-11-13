package twitch

import (
	"slices"
	"testing"
)

// Every permission either account asks for can be said in words.
func TestEveryScopeHasWords(t *testing.T) {
	for _, s := range append(slices.Clone(StreamerScopes), BotScopes...) {
		if scopeWords[s] == "" {
			t.Errorf("%s has no words", s)
		}
	}
	got := ScopeWords([]string{BroadcastScope, ClipsScope, AdsReadScope, CommercialScope, "made:up"})
	want := []string{"change your title and category and add markers", "make clips", "run and snooze ads", "made:up"}
	if !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
}
