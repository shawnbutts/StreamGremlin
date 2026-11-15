package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/buttons"
	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/earning"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/giveaways"
	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
	"code.streamgremlin.com/streamgremlin/internal/timers"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/wheels"
)

// Everyone in the pictures is made up. The names are chosen to read as
// ordinary chat names; none is meant to be a real account.
const (
	streamer = "CozyKestrel"
	bot      = "KestrelHelper"
	channel  = "cozykestrel"
)

var viewers = []string{
	"MossyToad", "byte_badger", "LunarLynx", "quiet_quokka", "TofuTamer", "rusty_robin",
	"NovaNebula", "GlitchGecko", "PaperCraneQ", "sleepy_sloth42", "WaffleWizard", "pixel_pangolin",
}

// chatLines is a few minutes of an ordinary stream's chat.
var chatLines = []struct{ who, text string }{
	{"MossyToad", "hiii kestrel, just got off work"},
	{"byte_badger", "!hello"},
	{"LunarLynx", "the new overlay looks so clean"},
	{"quiet_quokka", "!lurk"},
	{"TofuTamer", "what are we playing after this?"},
	{"pixel_pangolin", "!enter"},
	{"rusty_robin", "!quote"},
	{"MossyToad", "!enter"},
	{"NovaNebula", "that jump was ridiculous"},
	{"GlitchGecko", "clip it clip it"},
	{"LunarLynx", "!enter"},
	{"PaperCraneQ", "first time here, the art style is lovely"},
	{"WaffleWizard", "GG"},
	{"NovaNebula", "!enter"},
	{"byte_badger", "!enter"},
	{"rusty_robin", "hydrate check 💧"},
	{"TofuTamer", "!enter"},
	{"MossyToad", "!points"},
	{"GlitchGecko", "!enter"},
	{"LunarLynx", "boss fight time"},
	{"WaffleWizard", "!enter"},
	{"NovaNebula", "you've got this"},
}

func seed(ctx context.Context, c *client.Client, tw *mocktwitch.Server) error {
	do := func(method, path string, body, out any) error {
		if err := c.Do(ctx, method, path, body, out); err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		return nil
	}
	if err := do("PUT", "/v1/settings/ui", map[string]any{"theme": "dark"}, nil); err != nil {
		return err
	}
	if err := do("PUT", "/v1/connections/twitch/client-id", map[string]string{"client_id": "abcdefghij0123456789"}, nil); err != nil {
		return err
	}
	for role, login := range map[twitch.Role]string{twitch.RoleStreamer: streamer, twitch.RoleBot: bot} {
		if err := signIn(ctx, c, tw, role, login); err != nil {
			return err
		}
	}
	tw.SetModerator(channel, strings.ToLower(bot), true)
	tw.SetModerator(channel, "mossytoad", true)
	tw.SetLive(channel, true)
	tw.SetViewers(channel, 87)
	for tries := 0; tw.Subscribed(channel) == 0; tries++ {
		if tries > 100 {
			return errors.New("chat never connected")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Points: Acorns, earned by chatting.
	if err := do("PUT", "/v1/points/currency", map[string]string{"name": "Acorns"}, nil); err != nil {
		return err
	}
	rules := earning.Defaults()
	rules.On, rules.Chat.Amount, rules.Chat.LiveOnly = true, 10, false
	if err := do("PUT", "/v1/points/earning", rules, nil); err != nil {
		return err
	}

	// Workflows: the gallery's templates that need nothing filled in.
	var gallery struct {
		Templates []struct {
			ID       string          `json:"id"`
			Category string          `json:"category"`
			Blanks   []any           `json:"blanks"`
			Workflow json.RawMessage `json:"workflow"`
		} `json:"templates"`
	}
	if err := do("GET", "/v1/templates", nil, &gallery); err != nil {
		return err
	}
	saved := 0
	for _, t := range gallery.Templates {
		if len(t.Blanks) > 0 || strings.Contains(t.Category, "YouTube") || strings.Contains(t.Category, "Discord") || saved >= 18 {
			continue
		}
		var probe struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(t.Workflow, &probe) != nil || probe.ID == "" {
			continue
		}
		if err := c.DoRaw(ctx, "PUT", "/v1/workflows/"+url.PathEscape(probe.ID), t.Workflow, nil); err == nil {
			saved++
		}
	}

	// Buttons for the Live screen.
	for i, b := range []buttons.Button{
		{ID: "brb", Label: "Be right back", Color: "blue", Icon: "tabler:coffee"},
		{ID: "hydrate", Label: "Hydrate", Color: "teal", Icon: "tabler:droplet"},
		{ID: "clip", Label: "Clip that", Color: "green", Icon: "tabler:scissors"},
		{ID: "hype", Label: "Hype!", Color: "brass", Icon: "tabler:confetti"},
		{ID: "scene-chat", Label: "Just chatting", Icon: "tabler:message-circle"},
		{ID: "scene-game", Label: "Game scene", Icon: "tabler:device-gamepad-2"},
		{ID: "ending", Label: "Ending soon", Color: "red", Icon: "tabler:hourglass"},
	} {
		b.Position = i
		if err := do("PUT", "/v1/buttons/"+b.ID, b, nil); err != nil {
			// An unknown colour name shouldn't stop the pictures.
			b.Color = ""
			if err := do("PUT", "/v1/buttons/"+b.ID, b, nil); err != nil {
				return err
			}
		}
	}

	// Timers.
	for _, t := range []timers.Timer{
		{ID: "socials", Name: "Socials reminder", Enabled: true, EverySeconds: 900, ChatLines: 5, LiveOnly: true},
		{ID: "hydrate", Name: "Hydrate", Enabled: true, EverySeconds: 1800, LiveOnly: true},
		{ID: "discord", Name: "Discord plug", Enabled: false, EverySeconds: 2700, ChatLines: 10, LiveOnly: true},
	} {
		if err := do("PUT", "/v1/timers/"+t.ID, t, nil); err != nil {
			return err
		}
	}

	// Quotes.
	for _, q := range []struct{ text, by string }{
		{"I have never once been wrong about a jump. Except that one. And that one.", "MossyToad"},
		{"The map is a suggestion.", "LunarLynx"},
		{"We don't say 'boss fight', we say 'learning opportunity'.", "byte_badger"},
		{"Five more minutes, then bed. — said three hours ago", "NovaNebula"},
		{"Chat, I am the tutorial now.", "TofuTamer"},
	} {
		body := map[string]string{"text": q.text, "added_by": q.by, "request_id": events.NewID()}
		if err := do("POST", "/v1/quotes", body, nil); err != nil {
			return err
		}
	}

	// A wheel.
	if err := do("POST", "/v1/wheels", map[string]any{"name": "next-game", "title": "What do we play next?", "choices": []wheels.Choice{
		{Label: "Cozy farming", Weight: 2}, {Label: "Retro platformer", Weight: 1}, {Label: "Puzzle night", Weight: 1},
		{Label: "Horror (lights on)", Weight: 1}, {Label: "Viewer's choice", Weight: 1}, {Label: "Speedrun practice", Weight: 1},
	}, "spin_seconds": 6}, nil); err != nil {
		return err
	}

	// An alert overlay.
	if err := do("POST", "/v1/overlays", map[string]string{"name": "alerts"}, nil); err != nil {
		return err
	}

	// A giveaway, open, that chat enters.
	st := giveaways.Settings{Title: "Indie game key", Command: "enter", Winners: 1, SubscriberChances: 2}
	if err := do("POST", "/v1/giveaways", st, nil); err != nil {
		return err
	}

	// The stream: follows, subs, cheers, a raid, and chat.
	for _, v := range viewers[:6] {
		tw.Follow(channel, v)
	}
	tw.Subscribe(channel, "LunarLynx", "1000", false)
	tw.Resub(channel, "MossyToad", "1000", 14, 6, "fourteen months of cozy")
	tw.Gift(channel, "NovaNebula", "1000", 5)
	tw.Cheer(channel, "GlitchGecko", 500, "Cheer500 for the boss fight")
	tw.Raid(channel, "StarlitOtter", 42)
	for _, l := range chatLines {
		tw.Say(channel, l.who, l.text)
		time.Sleep(60 * time.Millisecond)
	}
	time.Sleep(2 * time.Second) // let the workflows answer
	return nil
}

func signIn(ctx context.Context, c *client.Client, tw *mocktwitch.Server, role twitch.Role, login string) error {
	var p twitch.PendingSignIn
	if err := c.Do(ctx, "POST", "/v1/connections/twitch/"+string(role)+"/sign-in", nil, &p); err != nil {
		return fmt.Errorf("sign in %s: %w", role, err)
	}
	if err := tw.Approve(p.UserCode, login); err != nil {
		return err
	}
	for tries := 0; tries < 100; tries++ {
		var conn struct {
			Twitch twitch.Status `json:"twitch"`
		}
		acct := &conn.Twitch.Streamer
		if role == twitch.RoleBot {
			acct = &conn.Twitch.Bot
		}
		if err := c.Do(ctx, "GET", "/v1/connections", nil, &conn); err == nil && acct.State == twitch.StateConnected {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("%s never signed in", role)
}

// shot is one picture: the screen's sidebar label, and script steps to
// run there first (each a line using __sg).
type shot struct {
	name   string
	screen string
	steps  []string
	before func(context.Context, *client.Client, *mocktwitch.Server) error
}

var shots = []shot{
	{name: "home", screen: "Home"},
	{name: "raid-guard", screen: "Live"},
	{name: "live", screen: "Live", steps: []string{`__sg.click("Dismiss")`}},
	{name: "workflows", screen: "Workflows", steps: []string{`__sg.click("Open all")`}},
	{name: "workflow-editor", screen: "Workflows", steps: []string{`__sg.click("Open all")`, `__sg.nth("Edit", 1).click()`}},
	{name: "events", screen: "Events"},
	{name: "timers", screen: "Timers"},
	{name: "quotes", screen: "Quotes"},
	{name: "overlays", screen: "Overlays"},
	{name: "giveaways", screen: "Giveaways"},
	{name: "points", screen: "Points"},
	{name: "viewers", screen: "Viewers"},
	{name: "filters", screen: "Chat filters"},
	{name: "banlists", screen: "Ban lists"},
	{name: "wheels", screen: "Wheels"},
	// The stand-in Twitch's notice is for testers, not the picture.
	{name: "connections", screen: "Connections", steps: []string{`__sg.hide('section[aria-label="Test server"]')`}},
	{name: "settings", screen: "Settings"},
}
