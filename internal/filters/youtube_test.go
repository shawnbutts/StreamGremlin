package filters

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

type fakeYouTube struct {
	mu   sync.Mutex
	sent []youtube.Mod
}

func (f *fakeYouTube) Moderate(_ context.Context, m youtube.Mod) (youtube.ModOutcome, error) {
	if err := youtube.CheckMod(m); err != nil {
		return youtube.ModOutcome{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return youtube.ModOutcome{State: youtube.ModDone, ID: "ban1"}, nil
}

func (f *fakeYouTube) all() []youtube.Mod {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]youtube.Mod(nil), f.sent...)
}

// sayYouTube stores a YouTube chat message as the adapter does.
func (r *rig) sayYouTube(channel, text string, backlog bool) events.Record {
	r.t.Helper()
	r.n++
	id := "LCC.yt" + strconv.Itoa(r.n)
	p := map[string]any{"message_id": id, "text": text, "emotes": []string{},
		"chatter": map[string]any{"id": channel, "login": "", "display_name": "Fan", "role": "viewer"}}
	if backlog {
		p["backlog"] = true
	}
	raw, _ := json.Marshal(p)
	env := events.Envelope{Type: "chat.message.received", Source: events.SourceYouTube, SourceEventID: id, OccurredAt: time.Now(),
		ChannelID: "UCstreamer", Actor: &events.Actor{Platform: events.SourceYouTube, ID: channel, DisplayName: "Fan"}, Payload: raw,
		DeliveryQuality: events.Confirmed}
	res, err := r.inbox.Accept(context.Background(), env)
	if err != nil {
		r.t.Fatal(err)
	}
	return res.Record
}

func youtubeRig(t *testing.T, left int) (*rig, *fakeYouTube) {
	r := newRig(t)
	f := &fakeYouTube{}
	r.mods.UseYouTube(f)
	r.svc.o.YouTubeQuotaLeft = func(context.Context) int { return left }
	return r, f
}

// On YouTube a rule deletes, whatever it does on Twitch, unless it's set
// to do the same; backlog, the streamer and Twitch-only actions are
// handled (D-100).
func TestYouTubeFiltersDeleteByDefault(t *testing.T) {
	r, f := youtubeRig(t, 9000)
	ctx := context.Background()
	r.set(func(s *Settings) {
		s.Links.On, s.Links.Action, s.Links.Seconds = true, ActionTimeout, 600
		s.Caps.On, s.Caps.Action = true, ActionWarn
	})
	link := r.sayYouTube("UCfan", "free followers at bit.ly/x", false)
	r.svc.handle(ctx, link)
	r.svc.handle(ctx, r.sayYouTube("UCfan", "THIS IS ALL CAPITALS SHOUTING", false))
	r.svc.handle(ctx, r.sayYouTube("UCfan", "old spam at bit.ly/y", true))       // backlog
	r.svc.handle(ctx, r.sayYouTube("UCstreamer", "my site at bit.ly/me", false)) // you
	sent := f.all()
	if len(sent) != 2 || sent[0].Action != youtube.ModDelete || sent[0].MessageID != link.SourceEventID || sent[1].Action != youtube.ModDelete {
		t.Fatalf("%+v", sent)
	}
	c := r.caught()
	if len(c) != 2 || c[0]["action"] != ActionDelete {
		t.Fatalf("%+v", c)
	}
	var warned map[string]any
	for _, x := range c {
		if x["rule"] == RuleCaps {
			warned = x
		}
	}
	if warned == nil || warned["note"] == nil {
		t.Fatalf("no note on a warning turned delete: %+v", c)
	}
	list, _ := r.mods.List(ctx, events.SourceYouTube, "UCfan", 10)
	if len(list) != 2 || list[0].Platform != events.SourceYouTube {
		t.Fatalf("%+v", list)
	}
}

func TestYouTubeFiltersTimeOutWhenAskedAndTheQuotaAllows(t *testing.T) {
	r, f := youtubeRig(t, 5000)
	ctx := context.Background()
	r.set(func(s *Settings) {
		s.Links.On, s.Links.Action, s.Links.Seconds, s.Links.YouTubeSame = true, ActionTimeout, 600, true
	})
	r.svc.handle(ctx, r.sayYouTube("UCfan", "bit.ly/x", false))
	if s := f.all(); len(s) != 1 || s[0].Action != youtube.ModTimeout || s[0].Seconds != 600 || s[0].ChannelID != "UCfan" {
		t.Fatalf("%+v", s)
	}
	// Low on quota: deletes instead, and says why.
	r.svc.o.YouTubeQuotaLeft = func(context.Context) int { return YouTubeQuotaFloor - 1 }
	r.svc.handle(ctx, r.sayYouTube("UCother", "bit.ly/y", false))
	if s := f.all(); len(s) != 2 || s[1].Action != youtube.ModDelete {
		t.Fatalf("%+v", s)
	}
	if c := r.caught(); len(c) != 2 || c[1]["note"] == nil {
		t.Fatalf("%+v", c)
	}
}

func TestOnYouTube(t *testing.T) {
	for _, c := range []struct {
		r       Rule
		left    int
		action  string
		seconds int
		why     bool
	}{
		{Rule{Action: ActionDelete}, 0, ActionDelete, 0, false},
		{Rule{Action: ActionWarn}, 9000, ActionDelete, 0, true},
		{Rule{Action: ActionTimeout, Seconds: 60}, 9000, ActionDelete, 0, false},
		{Rule{Action: ActionTimeout, Seconds: 60, YouTubeSame: true}, 9000, ActionTimeout, 60, false},
		{Rule{Action: ActionTimeout, Seconds: 60, YouTubeSame: true}, 999, ActionDelete, 0, true},
	} {
		a, s, why := c.r.OnYouTube(c.left)
		if a != c.action || s != c.seconds || (why != "") != c.why {
			t.Errorf("%+v %d: %s %d %q", c.r, c.left, a, s, why)
		}
	}
}
