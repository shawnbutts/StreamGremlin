package followbots

import (
	"context"
	"errors"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

type fakeInbox struct{ recs []events.Record }

func (f fakeInbox) Latest(context.Context, int, events.Filter) ([]events.Record, error) {
	return f.recs, nil
}

func follow(id string, at time.Time, q events.Quality, source string) events.Record {
	return events.Record{Envelope: events.Envelope{Type: TypeFollow, Source: source, OccurredAt: at, DeliveryQuality: q,
		Actor: &events.Actor{Platform: source, ID: id, Login: "u" + id, DisplayName: "U" + id}}}
}

func TestFind(t *testing.T) {
	now := time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)
	made := map[string]time.Time{}
	var recs []events.Record
	// Five day-old accounts following within 3 minutes: a burst.
	for i, id := range []string{"b1", "b2", "b3", "b4", "b5"} {
		at := now.Add(-time.Hour + time.Duration(i)*30*time.Second)
		made[id] = at.Add(-24 * time.Hour)
		recs = append(recs, follow(id, at, events.Confirmed, "twitch"))
	}
	// A young account alone; an old one; a young one that chatted; one too
	// long ago; a test follow; a YouTube subscriber; one Twitch can't age.
	made["lone"], made["old"], made["talker"], made["ancient"] = now.Add(-50*time.Hour), now.Add(-900*24*time.Hour), now.Add(-time.Hour), now.Add(-time.Hour)
	recs = append(recs, follow("lone", now.Add(-5*time.Hour), events.Confirmed, "twitch"), follow("old", now.Add(-2*time.Hour), events.Confirmed, "twitch"),
		follow("talker", now.Add(-30*time.Minute), events.Confirmed, "twitch"), follow("ancient", now.Add(-48*time.Hour), events.Confirmed, "twitch"),
		follow("test", now.Add(-time.Minute), events.Synthetic, "twitch"), follow("yt", now.Add(-time.Minute), events.Confirmed, "youtube"),
		follow("ghost", now.Add(-time.Minute), events.Confirmed, "twitch"), follow("b1", now.Add(-10*time.Minute), events.Confirmed, "twitch"))
	lookups := 0
	d := Deps{Inbox: fakeInbox{recs}, Now: func() time.Time { return now },
		Users: func(_ context.Context, _, ids []string) ([]twitch.User, twitch.RateInfo, error) {
			lookups++
			var out []twitch.User
			for _, id := range ids {
				if m, ok := made[id]; ok {
					out = append(out, twitch.User{ID: id, CreatedAt: m})
				}
			}
			return out, twitch.RateInfo{}, nil
		},
		Chatted: func(_ context.Context, id string) (bool, error) { return id == "talker", nil }}
	rv, err := Find(context.Background(), d, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if rv.Follows != 9 || rv.Unknown != 1 || len(rv.Suspects) != 6 || lookups != 1 {
		t.Fatalf("%+v lookups %d", rv, lookups)
	}
	burst := 0
	for _, s := range rv.Suspects {
		if s.InBurst {
			burst++
		}
		if s.UserID == "lone" && (s.InBurst || s.AgeDays != 1) {
			t.Fatalf("%+v", s)
		}
	}
	if burst != 5 || rv.Suspects[len(rv.Suspects)-1].UserID != "lone" {
		t.Fatalf("%+v", rv.Suspects)
	}
	if _, err := Find(context.Background(), d, Options{Hours: 0, Days: 7, Burst: 5, Minutes: 10}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
