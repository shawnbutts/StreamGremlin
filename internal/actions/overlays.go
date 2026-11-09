package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/sounds"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// clip shortens text to n characters, with an ellipsis: a long chat line
// shouldn't stop an alert.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// registerOverlays adds "Show an alert" (docs/adr/0017-alert-overlays.md).
// It's a Preview action: a test event shows the alert in the app's
// previews only, never in OBS, and plays no sound.
func registerOverlays(reg *workflow.Registry, store *overlays.Store, hub *overlays.Hub, tw *twitch.Service, lib *sounds.Store) {
	reg.Add(workflow.Action{
		Name: "overlays.alert", Label: "Show an alert", Group: "Overlays", External: true, Preview: true,
		Description: "Shows an alert on one of your alert overlays in OBS, after any alerts before it, with the viewer's " +
			"profile picture and a sound if you like. The overlay plays the sound, so OBS hears it through the browser " +
			"source. A test shows it in the app's preview only, silently.",
		Params: []workflow.Param{
			{Name: "overlay", Label: "Overlay", Type: "text", Required: true, Literal: true, Editor: "overlay",
				Pattern: overlays.NamePattern, PatternHint: "an overlay's name, like alerts"},
			{Name: "title", Label: "Title", Type: "text", Editor: "message", DefaultHint: "No title"},
			{Name: "text", Label: "Text", Type: "text", Required: true, Editor: "message"},
			{Name: "seconds", Label: "Seconds on screen", Type: "number", DefaultHint: "The overlay's own time"},
			{Name: "picture", Label: "Show the viewer's profile picture", Type: "bool"},
			{Name: "sound", Label: "Play a sound with it", Type: "text", Editor: "sound",
				Pattern: sounds.NamePattern, PatternHint: "a sound's name, like airhorn", DefaultHint: "No sound"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			name, _ := c.String("overlay", "")
			o, err := store.Get(ctx, name)
			if errors.Is(err, overlays.ErrNotFound) {
				return nil, fmt.Errorf("there's no overlay %q", name)
			}
			if err != nil {
				return nil, err
			}
			title, _ := c.String("title", "")
			text, _ := c.String("text", "")
			a := overlays.Alert{Title: clip(title, overlays.MaxTitle), Text: clip(text, overlays.MaxText), Seconds: o.Look.Seconds}
			if c.Actor != nil {
				name := c.Actor.DisplayName
				if name == "" {
					name = c.Actor.Login
				}
				a.Name = clip(name, 50)
			}
			if a.Text == "" {
				return nil, errors.New("the alert's text is empty")
			}
			if v, ok := c.Args["seconds"]; ok && v != nil {
				n, ok := number(v)
				if !ok || n < overlays.MinSeconds || n > overlays.MaxSeconds {
					return nil, fmt.Errorf("an alert stays %d to %d seconds", overlays.MinSeconds, overlays.MaxSeconds)
				}
				a.Seconds = n
			}
			var snd *sounds.Sound
			if sn, _ := c.String("sound", ""); sn != "" {
				s, err := lib.Get(ctx, sn)
				if errors.Is(err, sounds.ErrNotFound) {
					return nil, fmt.Errorf("there's no sound %q in your library", sn)
				}
				if err != nil {
					return nil, err
				}
				snd = &s
			}
			out := map[string]any{}
			wantPicture, _ := c.Bool("picture", false)
			if c.DryRun {
				res := hub.Preview(o.Name, a)
				out["result"] = res
				if res.Previews == 0 {
					// Say so: a test that reached nobody mustn't read as shown.
					out["preview"] = "nobody saw it: no preview of " + o.Name + " was open. Press Test on the Workflows " +
						"screen to see it, or Preview an alert on the Overlays screen."
				} else {
					out["preview"] = fmt.Sprintf("shown in %d preview(s) in the app, never in OBS", res.Previews)
				}
				if wantPicture {
					out["picture"] = "not looked up for a test"
				}
				if snd != nil {
					out["sound"] = "would play " + snd.Name
				}
				return out, nil
			}
			if wantPicture {
				switch {
				case c.Actor == nil || c.Actor.Platform != "twitch" || c.Actor.ID == "":
					out["picture"] = "none: this event has no Twitch viewer"
				case tw == nil:
					out["picture"] = "none: Twitch isn't set up"
				default:
					pic, err := tw.ProfilePicture(ctx, c.Actor.ID)
					switch {
					case err != nil:
						// A picture is decoration: the alert still shows.
						out["picture"] = "none: " + err.Error()
					case pic == "":
						out["picture"] = "none: Twitch has no picture for them"
					default:
						a.Picture = pic
						out["picture"] = pic
					}
				}
			}
			if snd != nil {
				// The overlay page plays it, so OBS hears it through the
				// browser source, in time with the alert.
				a.Sound = &overlays.AlertSound{Name: snd.Name, Volume: snd.Volume}
			}
			res, err := hub.Show(ctx, o.Name, a, c.IdempotencyKey)
			out["result"] = res
			if snd != nil && res.SoundPlayed {
				out["sound"] = "played " + snd.Name + " in the overlay"
			}
			if err != nil {
				return out, alertError(o.Name, err)
			}
			return out, nil
		},
	})
}

// alertError says what went wrong in words, keeping the error to match.
func alertError(name string, err error) error {
	switch {
	case errors.Is(err, overlays.ErrNotOpen):
		return fmt.Errorf("the overlay %q isn't open in OBS: add its link as a browser source in a scene (%w)", name, err)
	case errors.Is(err, overlays.ErrNoAnswer):
		return fmt.Errorf("the overlay %q was sent the alert but didn't say whether it showed it (%w)", name, err)
	case errors.Is(err, overlays.ErrSoundFailed), errors.Is(err, overlays.ErrSoundNoAnswer):
		return fmt.Errorf("%q: %w. If the overlay was open in OBS before StreamGremlin was updated, refresh the browser source "+
			"once (Properties, Refresh cache of current page)", name, err)
	case errors.Is(err, overlays.ErrBusy), errors.Is(err, overlays.ErrWaitedTooLong):
		return fmt.Errorf("%q: %w", name, err)
	}
	return err
}
