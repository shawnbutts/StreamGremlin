package viewers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

func newService(t *testing.T) (*Service, *events.Inbox, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	in := events.NewInbox(db, nil)
	return New(db, in, nil), in, db
}

func say(t *testing.T, in *events.Inbox, typ, id, login, name string, at time.Time, q events.Quality) {
	t.Helper()
	_, err := in.Accept(context.Background(), events.Envelope{Type: typ, Source: "twitch", Payload: []byte(`{}`), DeliveryQuality: q,
		ReceivedAt: at, Actor: &events.Actor{Platform: "twitch", ID: id, Login: login, DisplayName: name}})
	if err != nil {
		t.Fatal(err)
	}
}

// Viewers come from real events with a viewer: chat counts messages, other
// events only seen times; test events and core events never make one;
// catching up again counts nothing twice; names follow the newest event,
// and times never move the wrong way.
func TestViewersFromEvents(t *testing.T) {
	s, in, _ := newService(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	say(t, in, "chat.message.received", "1", "ann", "Ann", t0, events.Confirmed)
	say(t, in, "chat.message.received", "1", "ann", "Ann", t0.Add(time.Minute), events.Confirmed)
	say(t, in, "audience.follow.created", "2", "bob", "Bob", t0, events.Confirmed)
	say(t, in, "chat.message.received", "3", "tester", "Tester", t0, events.Synthetic)
	// An older event, arriving late, with an old name.
	say(t, in, "chat.message.received", "1", "ann_old", "AnnOld", t0.Add(-time.Hour), events.Confirmed)
	if err := s.CatchUp(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.CatchUp(ctx); err != nil {
		t.Fatal(err)
	}
	ann, err := s.ByAccount(ctx, "twitch", "1")
	if err != nil || ann.Login != "ann" || ann.Messages != 3 || !ann.FirstSeen.Equal(t0.Add(-time.Hour)) || !ann.LastSeen.Equal(t0.Add(time.Minute)) ||
		ann.LastChatAt == nil || !ann.LastChatAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("%+v %v", ann, err)
	}
	bob, _ := s.ByAccount(ctx, "twitch", "2")
	if bob.Messages != 0 || bob.LastChatAt != nil {
		t.Fatalf("%+v", bob)
	}
	if _, err := s.ByAccount(ctx, "twitch", "3"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a test event made a viewer")
	}
	// A rename follows the newest event.
	say(t, in, "chat.message.received", "1", "annie", "Annie", t0.Add(2*time.Minute), events.Confirmed)
	_ = s.CatchUp(ctx)
	if v, _ := s.ByLogin(ctx, "@ANNIE"); v.ID != ann.ID || v.DisplayName != "Annie" {
		t.Fatalf("%+v", v)
	}
	list, _ := s.Search(ctx, "an", 10)
	if len(list) != 1 || list[0].ID != ann.ID {
		t.Fatal(list)
	}
	if all, _ := s.Search(ctx, "", 10); len(all) != 2 || all[0].ID != ann.ID {
		t.Fatal(all)
	}
	if none, _ := s.Search(ctx, "%", 10); len(none) != 0 {
		t.Fatal("% matched as a wildcard:", none)
	}
}

// Forgetting blanks the account and its names but keeps the row; the same
// account later is a new, unconnected viewer. Ensure makes a record once.
func TestForgetAndEnsure(t *testing.T) {
	s, in, _ := newService(t)
	ctx := context.Background()
	say(t, in, "chat.message.received", "1", "ann", "Ann", time.Now(), events.Confirmed)
	_ = s.CatchUp(ctx)
	ann, _ := s.ByAccount(ctx, "twitch", "1")
	gone, err := s.Forget(ctx, ann.ID)
	if err != nil || gone.ForgottenAt == nil || gone.Login != "" || gone.UserID != "forgotten:"+ann.ID || gone.Messages != 0 {
		t.Fatalf("%+v %v", gone, err)
	}
	if _, err := s.ByLogin(ctx, "ann"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a forgotten viewer found by login")
	}
	say(t, in, "chat.message.received", "1", "ann", "Ann", time.Now(), events.Confirmed)
	_ = s.CatchUp(ctx)
	again, _ := s.ByAccount(ctx, "twitch", "1")
	if again.ID == ann.ID {
		t.Fatal("the forgotten viewer came back")
	}
	if _, err := s.Forget(ctx, "v_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	e1, err := s.Ensure(ctx, events.Actor{Platform: "twitch", ID: "9", Login: "Cat", DisplayName: "Cat"})
	e2, _ := s.Ensure(ctx, events.Actor{Platform: "twitch", ID: "9", Login: "cat"})
	if err != nil || e1.ID != e2.ID || e1.Login != "cat" {
		t.Fatal(e1, e2, err)
	}
}

// YouTube viewers are recorded like Twitch's, except chat from before
// StreamGremlin was reading; a name that's a Twitch login and a different
// YouTube handle is ambiguous until the two are linked (D-101).
func TestYouTubeViewersAndNames(t *testing.T) {
	s, in, db := newService(t)
	ctx := context.Background()
	now := time.Now()
	add := func(source, id, login, name, payload string) {
		t.Helper()
		if _, err := in.Accept(ctx, events.Envelope{Type: "chat.message.received", Source: source, Payload: []byte(payload),
			DeliveryQuality: events.Confirmed, ReceivedAt: now, Actor: &events.Actor{Platform: source, ID: id, Login: login, DisplayName: name}}); err != nil {
			t.Fatal(err)
		}
	}
	add("twitch", "1", "ann", "Ann", `{}`)
	add("youtube", "UCann", "ann", "@Ann", `{}`)
	add("youtube", "UCold", "old", "@Old", `{"backlog":true}`)
	if err := s.CatchUp(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByAccount(ctx, "youtube", "UCold"); !errors.Is(err, ErrNotFound) {
		t.Fatal("backlog made a viewer")
	}
	yt, err := s.ByAccount(ctx, "youtube", "UCann")
	if err != nil || yt.Messages != 1 || yt.Login != "ann" {
		t.Fatalf("%+v %v", yt, err)
	}
	if _, err := s.ByLogin(ctx, "@ann"); !errors.Is(err, ErrAmbiguous) {
		t.Fatal(err)
	}
	tw, _ := s.ByAccount(ctx, "twitch", "1")
	if _, err := db.ExecContext(ctx, `INSERT INTO viewer_links (id, twitch_viewer, youtube_viewer, linked_at, linked_by) VALUES ('l1', ?, ?, 'x', 'test')`,
		tw.ID, yt.ID); err != nil {
		t.Fatal(err)
	}
	if v, err := s.ByLogin(ctx, "ann"); err != nil || v.ID != tw.ID {
		t.Fatalf("%+v %v", v, err)
	}
}
