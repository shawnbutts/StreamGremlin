package twitch

import (
	"context"
	"errors"
	"net/url"
	"time"
)

// Shield Mode (M8 slice 1, D-105): Get and Update Shield Mode Status,
// as the streamer. Twitch's Shield Mode applies the stricter moderation
// profile the streamer set up in Twitch's Mod View; StreamGremlin only
// turns it on and off, and never changes that profile.

// ShieldScope lets StreamGremlin read and change Shield Mode; Twitch's
// read-only scope isn't asked for, as this one covers both.
const ShieldScope = "moderator:manage:shield_mode"

// ShieldReadScope only reads it (EventSub accepts either).
const ShieldReadScope = "moderator:read:shield_mode"

// ShieldChanged is the event for Shield Mode turning on or off, from
// anywhere, with enabled and the moderator who did it.
const ShieldChanged = "moderation.shield_mode.changed"

// Shield is Shield Mode's state.
type Shield struct {
	Active          bool       `json:"active"`
	ModeratorLogin  string     `json:"moderator_login,omitempty"`
	ModeratorName   string     `json:"moderator_name,omitempty"`
	LastActivatedAt *time.Time `json:"last_activated_at,omitempty"`
}

type shieldData struct {
	Active         bool   `json:"is_active"`
	ModeratorLogin string `json:"moderator_login"`
	ModeratorName  string `json:"moderator_name"`
	LastActivated  string `json:"last_activated_at"`
}

func (d shieldData) shield() Shield {
	s := Shield{Active: d.Active, ModeratorLogin: d.ModeratorLogin, ModeratorName: d.ModeratorName}
	if t, err := time.Parse(time.RFC3339, d.LastActivated); err == nil && !t.IsZero() {
		s.LastActivatedAt = &t
	}
	return s
}

// ShieldMode reads Shield Mode's state.
func (s *Service) ShieldMode(ctx context.Context) (Shield, error) {
	var out struct {
		Data []shieldData `json:"data"`
	}
	q := url.Values{"moderator_id": {s.streamer.Status().UserID}}
	if err := s.streamerRaw(ctx, ShieldScope, "GET", "/moderation/shield_mode", q, nil, &out); err != nil {
		return Shield{}, viewerError(err)
	}
	if len(out.Data) != 1 {
		return Shield{}, errors.New("twitch: an unreadable Shield Mode answer")
	}
	return out.Data[0].shield(), nil
}

// SetShieldMode turns Shield Mode on or off, once (never retried), and
// returns it as Twitch now has it.
func (s *Service) SetShieldMode(ctx context.Context, on bool) (Shield, ModOutcome, error) {
	var out struct {
		Data []shieldData `json:"data"`
	}
	q := url.Values{"moderator_id": {s.streamer.Status().UserID}}
	// A struct, not a map: broadcaster_id goes in the query here.
	body := struct {
		Active bool `json:"is_active"`
	}{on}
	err := s.streamerRaw(ctx, ShieldScope, "PUT", "/moderation/shield_mode", q, body, &out)
	o := modOutcome(err)
	if o.State != ModDone {
		return Shield{}, o, nil
	}
	if len(out.Data) != 1 {
		return Shield{}, ModOutcome{State: ModUnknown, Detail: "Twitch's answer couldn't be read; check Shield Mode."}, nil
	}
	return out.Data[0].shield(), o, nil
}
