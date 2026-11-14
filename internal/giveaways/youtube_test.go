package giveaways

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

type fakeYouTube struct {
	mu   sync.Mutex
	said []string
	live bool
}

func (f *fakeYouTube) SendChat(_ context.Context, req youtube.SendRequest) (youtube.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.said = append(f.said, req.Text)
	return youtube.SendResult{MessageIDs: []string{"y"}}, nil
}

func (f *fakeYouTube) ChatStatus() youtube.ChatStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.live {
		return youtube.ChatStatus{}
	}
	return youtube.ChatStatus{Broadcast: &youtube.Broadcast{ID: "b", LiveChatID: "c"}}
}

func (f *fakeYouTube) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.said, "\n")
}

// ytSay stores a YouTube chat command, as the YouTube adapter does.
func (r *rig) ytSay(id, handle, command string, extra map[string]any) {
	r.t.Helper()
	c := map[string]any{"id": id, "login": strings.ToLower(strings.TrimPrefix(handle, "@")), "display_name": handle}
	payload := map[string]any{"chatter": c, "text": "!" + command, "command": command}
	for k, v := range extra {
		payload[k] = v
	}
	p, _ := json.Marshal(payload)
	if _, err := r.inbox.Accept(context.Background(), events.Envelope{Type: "chat.command.received", Source: events.SourceYouTube,
		Actor: &events.Actor{Platform: events.SourceYouTube, ID: id}, DeliveryQuality: events.Confirmed, Payload: p, ReceivedAt: r.now()}); err != nil {
		r.t.Fatal(err)
	}
}

// YouTube viewers enter and hear in YouTube chat; announcements go to both
// chats while YouTube's is read; one person linked on both platforms enters
// once; Twitch-only rules turn YouTube viewers away, in YouTube chat (D-101).
func TestYouTubeEntries(t *testing.T) {
	r := newRig(t)
	yt := &fakeYouTube{live: true}
	r.svc.Close()
	r.svc = New(Options{DB: r.db, Inbox: r.inbox, Twitch: r.chat, Now: r.now, AckEvery: r.svc.ackEvery, Display: r.shown, YouTube: yt,
		Partner: func(_ context.Context, platform, id string) (events.Actor, bool, error) {
			if platform == events.SourceYouTube && id == "UCann" {
				return events.Actor{Platform: "twitch", ID: "id-ann"}, true, nil
			}
			if platform == "twitch" && id == "id-ann" {
				return events.Actor{Platform: events.SourceYouTube, ID: "UCann"}, true, nil
			}
			return events.Actor{}, false, nil
		}})
	if err := r.svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.svc.Close)
	ctx := context.Background()
	g, err := r.svc.Open(ctx, Settings{Title: "a key"}, "the app")
	if err != nil {
		t.Fatal(err)
	}
	r.eventually("the opening on both", func() bool {
		return strings.Contains(r.chat.all(), "Type !join") && strings.Contains(yt.all(), "Type !join")
	})
	r.ytSay("UCcat", "@Cat.Tube", "join", nil)
	r.say("ann", "join", false)
	r.ytSay("UCann", "@Ann", "join", nil) // Ann again, linked: not a second entry
	r.ytSay("UCold", "@Old", "join", map[string]any{"backlog": true})
	r.entries(g.ID, 2)
	r.eventually("YouTube's acknowledgement", func() bool { return strings.Contains(yt.all(), "@cat.tube: you're in a key!") })
	if strings.Contains(r.chat.all(), "@cat.tube") || strings.Contains(yt.all(), "@ann:") {
		t.Fatalf("an entrant heard in the wrong chat:\n%s\n--\n%s", r.chat.all(), yt.all())
	}
	d := r.detail(g.ID)
	for _, m := range d.Messages {
		if m.Platform != "twitch" && m.Platform != "youtube" {
			t.Fatalf("%+v", m)
		}
	}
	if _, err := r.svc.Cancel(ctx, g.ID); err != nil {
		t.Fatal(err)
	}

	// A followers-only giveaway can't be checked for YouTube viewers.
	g, err = r.svc.Open(ctx, Settings{Title: "a follower prize", Rules: Rules{Followers: true}}, "the app")
	if err != nil {
		t.Fatal(err)
	}
	r.ytSay("UCcat", "@Cat.Tube", "join", nil)
	r.eventually("the refusal on YouTube", func() bool {
		return strings.Contains(yt.all(), "@cat.tube: a follower prize's rules can only be checked for Twitch chat")
	})
	if len(r.detail(g.ID).Entries) != 0 {
		t.Fatal("entered")
	}
}
