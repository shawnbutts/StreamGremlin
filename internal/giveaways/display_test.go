package giveaways

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
)

// fakeDisplay records what giveaway overlays would be told.
type fakeDisplay struct {
	mu       sync.Mutex
	pages    int
	views    []*overlays.GiveawayView
	draws    []overlays.GiveawayDraw
	pictures map[string]string
}

func (f *fakeDisplay) SetGiveaway(v *overlays.GiveawayView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.views = append(f.views, v)
}

func (f *fakeDisplay) GiveawayDraw(d overlays.GiveawayDraw) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.draws = append(f.draws, d)
}

func (f *fakeDisplay) GiveawayPicture(id, pic string, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pictures == nil {
		f.pictures = map[string]string{}
	}
	f.pictures[id] = pic
}

func (f *fakeDisplay) GiveawayPages() int { f.mu.Lock(); defer f.mu.Unlock(); return f.pages }

func (f *fakeDisplay) last() overlays.GiveawayView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.views[len(f.views)-1]
}

func (f *fakeDisplay) drawn() []overlays.GiveawayDraw {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]overlays.GiveawayDraw(nil), f.draws...)
}

// Giveaway overlays are told the state after each change, and each winner
// after the draw with names to roll through (the winner last). With an
// overlay open in OBS, a winner's chat message waits for the roll, and the
// next winner's one roll more; the picture follows.
func TestTheOverlayIsToldAndChatWaitsForTheRoll(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.shown.pages = 1
	g, _ := r.svc.Open(ctx, Settings{Title: "a mug", Winners: 2, ClaimSeconds: 60}, "app")
	if v := r.shown.last(); v.State != Open || v.HowToEnter != "Type !join in chat to enter" || v.Entries != 0 || v.Practice {
		t.Fatal(v)
	}
	for _, v := range []string{"ann", "bob", "cat"} {
		r.say(v, "join", false)
	}
	r.entries(g.ID, 3)
	r.eventually("the count", func() bool { return r.shown.last().Entries == 3 })
	draws, err := r.svc.Draw(ctx, g.ID, 0)
	if err != nil || len(draws) != 2 {
		t.Fatal(draws, err)
	}
	shown := r.shown.drawn()
	if len(shown) != 2 || shown[0].Name != draws[0].Winner.DisplayName || shown[0].Roll[len(shown[0].Roll)-1] != shown[0].Name ||
		len(shown[0].Roll) != 3 || shown[0].Claim != "Type !claim within 60 seconds to claim it." {
		t.Fatal(shown)
	}
	if v := r.shown.last(); v.State != Closed || len(v.Winners) != 2 || v.Winners[0].Outcome != PendingClaim {
		t.Fatal(v)
	}
	r.eventually("the pictures", func() bool {
		r.shown.mu.Lock()
		defer r.shown.mu.Unlock()
		return strings.HasSuffix(r.shown.pictures[draws[0].ID], draws[0].Winner.UserID+".png")
	})
	// Chat waits for the roll: nothing names the winner yet.
	time.Sleep(1200 * time.Millisecond)
	if strings.Contains(r.chat.all(), " wins a mug!") {
		t.Fatal("chat named a winner before the roll landed:\n" + r.chat.all())
	}
	r.wait(overlays.ChatWait)
	r.eventually("the first winner", func() bool { return strings.Count(r.chat.all(), " wins a mug!") == 1 })
	r.wait(overlays.DrawTime)
	r.eventually("the second winner", func() bool { return strings.Count(r.chat.all(), " wins a mug!") == 2 })
	if _, err := r.svc.Cancel(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if v := r.shown.last(); v.State != Cancelled || v.EndedAt == nil {
		t.Fatal(v)
	}
}

// With no giveaway overlay open, chat names winners at once. A practice
// draw is shown (to the app's previews) and never waits.
func TestNoOverlayNoWait(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, _ := r.svc.Open(ctx, Settings{Title: "a pin"}, "app")
	r.say("ann", "join", false)
	r.entries(g.ID, 1)
	if _, err := r.svc.Draw(ctx, g.ID, 1); err != nil {
		t.Fatal(err)
	}
	r.eventually("the winner at once", func() bool { return strings.Contains(r.chat.all(), "@ann wins a pin!") })
	_, _ = r.svc.End(ctx, g.ID)

	r.shown.pages = 1
	p, _ := r.svc.Open(ctx, Settings{Title: "p", Practice: true}, "app")
	if !r.shown.last().Practice {
		t.Fatal("practice state not marked")
	}
	r.say("tester", "join", true)
	r.entries(p.ID, 1)
	if _, err := r.svc.Draw(ctx, p.ID, 1); err != nil {
		t.Fatal(err)
	}
	d := r.shown.drawn()
	if !d[len(d)-1].Practice {
		t.Fatal("a practice draw not marked")
	}
	var after *string
	_ = r.db.QueryRowContext(ctx, "SELECT send_after FROM giveaway_messages WHERE giveaway_id = ? AND text LIKE '%wins%'", p.ID).Scan(&after)
	if after != nil {
		t.Fatal("a practice winner waited")
	}
}

// A roll shows at most MaxRoll names, the winner last.
func TestTheRollIsCapped(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	g, _ := r.svc.Open(ctx, Settings{Title: "many"}, "app")
	for i := 0; i < 40; i++ {
		r.say("v"+string(rune('a'+i%26))+string(rune('a'+i/26)), "join", false)
	}
	r.entries(g.ID, 40)
	draws, _ := r.svc.Draw(ctx, g.ID, 1)
	d := r.shown.drawn()
	if len(d) != 1 || len(d[0].Roll) != overlays.MaxRoll || d[0].Roll[overlays.MaxRoll-1] != draws[0].Winner.DisplayName {
		t.Fatal(d)
	}
}
