package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

func newInbox(t *testing.T) (*Inbox, *storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return NewInbox(db, nil), db, dir
}

func chatEvent(sourceID, text string) Envelope {
	return Envelope{
		Type:            "chat.message.received",
		Source:          "twitch",
		SourceEventID:   sourceID,
		OccurredAt:      time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC),
		Actor:           &Actor{Platform: "twitch", ID: "42", Login: "viewer"},
		Payload:         json.RawMessage(fmt.Sprintf(`{"text": %q}`, text)),
		DeliveryQuality: Confirmed,
	}
}

func TestValidType(t *testing.T) {
	for _, ok := range []string{"chat.message.received", "audience.raid.received", "ads.break.end_estimated", "a.b.c.d"} {
		if !ValidType(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "chat.message", "Chat.message.received", "twitch.chat.received", "obs.scene.changed",
		"provider.twitch.x", "chat..received", "chat.message.received.", "1chat.a.b", strings.Repeat("a.", 70) + "a"} {
		if ValidType(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(e *Envelope){
		"array payload":  func(e *Envelope) { e.Payload = json.RawMessage(`[1]`) },
		"null payload":   func(e *Envelope) { e.Payload = json.RawMessage(`null`) },
		"broken payload": func(e *Envelope) { e.Payload = json.RawMessage(`{"a":`) },
		"huge payload":   func(e *Envelope) { e.Payload = json.RawMessage(`{"a":"` + strings.Repeat("x", MaxPayloadBytes) + `"}`) },
		"bad source":     func(e *Envelope) { e.Source = "Twitch" },
		"bad quality":    func(e *Envelope) { e.DeliveryQuality = "probably" },
		"spaced id":      func(e *Envelope) { e.SourceEventID = "a b" },
		"actor w/o id":   func(e *Envelope) { e.Actor = &Actor{Platform: "twitch"} },
		"bad type":       func(e *Envelope) { e.Type = "twitch.chat.message" },
	}
	for name, mutate := range cases {
		in, _, _ := newInbox(t)
		e := chatEvent("m1", "hi")
		mutate(&e)
		if _, err := in.Accept(context.Background(), e); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAcceptStoresOnceInArrivalOrder(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	first, err := in.Accept(ctx, chatEvent("m1", "first"))
	if err != nil || first.Duplicate || first.Record.Seq != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	// Twitch redelivers the same message: new event_id, same source id.
	again, err := in.Accept(ctx, chatEvent("m1", "first"))
	if err != nil || !again.Duplicate || again.Record.EventID != first.Record.EventID {
		t.Fatalf("redelivery: %+v %v", again, err)
	}
	// Retrying with the same event_id is a duplicate too.
	e := chatEvent("", "no source id")
	e.EventID = NewID()
	if _, err := in.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	if r, err := in.Accept(ctx, e); err != nil || !r.Duplicate {
		t.Fatalf("same event_id: %+v %v", r, err)
	}
	// Out of order: an older event arriving later is stored after, by arrival.
	old := chatEvent("m0", "older")
	old.OccurredAt = old.OccurredAt.Add(-time.Minute)
	r, err := in.Accept(ctx, old)
	if err != nil || r.Record.Seq != 3 {
		t.Fatalf("out of order: %+v %v", r, err)
	}
	if head, _ := in.Head(ctx); head != 3 {
		t.Fatalf("head %d", head)
	}
}

func TestConflictingDuplicateIsRefused(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	if _, err := in.Accept(ctx, chatEvent("m1", "original")); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Accept(ctx, chatEvent("m1", "changed")); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v", err)
	}
	recs, _ := in.Latest(ctx, 10, nil)
	if len(recs) != 1 || !strings.Contains(string(recs[0].Payload), "original") {
		t.Fatalf("the first event must win: %+v", recs)
	}
}

func TestPreparedFieldsAndRoundTrip(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	e := chatEvent("m1", "hi")
	e.Payload = json.RawMessage("{ \"text\" :  \"hi\" }")
	r, err := in.Accept(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := in.Get(ctx, r.Record.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.CorrelationID != got.EventID || got.ReceivedAt.IsZero() ||
		string(got.Payload) != `{"text":"hi"}` || got.Actor.Login != "viewer" || !got.OccurredAt.Equal(e.OccurredAt) {
		t.Fatalf("%+v", got)
	}
	if _, err := in.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestEventsAreAppendOnly(t *testing.T) {
	in, db, _ := newInbox(t)
	if _, err := in.Accept(context.Background(), chatEvent("m1", "hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE events SET payload = '{}'"); err == nil {
		t.Fatal("an event was changed")
	}
}

func TestReadingByCursorAndFilter(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := in.Accept(ctx, chatEvent(fmt.Sprint("m", i), "x")); err != nil {
			t.Fatal(err)
		}
	}
	scene := Envelope{Type: "broadcast.scene.changed", Source: "obs", Payload: json.RawMessage(`{"scene_name":"A"}`), DeliveryQuality: Confirmed}
	if _, err := in.Accept(ctx, scene); err != nil {
		t.Fatal(err)
	}
	after, _ := in.After(ctx, 2, 2, nil)
	if len(after) != 2 || after[0].Seq != 3 || after[1].Seq != 4 {
		t.Fatalf("after: %+v", after)
	}
	latest, _ := in.Latest(ctx, 3, nil)
	if len(latest) != 3 || latest[0].Seq != 4 || latest[2].Seq != 6 {
		t.Fatalf("latest: %+v", latest)
	}
	for f, want := range map[string]int{"broadcast.*": 1, "chat.*": 5, "chat.message.received": 5, "chat.command.received": 0, "broadcast.scene.*": 1} {
		got, _ := in.After(ctx, 0, 100, Filter{f})
		if len(got) != want {
			t.Errorf("%s: %d, want %d", f, len(got), want)
		}
	}
	// "_" in a pattern is a literal, not a LIKE wildcard.
	if got, _ := in.After(ctx, 0, 100, Filter{"chat_message.*"}); len(got) != 0 {
		t.Errorf("underscore matched as a wildcard: %d", len(got))
	}
	for _, p := range []string{"chat.*", "broadcast.scene.*", "chat.message.received"} {
		if !ValidPattern(p) {
			t.Errorf("%q should be a valid pattern", p)
		}
	}
	for _, p := range []string{"*", ".*", "chat.**", "chat*", "Chat.*", "twitch.chat.received"} {
		if ValidPattern(p) {
			t.Errorf("%q should be invalid", p)
		}
	}
}

func TestWaitWakesOnNewEventsAndTimesOut(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	start := time.Now()
	recs, err := in.Wait(ctx, 0, 10, nil, 50*time.Millisecond)
	if err != nil || len(recs) != 0 || time.Since(start) < 40*time.Millisecond {
		t.Fatalf("timeout: %v %v %v", recs, err, time.Since(start))
	}
	got := make(chan []Record, 1)
	go func() {
		r, _ := in.Wait(ctx, 0, 10, Filter{"chat.*"}, 5*time.Second)
		got <- r
	}()
	time.Sleep(30 * time.Millisecond)
	scene := Envelope{Type: "broadcast.scene.changed", Source: "obs", Payload: json.RawMessage(`{}`), DeliveryQuality: Confirmed}
	if _, err := in.Accept(ctx, scene); err != nil { // filtered out: keeps waiting
		t.Fatal(err)
	}
	if _, err := in.Accept(ctx, chatEvent("m1", "x")); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		if len(r) != 1 || r[0].Type != "chat.message.received" {
			t.Fatalf("woke with %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait didn't wake")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := in.Wait(cctx, 100, 10, nil, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestConcurrentAcceptsKeepEveryEventOnce(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each message is delivered twice at once.
			if _, err := in.Accept(ctx, chatEvent(fmt.Sprint("m", i%20), "x")); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	recs, _ := in.After(ctx, 0, 100, nil)
	if len(recs) != 20 {
		t.Fatalf("%d events, want 20", len(recs))
	}
	for i, r := range recs {
		if r.Seq != int64(i+1) {
			t.Fatalf("gap at %d: %d", i, r.Seq)
		}
	}
}

func TestSimulate(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	res, err := in.Simulate(ctx, Simulation{Type: "chat.command.received", RequestID: "r1",
		Fields: map[string]string{"user": "Gremlin_Fan", "command": "!Hello", "args": "there friend"}})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Record
	var p struct {
		Command, Args, Text string
		ArgList             []string `json:"arg_list"`
	}
	_ = json.Unmarshal(r.Payload, &p)
	if r.DeliveryQuality != Synthetic || r.Source != "twitch" || r.Actor.Login != "gremlin_fan" ||
		p.Command != "hello" || p.Text != "!Hello there friend" || len(p.ArgList) != 2 {
		t.Fatalf("%+v %+v", r, p)
	}
	// A retried request gives the same event, even with different fields.
	again, err := in.Simulate(ctx, Simulation{Type: "chat.command.received", RequestID: "r1"})
	if err != nil || !again.Duplicate || again.Record.Seq != r.Seq {
		t.Fatalf("retry: %+v %v", again, err)
	}
	// Defaults fill missing fields.
	res, err = in.Simulate(ctx, Simulation{Type: "chat.message.received", RequestID: "r2"})
	if err != nil || !strings.Contains(string(res.Record.Payload), "Hello from a test event!") {
		t.Fatalf("defaults: %s %v", res.Record.Payload, err)
	}
	for name, s := range map[string]Simulation{
		"unknown type":  {Type: "chat.nothing.happened", RequestID: "x1"},
		"unknown field": {Type: "chat.message.received", RequestID: "x2", Fields: map[string]string{"bits": "5"}},
		"too long":      {Type: "chat.message.received", RequestID: "x3", Fields: map[string]string{"text": strings.Repeat("a", 501)}},
		"no request id": {Type: "chat.message.received"},
		"bad command":   {Type: "chat.command.received", RequestID: "x4", Fields: map[string]string{"command": "hello"}},
		"no scene":      {Type: "broadcast.scene.changed", RequestID: "x5", Fields: map[string]string{"scene_name": " "}},
	} {
		if _, err := in.Simulate(ctx, s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestConcurrentRetriedSimulations(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := in.Simulate(ctx, Simulation{Type: "chat.message.received", RequestID: "same"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if head, _ := in.Head(ctx); head != 1 {
		t.Fatalf("%d events from one request", head)
	}
}

func TestCatalogIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Catalog() {
		if !ValidType(k.Type) || seen[k.Type] || k.Label == "" || k.Description == "" || len(k.Sources) == 0 || k.build == nil {
			t.Errorf("bad catalog entry %+v", k)
		}
		seen[k.Type] = true
		for _, f := range k.Fields {
			if f.Max <= 0 || (f.Kind != "text" && f.Kind != "number" && f.Kind != "choice") || len([]rune(f.Default)) > f.Max ||
				(f.Kind == "choice") != (len(f.Choices) > 0) || (f.Kind == "choice" && !slices.Contains(f.Choices, f.Default)) {
				t.Errorf("%s: bad field %+v", k.Type, f)
			}
			if f.Path == "" {
				continue
			}
			// A field with a path sets that path: each choice lands there.
			for _, c := range append([]string{f.Default}, f.Choices...) {
				env, err := (Simulation{Type: k.Type, RequestID: "p", Fields: map[string]string{f.Name: c}}).Build()
				if err != nil {
					t.Fatalf("%s %s=%s: %v", k.Type, f.Name, c, err)
				}
				var p map[string]any
				_ = json.Unmarshal(env.Payload, &p)
				var got any = p
				for _, part := range strings.Split(f.Path, ".") {
					got = got.(map[string]any)[part]
				}
				if got != c {
					t.Errorf("%s: %s=%s set %s to %v", k.Type, f.Name, c, f.Path, got)
				}
			}
		}
		// Every entry can make a test event from its defaults.
		if _, err := (Simulation{Type: k.Type, RequestID: "check"}).Build(); err != nil {
			t.Errorf("%s: %v", k.Type, err)
		}
	}
}

func TestNewIDIsUUIDv4(t *testing.T) {
	id := NewID()
	if len(id) != 36 || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) || NewID() == id {
		t.Fatal(id)
	}
}

// Search finds events by type, viewer or contents, newest first in the
// limit, ignoring case; % and _ are only themselves.
func TestSearch(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	for i, text := range []string{"Hello there", "100% hype", "good_game", "nothing", "HELLO again"} {
		if _, err := in.Accept(ctx, chatEvent(fmt.Sprint("s", i), text)); err != nil {
			t.Fatal(err)
		}
	}
	scene := Envelope{Type: "broadcast.scene.changed", Source: "obs", Payload: json.RawMessage(`{"scene_name":"Just Chatting"}`), DeliveryQuality: Confirmed}
	if _, err := in.Accept(ctx, scene); err != nil {
		t.Fatal(err)
	}
	texts := func(recs []Record) []string {
		var out []string
		for _, r := range recs {
			var p struct {
				Text      string `json:"text"`
				SceneName string `json:"scene_name"`
			}
			_ = json.Unmarshal(r.Payload, &p)
			out = append(out, p.Text+p.SceneName)
		}
		return out
	}
	for q, want := range map[string][]string{
		"hello":         {"Hello there", "HELLO again"},
		"100%":          {"100% hype"},
		"%":             {"100% hype"},
		"d_g":           {"good_game"},
		"_":             {"good_game"},
		"VIEWER":        {"Hello there", "100% hype", "good_game", "nothing", "HELLO again"}, // the viewer's login
		"scene.changed": {"Just Chatting"},
		"just chat":     {"Just Chatting"},
		"zebra":         nil,
	} {
		got, err := in.Search(ctx, q, 50, nil)
		if err != nil || !slices.Equal(texts(got), want) {
			t.Errorf("%q: %v %v", q, texts(got), err)
		}
	}
	// The limit keeps the newest; a filter narrows by type.
	if got, _ := in.Search(ctx, "hello", 1, nil); !slices.Equal(texts(got), []string{"HELLO again"}) {
		t.Error(texts(got))
	}
	if got, _ := in.Search(ctx, "chat", 50, Filter{"broadcast.*"}); !slices.Equal(texts(got), []string{"Just Chatting"}) {
		t.Error(texts(got))
	}
}

// One account's chat, for the Live screen's viewer card: newest kept,
// oldest first, others' messages and test messages apart.
func TestChatFrom(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	for i := range 5 {
		if _, err := in.Accept(ctx, chatEvent(fmt.Sprintf("m%d", i), fmt.Sprintf("hello %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	other := chatEvent("o1", "not me")
	other.Actor = &Actor{Platform: "twitch", ID: "43", Login: "other"}
	if _, err := in.Accept(ctx, other); err != nil {
		t.Fatal(err)
	}
	test := chatEvent("t1", "a test")
	test.DeliveryQuality = "synthetic"
	if _, err := in.Accept(ctx, test); err != nil {
		t.Fatal(err)
	}
	recs, err := in.ChatFrom(ctx, "twitch", "42", 3, false)
	if err != nil || len(recs) != 3 || recs[0].SourceEventID != "m2" || recs[2].SourceEventID != "m4" {
		t.Fatalf("%+v %v", recs, err)
	}
	if recs, _ := in.ChatFrom(ctx, "twitch", "42", 10, true); len(recs) != 1 || recs[0].SourceEventID != "t1" {
		t.Fatalf("test: %+v", recs)
	}
	if recs, _ := in.ChatFrom(ctx, "twitch", "99", 10, false); len(recs) != 0 {
		t.Fatalf("nobody: %+v", recs)
	}
}

// YouTube chat is log-only for now (D-097): stored and readable, but not
// counted as viewer chat for timers.
func TestYouTubeChatIsLogOnly(t *testing.T) {
	in, _, _ := newInbox(t)
	ctx := context.Background()
	tw := chatEvent("tw1", "hi")
	yt := chatEvent("yt1", "hi")
	yt.Source = SourceYouTube
	yt.Actor = &Actor{Platform: "youtube", ID: "UCv"}
	for _, e := range []Envelope{tw, yt} {
		if _, err := in.Accept(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := in.CountViewerChat(ctx, 0); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	recs, _ := in.After(ctx, 0, 10, nil)
	if len(recs) != 2 || LogOnly(recs[0]) || !LogOnly(recs[1]) {
		t.Fatalf("%+v", recs)
	}
}
