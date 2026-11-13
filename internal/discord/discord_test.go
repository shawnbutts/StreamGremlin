package discord

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockdiscord"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

type rig struct {
	mock *mockdiscord.Server
	sec  *secrets.Store
	s    *Service
	logs *bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	m := mockdiscord.New()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(dir, "d.db"))
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
	logs := &bytes.Buffer{}
	s := New(Options{Settings: settings.NewStore(db), Secrets: sec, Base: srv.URL, Logger: slog.New(slog.NewTextHandler(logs, nil))})
	return &rig{mock: m, sec: sec, s: s, logs: logs}
}

func (r *rig) add(t *testing.T, name string) (id, token string) {
	t.Helper()
	id, token = r.mock.AddWebhook("Captain Hook")
	if _, err := r.s.Add(context.Background(), name, "https://discord.com/api/webhooks/"+id+"/"+token); err != nil {
		t.Fatal(err)
	}
	return id, token
}

func TestParseLink(t *testing.T) {
	good := map[string]string{
		"https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz_-0123":      "123456789012345678",
		" https://discordapp.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz/ ":      "123456789012345678",
		"https://canary.discord.com/api/v10/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz": "123456789012345678",
	}
	for link, want := range good {
		if id, tok, err := ParseLink(link); err != nil || id != want || tok == "" {
			t.Errorf("%q: %v %v", link, id, err)
		}
	}
	for _, bad := range []string{"", "http://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz",
		"https://evil.example/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz",
		"https://discord.com.evil.example/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz",
		"https://discord.com/api/webhooks/123456789012345678", "https://discord.com/channels/1/2",
		"https://user:pw@discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz"} {
		if _, _, err := ParseLink(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// Adding checks the link with Discord, keeps the token only in the secret
// store, and never logs it; removing forgets both.
func TestAddListRemove(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	id, token := r.add(t, "announcements")
	ws, err := r.s.Webhooks(ctx)
	if err != nil || len(ws) != 1 || ws[0].Name != "announcements" || ws[0].ID != id || ws[0].DiscordName != "Captain Hook" || ws[0].ChannelID == "" {
		t.Fatal(ws, err)
	}
	if v, err := r.sec.Get(ctx, "discord/webhook/announcements"); err != nil || v.Reveal() != id+"/"+token {
		t.Fatal("secret:", err)
	}
	if strings.Contains(r.logs.String(), token) {
		t.Fatal("the token was logged")
	}
	// The same name again replaces it.
	r.add(t, "announcements")
	if ws, _ := r.s.Webhooks(ctx); len(ws) != 1 || ws[0].ID == id {
		t.Fatal("replaced:", ws)
	}
	for name, link := range map[string]string{
		"Bad Name":  "https://discord.com/api/webhooks/" + id + "/" + token,
		"unknown":   "https://discord.com/api/webhooks/999999999999999999/" + token,
		"wrongtok":  "https://discord.com/api/webhooks/" + id + "/" + strings.Repeat("x", 30),
		"not-there": "https://example.com/x",
	} {
		if _, err := r.s.Add(ctx, name, link); err == nil {
			t.Errorf("%s: added", name)
		}
	}
	if _, err := r.s.Add(ctx, "gone", "https://discord.com/api/webhooks/999999999999999999/"+token); !errors.Is(err, ErrWebhookGone) {
		t.Fatal(err)
	}
	if err := r.s.Remove(ctx, "announcements"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Remove(ctx, "announcements"); !errors.Is(err, ErrNoWebhook) {
		t.Fatal(err)
	}
	if _, err := r.sec.Get(ctx, "discord/webhook/announcements"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("secret kept:", err)
	}
}

func TestSend(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	id, _ := r.add(t, "live")

	// A viewer's text can't ping anyone: parse is always sent, empty.
	sent, err := r.s.Send(ctx, "live", Message{Content: "hi @everyone from <@123>"})
	if err != nil || sent.MessageID == "" {
		t.Fatal(sent, err)
	}
	p := r.mock.Posts()[0]
	if p.WebhookID != id || p.Parse == nil || len(p.Parse) != 0 || p.Content != "hi @everyone from <@123>" {
		t.Fatalf("%+v", p)
	}

	// @everyone only when asked, first; a card with colour and pictures.
	_, err = r.s.Send(ctx, "live", Message{Content: "We're live!", Mention: MentionEveryone, Username: "Gremlin",
		Embed: Embed{Title: "Building a bot", Description: "Software and Game Development", URL: "https://twitch.tv/streamer",
			Color: "#9146FF", Image: "https://static-cdn.jtvnw.net/previews-ttv/live_user_streamer-1280x720.jpg", Footer: "Twitch"}})
	if err != nil {
		t.Fatal(err)
	}
	p = r.mock.Posts()[1]
	e := p.Embeds[0]
	if p.Content != "@everyone We're live!" || len(p.Parse) != 1 || p.Parse[0] != "everyone" || p.Username != "Gremlin" ||
		e["title"] != "Building a bot" || e["color"] != float64(0x9146FF) || e["url"] != "https://twitch.tv/streamer" ||
		e["image"].(map[string]any)["url"] == "" || e["footer"].(map[string]any)["text"] != "Twitch" {
		t.Fatalf("%+v", p)
	}
	if _, err := r.s.Send(ctx, "live", Message{Mention: MentionHere}); err != nil || r.mock.Posts()[2].Content != "@here" {
		t.Fatal("here alone:", err)
	}

	// Long text is cut to fit, not refused.
	sent, err = r.s.Send(ctx, "live", Message{Content: strings.Repeat("a", 2500), Embed: Embed{Title: strings.Repeat("t", 300), Description: strings.Repeat("d", 5000)}})
	if err != nil || !sent.Truncated {
		t.Fatal(sent, err)
	}
	p = r.mock.Posts()[3]
	if n := len([]rune(p.Content)); n != 2000 || !strings.HasSuffix(p.Content, "…") {
		t.Fatal(n)
	}

	for _, bad := range []Message{{}, {Embed: Embed{URL: "https://x.example"}}, {Content: "x", Embed: Embed{Title: "t", Color: "purple"}},
		{Content: "x", Embed: Embed{Title: "t", Image: "javascript:alert(1)"}}, {Content: "x", Mention: "role"}} {
		if _, err := r.s.Send(ctx, "live", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if _, err := r.s.Send(ctx, "nope", Message{Content: "x"}); !errors.Is(err, ErrNoWebhook) {
		t.Fatal(err)
	}

	// Rate limited twice: waited for and posted once.
	before := len(r.mock.Posts())
	r.mock.SetRateLimited(2)
	if _, err := r.s.Send(ctx, "live", Message{Content: "after a wait"}); err != nil || len(r.mock.Posts()) != before+1 {
		t.Fatal("limited:", err)
	}
	r.mock.SetRateLimited(3)
	if _, err := r.s.Send(ctx, "live", Message{Content: "x"}); !errors.Is(err, ErrRefused) {
		t.Fatal("limited 3 times:", err)
	}
	r.mock.SetRateLimited(0)

	// Discord down: unknown, sent once.
	r.mock.SetDown(true)
	if _, err := r.s.Send(ctx, "live", Message{Content: "x"}); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatal("down:", err)
	}
	r.mock.SetDown(false)

	// Deleted in Discord.
	r.mock.DeleteWebhook(id)
	if _, err := r.s.Send(ctx, "live", Message{Content: "x"}); !errors.Is(err, ErrWebhookGone) {
		t.Fatal("gone:", err)
	}
}

// A long wait asked by Discord is refused rather than waited for.
func TestSendLongRateLimit(t *testing.T) {
	r := newRig(t)
	r.add(t, "live")
	r.s.maxWait = time.Millisecond
	r.mock.SetRateLimited(1)
	if _, err := r.s.Send(context.Background(), "live", Message{Content: "x"}); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "wait") {
		t.Fatal(err)
	}
}

func TestManifestIsComplete(t *testing.T) {
	m := DiscordManifest()
	if m.VerifiedAt == "" || len(m.Docs) == 0 || len(m.Capabilities) == 0 {
		t.Fatal(m)
	}
	for _, c := range m.Capabilities {
		if c.Name == "" || c.Notes == "" || (c.Support != "supported" && c.Support != "partial" && c.Support != "unsupported" && c.Support != "unknown") {
			t.Errorf("%+v", c)
		}
	}
}
