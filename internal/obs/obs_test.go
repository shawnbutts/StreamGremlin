package obs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/mockobs"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

type rig struct {
	t     *testing.T
	mock  *mockobs.Server
	url   string
	db    *storage.DB
	sec   *secrets.Store
	set   *settings.Store
	inbox *events.Inbox
}

func newRig(t *testing.T) *rig {
	t.Helper()
	m := mockobs.New()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	sec, err := secrets.Open(ctx, db, secrets.Options{DataDir: dir, Want: secrets.KindFile})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, mock: m, url: "ws" + strings.TrimPrefix(srv.URL, "http"), db: db, sec: sec,
		set: settings.NewStore(db), inbox: events.NewInbox(db, nil)}
}

func (r *rig) service() *Service {
	r.t.Helper()
	s := New(Options{DB: r.db, Settings: r.set, Secrets: r.sec, Inbox: r.inbox})
	s.backoffMin = 20 * time.Millisecond
	if err := s.Start(context.Background()); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(s.Close)
	return s
}

// connected starts a service turned on against the mock.
func (r *rig) connected(password ...string) *Service {
	r.t.Helper()
	s := r.service()
	var pw *string
	if len(password) > 0 {
		pw = &password[0]
	}
	if err := s.Configure(context.Background(), Config{URL: r.url, Enabled: true}, pw); err != nil {
		r.t.Fatal(err)
	}
	waitState(r.t, s, StateConnected)
	return s
}

func waitState(t *testing.T, s *Service, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.Status(); st.State == want {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state %s (%s), want %s", s.Status().State, s.Status().Detail, want)
	return Status{}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// eventsOf returns stored events of typ, with payloads.
func eventsOf(t *testing.T, in *events.Inbox, typ string) []map[string]any {
	t.Helper()
	recs, err := in.After(context.Background(), 0, 500, events.Filter{typ})
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, r := range recs {
		var p map[string]any
		_ = json.Unmarshal(r.Payload, &p)
		out = append(out, p)
	}
	return out
}

func TestAuthMatchesTheProtocolsWorkedSteps(t *testing.T) {
	// base64(sha256(base64(sha256(password+salt)) + challenge)), computed independently.
	if got := auth("supersecretpassword", "lM1GncleQOaCu9lT1yeUZhFYnqhsLLP1G5lAGo3ixaI=", "+IxH4CnCiqpX1rM9scsNynZzbOe4KhDeYcTNS3PDaeY="); got == "" || len(got) != 44 {
		t.Fatal(got)
	}
}

func TestOffUntilTurnedOn(t *testing.T) {
	r := newRig(t)
	s := r.service()
	if st := s.Status(); st.State != StateOff || st.URL != DefaultURL || st.Enabled {
		t.Fatalf("%+v", st)
	}
	if _, _, err := s.Scenes(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	for _, bad := range []string{"http://x:1", "127.0.0.1:4455", "ws://", "ws://user:pw@host:1", "ws://h:port"} {
		if err := s.Configure(context.Background(), Config{URL: bad, Enabled: true}, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestConnectsWithAPasswordAndSurvivesARestart(t *testing.T) {
	r := newRig(t)
	r.mock.SetPassword("hunter2")
	s := r.connected("hunter2")
	st := s.Status()
	if !st.HasPassword || st.OBSVersion != "31.0.0" || st.WebSocketVersion != "5.5.0" || st.Scene != "Starting Soon" {
		t.Fatalf("%+v", st)
	}
	statusJSON, _ := json.Marshal(st)
	if strings.Contains(string(statusJSON), "hunter2") {
		t.Fatal("the password is in the status")
	}
	s.Close()
	s2 := r.service() // the setting and the password are kept
	waitState(t, s2, StateConnected)
}

func TestWrongPasswordStopsUntilFixed(t *testing.T) {
	r := newRig(t)
	r.mock.SetPassword("right")
	s := r.service()
	wrong := "wrong"
	_ = s.Configure(context.Background(), Config{URL: r.url, Enabled: true}, &wrong)
	st := waitState(t, s, StateNeedsAction)
	if !strings.Contains(st.Detail, "refused the password") {
		t.Fatal(st)
	}
	time.Sleep(100 * time.Millisecond)
	if s.Status().State != StateNeedsAction {
		t.Fatal("it retried with a wrong password")
	}
	right := "right"
	_ = s.Configure(context.Background(), Config{URL: r.url, Enabled: true}, &right)
	waitState(t, s, StateConnected)
	// No password saved, but OBS wants one.
	empty := ""
	_ = s.Configure(context.Background(), Config{URL: r.url, Enabled: true}, &empty)
	if st := waitState(t, s, StateNeedsAction); !strings.Contains(st.Detail, "password") || st.HasPassword {
		t.Fatal(st)
	}
}

func TestKickedIsNotRetried(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	r.mock.Kick()
	st := waitState(t, s, StateNeedsAction)
	if !strings.Contains(st.Detail, "Kick") {
		t.Fatal(st)
	}
	time.Sleep(100 * time.Millisecond)
	if r.mock.Clients() != 0 {
		t.Fatal("reconnected after a kick")
	}
	_ = s.Configure(context.Background(), Config{URL: r.url, Enabled: true}, nil)
	waitState(t, s, StateConnected)
}

func TestReconnectsAfterADropOrOBSClosing(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	r.mock.Drop()
	waitState(t, s, StateDisconnected)
	waitState(t, s, StateConnected)
	r.mock.Exit()
	waitFor(t, "a disconnect", func() bool { return s.Status().State != StateConnected })
	waitState(t, s, StateConnected)
	// Turning it off disconnects.
	_ = s.Configure(context.Background(), Config{URL: r.url, Enabled: false}, nil)
	waitState(t, s, StateOff)
	waitFor(t, "the mock to see it go", func() bool { return r.mock.Clients() == 0 })
}

func TestScenesSourcesAndAttribution(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	ctx := context.Background()
	cur, scenes, err := s.Scenes(ctx)
	if err != nil || cur != "Starting Soon" || strings.Join(scenes, ",") != "Starting Soon,Just Chatting,Gaming,Be Right Back" {
		t.Fatalf("%s %v %v", cur, scenes, err)
	}
	items, err := s.SceneItems(ctx, "Gaming")
	if err != nil || len(items) != 3 || items[0].Source != "Game Capture" || !items[0].Visible {
		t.Fatalf("%+v %v", items, err)
	}
	prev, err := s.SwitchScene(ctx, "Gaming", "app")
	if err != nil || prev != "Starting Soon" || r.mock.Current() != "Gaming" {
		t.Fatalf("%s %v", prev, err)
	}
	waitFor(t, "ours", func() bool { return s.Status().SceneSetBy == "app" })
	r.mock.SwitchByHand("Just Chatting")
	waitFor(t, "theirs", func() bool { st := s.Status(); return st.Scene == "Just Chatting" && st.SceneSetBy == "" })
	// The status can change just before the event is stored: wait for both.
	got := eventsOf(t, r.inbox, "broadcast.scene.changed")
	waitFor(t, "both scene events", func() bool { got = eventsOf(t, r.inbox, "broadcast.scene.changed"); return len(got) >= 2 })
	if len(got) != 2 || got[0]["changed_by"] != "streamgremlin" || got[0]["requested_by"] != "app" ||
		got[1]["changed_by"] != "external" || got[1]["previous_scene_name"] != "Gaming" {
		t.Fatalf("%+v", got)
	}
	if _, err := s.SwitchScene(ctx, "Nope", "app"); !IsNotFound(err) {
		t.Fatalf("missing scene: %v", err)
	}
	was, err := s.SetSourceVisible(ctx, "Gaming", "Webcam", false)
	if err != nil || !was || r.mock.Visible("Gaming", "Webcam") {
		t.Fatalf("%v %v", was, err)
	}
	waitFor(t, "the visibility event", func() bool {
		v := eventsOf(t, r.inbox, "broadcast.scene_item.visibility_changed")
		return len(v) == 1 && v[0]["visible"] == false
	})
	if _, err := s.SetSourceVisible(ctx, "Gaming", "Nope", true); !IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestFiltersMuteTextMediaRecordingReplayScreenshot(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	ctx := context.Background()
	fs, err := s.Filters(ctx, "Webcam")
	if err != nil || len(fs) != 2 || fs[1].Name != "Blur" || fs[1].Enabled {
		t.Fatalf("%+v %v", fs, err)
	}
	if on, err := s.SetFilter(ctx, "Webcam", "Blur", ModeToggle); err != nil || !on || !r.mock.FilterEnabled("Webcam", "Blur") {
		t.Fatal(on, err)
	}
	if on, err := s.SetFilter(ctx, "Webcam", "Blur", ModeOff); err != nil || on {
		t.Fatal(on, err)
	}
	if _, err := s.SetFilter(ctx, "Webcam", "Blur", "sideways"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if muted, err := s.SetMuted(ctx, "Mic/Aux", ModeOn); err != nil || !muted {
		t.Fatal(muted, err)
	}
	if muted, err := s.SetMuted(ctx, "Mic/Aux", ModeToggle); err != nil || muted {
		t.Fatal(muted, err)
	}
	if err := s.SetText(ctx, "BRB Text", "Back in 5!"); err != nil {
		t.Fatal(err)
	}
	if in, _ := r.mock.InputState("BRB Text"); in.Settings["text"] != "Back in 5!" {
		t.Fatal(in)
	}
	if err := s.SetText(ctx, "Webcam", "oops"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("text into a camera: %v", err)
	}
	if err := s.Media(ctx, "Music", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := s.Media(ctx, "Music", "dance"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // safe to repeat
		if _, _, err := s.Recording(ctx, true); err != nil || !r.mock.Recording() {
			t.Fatal(err)
		}
	}
	changed, path, err := s.Recording(ctx, false)
	if err != nil || !changed || !strings.HasSuffix(path, ".mkv") {
		t.Fatal(changed, path, err)
	}
	if err := s.SaveReplay(ctx); err == nil || !strings.Contains(err.Error(), "replay buffer isn't running") {
		t.Fatal(err)
	}
	r.mock.SetReplayBuffer(true)
	if err := s.SaveReplay(ctx); err != nil {
		t.Fatal(err)
	}
	shot, err := s.Screenshot(ctx, "Webcam", `C:\Shots`)
	if err != nil || !strings.HasPrefix(shot, `C:\Shots\Webcam-`) || r.mock.LastScreenshot() != shot {
		t.Fatal(shot, err)
	}
	if _, err := s.Screenshot(ctx, "Webcam", "relative/folder"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ins, err := s.Inputs(ctx)
	if err != nil || len(ins) < 4 {
		t.Fatal(ins, err)
	}
	for typ, want := range map[string]int{
		"broadcast.filter.enabled_changed": 2, "broadcast.audio.mute_changed": 2, "broadcast.media.started": 1,
		"broadcast.recording.started": 1, "broadcast.recording.stopped": 1, "broadcast.replay.saved": 1,
	} {
		waitFor(t, typ, func() bool { return len(eventsOf(t, r.inbox, typ)) == want })
	}
}

func TestStreamEventsAndAChangeSeenOnReconnect(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	r.mock.StartStream()
	waitFor(t, "stream started", func() bool { return len(eventsOf(t, r.inbox, "broadcast.stream.started")) == 1 })
	// The scene changes while StreamGremlin can't see OBS.
	_ = s.Configure(context.Background(), Config{URL: r.url, Enabled: false}, nil)
	waitState(t, s, StateOff)
	r.mock.SwitchByHand("Gaming")
	_ = s.Configure(context.Background(), Config{URL: r.url, Enabled: true}, nil)
	waitState(t, s, StateConnected)
	waitFor(t, "the change seen on reconnect", func() bool {
		for _, e := range eventsOf(t, r.inbox, "broadcast.scene.changed") {
			if e["observed_on_reconnect"] == true && e["scene_name"] == "Gaming" {
				return true
			}
		}
		return false
	})
}

func TestRequestFailuresAreReported(t *testing.T) {
	r := newRig(t)
	s := r.connected()
	r.mock.FailNext("SetCurrentProgramScene", 207)
	_, err := s.SwitchScene(context.Background(), "Gaming", "app")
	var re *RequestError
	if !errors.As(err, &re) || re.Code != 207 {
		t.Fatal(err)
	}
	if r.mock.Current() != "Starting Soon" {
		t.Fatal("switched anyway")
	}
}
