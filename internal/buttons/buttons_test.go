package buttons

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

type rig struct {
	t   *testing.T
	ctx context.Context
	in  *events.Inbox
	svc *Service
	now time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, ctx: ctx, in: events.NewInbox(db, nil), now: time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)}
	r.svc = New(db, r.in, func() time.Time { return r.now })
	return r
}

func (r *rig) save(b Button) Button {
	r.t.Helper()
	got, _, err := r.svc.Save(r.ctx, b)
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

func ids(bs []Button) []string {
	out := []string{}
	for _, b := range bs {
		out = append(out, b.ID)
	}
	return out
}

func TestSaveListOrder(t *testing.T) {
	r := newRig(t)
	r.save(Button{ID: "brb", Label: "Be right back"})
	r.save(Button{ID: "hype", Label: "Hype!", Color: "red"})
	r.save(Button{ID: "end", Label: "End the stream"})
	list, err := r.svc.List(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(list); len(got) != 3 || got[0] != "brb" || got[1] != "hype" || got[2] != "end" {
		t.Fatalf("new buttons go last, in order: %v", got)
	}
	// Changing one keeps its place.
	b, created, err := r.svc.Save(r.ctx, Button{ID: "brb", Label: "BRB", Color: "blue"})
	if err != nil || created || b.Label != "BRB" || b.Color != "blue" || b.Position != 0 {
		t.Fatalf("change: %+v %v %v", b, created, err)
	}
	if err := r.svc.Move(r.ctx, []string{"end", "brb", "hype"}); err != nil {
		t.Fatal(err)
	}
	list, _ = r.svc.List(r.ctx)
	if got := ids(list); got[0] != "end" || got[1] != "brb" || got[2] != "hype" {
		t.Fatalf("moved: %v", got)
	}
	// A move must name every button, once.
	for _, bad := range [][]string{{"end", "brb"}, {"end", "brb", "brb"}, {"end", "brb", "nope"}} {
		if err := r.svc.Move(r.ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("move %v: %v", bad, err)
		}
	}
	existed, err := r.svc.Delete(r.ctx, "brb")
	if err != nil || !existed {
		t.Fatal(existed, err)
	}
	if existed, _ := r.svc.Delete(r.ctx, "brb"); existed {
		t.Fatal("deleted twice")
	}
	r.save(Button{ID: "raid", Label: "Raid out"})
	list, _ = r.svc.List(r.ctx)
	if got := ids(list); len(got) != 3 || got[2] != "raid" {
		t.Fatalf("after delete, a new one still goes last: %v", got)
	}
}

// An icon from the bundled pack, and the label hidden beside it: saved,
// changed, cleared; a hidden label needs an icon; a bad name is refused.
func TestIcons(t *testing.T) {
	r := newRig(t)
	b, _, err := r.svc.Save(r.ctx, Button{ID: "brb", Label: "Be right back", Icon: "tabler:player-pause", HideLabel: true})
	if err != nil || b.Icon != "tabler:player-pause" || !b.HideLabel {
		t.Fatalf("%+v %v", b, err)
	}
	if got, _ := r.svc.Get(r.ctx, "brb"); got.Icon != "tabler:player-pause" || !got.HideLabel {
		t.Fatalf("read back: %+v", got)
	}
	// Changing only the icon is a change.
	if b, _, _ = r.svc.Save(r.ctx, Button{ID: "brb", Label: "Be right back", Icon: "tabler:brand-twitch", HideLabel: true}); b.Icon != "tabler:brand-twitch" {
		t.Fatalf("icon changed: %+v", b)
	}
	// Nerd Fonts' names have underscores.
	if b, _, err = r.svc.Save(r.ctx, Button{ID: "brb", Label: "Be right back", Icon: "nf:md-microphone_off", HideLabel: true}); err != nil || b.Icon != "nf:md-microphone_off" {
		t.Fatalf("nerd fonts: %+v %v", b, err)
	}
	if b, _, _ = r.svc.Save(r.ctx, Button{ID: "brb", Label: "Be right back", Icon: "tabler:brand-twitch"}); b.HideLabel {
		t.Fatalf("label shown again: %+v", b)
	}
	if b, _, _ = r.svc.Save(r.ctx, Button{ID: "brb", Label: "Be right back"}); b.Icon != "" {
		t.Fatalf("icon cleared: %+v", b)
	}
	for _, bad := range []Button{
		{ID: "x", Label: "x", HideLabel: true},
		{ID: "x", Label: "x", Icon: "player-pause"},
		{ID: "x", Label: "x", Icon: "fa:star"},
		{ID: "x", Label: "x", Icon: "tabler:Star"},
		{ID: "x", Label: "x", Icon: "tabler:../star"},
		{ID: "x", Label: "x", Icon: "tabler:player_pause"},
		{ID: "x", Label: "x", Icon: "nf:md"},
		{ID: "x", Label: "x", Icon: "nf:-md-x"},
		{ID: "x", Label: "x", Icon: "tabler:" + strings.Repeat("a", 80)},
	} {
		if _, _, err := r.svc.Save(r.ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
}

func TestCheck(t *testing.T) {
	r := newRig(t)
	long := ""
	for range MaxLabel + 1 {
		long += "x"
	}
	for _, b := range []Button{
		{ID: "", Label: "x"},
		{ID: "Bad Id", Label: "x"},
		{ID: "ok", Label: "  "},
		{ID: "ok", Label: long},
		{ID: "ok", Label: "x", Color: "chartreuse"},
	} {
		if _, _, err := r.svc.Save(r.ctx, b); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", b, err)
		}
	}
	for i := range MaxButtons {
		r.save(Button{ID: "b" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Label: "x"})
	}
	if _, _, err := r.svc.Save(r.ctx, Button{ID: "onemore", Label: "x"}); !errors.Is(err, ErrFull) {
		t.Fatalf("full: %v", err)
	}
}

func TestPress(t *testing.T) {
	r := newRig(t)
	r.save(Button{ID: "hype", Label: "Hype!"})
	b, rec, dup, err := r.svc.Press(r.ctx, "hype", FromLive, "req-1")
	if err != nil || dup || b.ID != "hype" {
		t.Fatalf("press: %+v %v %v", b, dup, err)
	}
	if rec.Type != Pressed || rec.Source != "buttons" || rec.Actor != nil || rec.DeliveryQuality != events.Confirmed {
		t.Fatalf("event: %+v", rec)
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p["button_id"] != "hype" || p["label"] != "Hype!" || p["from"] != "live" {
		t.Fatalf("payload: %v", p)
	}
	// The same request again, later, is the same press: nothing runs twice.
	r.now = r.now.Add(2 * time.Second)
	_, again, dup, err := r.svc.Press(r.ctx, "hype", FromLive, "req-1")
	if err != nil || !dup || again.Seq != rec.Seq {
		t.Fatalf("repeat: %v %v %v", again.Seq, dup, err)
	}
	// Another request is another press; no request id is always another.
	if _, rec2, dup, _ := r.svc.Press(r.ctx, "hype", FromCLI, "req-2"); dup || rec2.Seq == rec.Seq {
		t.Fatal("a new request is a new press")
	}
	_, a, _, _ := r.svc.Press(r.ctx, "hype", FromLive, "")
	_, c, _, _ := r.svc.Press(r.ctx, "hype", FromLive, "")
	if a.Seq == c.Seq {
		t.Fatal("presses without a request id are each new")
	}
	if _, _, _, err := r.svc.Press(r.ctx, "nope", FromLive, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, _, err := r.svc.Press(r.ctx, "hype", "elsewhere", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("from: %v", err)
	}
	if _, _, _, err := r.svc.Press(r.ctx, "hype", FromLive, "has space"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("request id: %v", err)
	}
	// A button deleted and made again under the same id is a new button:
	// an old request id doesn't swallow its first press.
	if _, err := r.svc.Delete(r.ctx, "hype"); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(time.Minute)
	r.save(Button{ID: "hype", Label: "Hype again"})
	if _, _, dup, _ := r.svc.Press(r.ctx, "hype", FromLive, "req-1"); dup {
		t.Fatal("a new button's first press isn't a repeat")
	}
}
