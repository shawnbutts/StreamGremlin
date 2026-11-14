package moderation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

// Shield Mode and locking chat (M8 slice 1, D-105). Twitch only.
//
// A lock is the panic button: Shield Mode on, then emote-only and
// followers-only (at least LockFollowerMinutes) on. It records what was
// there before, so an unlock puts back only what it changed, also after a
// restart. One lock or unlock runs at a time, and a second lock is refused
// while one is in place.

// Actions.
const (
	ShieldMode = "shield_mode"
	Lock       = "lock"
	Unlock     = "unlock"
)

// LockFollowerMinutes is how long an account must have followed to chat
// while locked, unless followers-only already asked for longer: a new
// follow bot can't follow and chat at once.
const LockFollowerMinutes = 10

// Shielder is what Shield Mode and locks need from Twitch, beyond
// moderating.
type Shielder interface {
	ShieldMode(ctx context.Context) (twitch.Shield, error)
	SetShieldMode(ctx context.Context, on bool) (twitch.Shield, twitch.ModOutcome, error)
	ChatSettings(ctx context.Context) (twitch.ChatSettings, error)
}

// LockDetail is what a lock found and changed.
type LockDetail struct {
	// Before is the chat modes before, and ShieldBefore Shield Mode; nil
	// when Twitch couldn't say.
	Before       *twitch.ChatSettings `json:"before,omitempty"`
	ShieldBefore *bool                `json:"shield_before,omitempty"`
	// What the lock changed.
	ShieldOn        bool `json:"shield_on"`
	ChatOn          bool `json:"chat_on"`
	FollowerMinutes int  `json:"follower_minutes"`
}

type detailJSON struct {
	On       *bool       `json:"on,omitempty"`
	Lock     *LockDetail `json:"lock,omitempty"`
	UnlockOf string      `json:"unlock_of,omitempty"`
}

// readDetail fills a record's details from detail_json, by its action.
func (r *Record) readDetail(raw string) error {
	switch r.Action {
	case ShieldMode, Lock, Unlock:
		var d detailJSON
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return err
		}
		r.On, r.Lock, r.UnlockOf = d.On, d.Lock, d.UnlockOf
		return nil
	}
	r.Chat = &twitch.ChatSettingsChange{}
	return json.Unmarshal([]byte(raw), r.Chat)
}

// part is one request a lock or unlock made, in words.
type part struct {
	what string
	out  twitch.ModOutcome
}

// combine says how parts went: done when every part was, unknown when any
// is, refused when none was done; a part refused beside one done is said.
func combine(parts []part, done string) twitch.ModOutcome {
	if len(parts) == 0 {
		return twitch.ModOutcome{State: Done, Detail: done}
	}
	var ok, unknown, refused []string
	for _, p := range parts {
		switch p.out.State {
		case Done:
			ok = append(ok, p.what)
		case Unknown:
			unknown = append(unknown, p.what+": "+p.out.Detail)
		default:
			refused = append(refused, p.what+": "+p.out.Detail)
		}
	}
	switch {
	case len(unknown) > 0:
		return twitch.ModOutcome{State: Unknown, Detail: "Twitch didn't answer for " + strings.Join(unknown, "; ") + ". Check Live's chat modes."}
	case len(ok) == 0:
		return twitch.ModOutcome{State: Refused, Detail: strings.Join(refused, "; ")}
	case len(refused) > 0:
		return twitch.ModOutcome{State: Done, Detail: done + " But Twitch refused " + strings.Join(refused, "; ")}
	}
	return twitch.ModOutcome{State: Done, Detail: done}
}

// shieldOrLock does a shield_mode, lock or unlock request, and returns the
// outcome and the details to keep.
func (s *Service) shieldOrLock(ctx context.Context, rec *Record) (twitch.ModOutcome, string) {
	sh, ok := s.tw.(Shielder)
	if !ok {
		return twitch.ModOutcome{State: Refused, Detail: "Shield Mode isn't available here."}, ""
	}
	switch rec.Action {
	case ShieldMode:
		st, out, err := sh.SetShieldMode(ctx, *rec.On)
		if err != nil {
			out = twitch.ModOutcome{State: Refused, Detail: err.Error()}
		}
		if out.State == Done {
			rec.Shield = &st
		}
		b, _ := json.Marshal(detailJSON{On: rec.On})
		return out, string(b)
	case Lock:
		out := s.lock(ctx, sh, rec)
		b, _ := json.Marshal(detailJSON{Lock: rec.Lock})
		return out, string(b)
	}
	out := s.unlock(ctx, sh, rec)
	b, _ := json.Marshal(detailJSON{UnlockOf: rec.UnlockOf})
	return out, string(b)
}

func (s *Service) lock(ctx context.Context, sh Shielder, rec *Record) twitch.ModOutcome {
	if open, err := s.Locked(ctx); err != nil {
		return twitch.ModOutcome{State: Refused, Detail: "The record couldn't be read: " + err.Error()}
	} else if open != nil {
		return twitch.ModOutcome{State: Refused, Detail: "Chat is locked already: unlock it first."}
	}
	d := &LockDetail{FollowerMinutes: LockFollowerMinutes}
	if cs, err := sh.ChatSettings(ctx); err == nil {
		d.Before = &cs
		if cs.FollowersOnly && cs.FollowerMinutes != nil && *cs.FollowerMinutes > d.FollowerMinutes {
			d.FollowerMinutes = *cs.FollowerMinutes
		}
	}
	if st, err := sh.ShieldMode(ctx); err == nil {
		d.ShieldBefore = &st.Active
	}
	rec.Lock = d
	var parts []part
	if d.ShieldBefore == nil || !*d.ShieldBefore {
		st, out, err := sh.SetShieldMode(ctx, true)
		if err != nil {
			out = twitch.ModOutcome{State: Refused, Detail: err.Error()}
		}
		if out.State == Done {
			rec.Shield = &st
		}
		d.ShieldOn = out.State != Refused
		parts = append(parts, part{"Shield Mode", out})
	}
	on, minutes := true, d.FollowerMinutes
	cs, out, err := s.tw.ChangeChatSettings(ctx, twitch.ChatSettingsChange{EmoteOnly: &on, FollowersOnly: &on, FollowerMinutes: &minutes})
	if err != nil {
		out = twitch.ModOutcome{State: Refused, Detail: err.Error()}
	}
	if out.State == Done {
		rec.Settings = &cs
	}
	d.ChatOn = out.State != Refused
	parts = append(parts, part{"emote-only and followers-only", out})
	return combine(parts, fmt.Sprintf("Locked: Shield Mode, emote-only and followers-only (followed %d minutes) on.", minutes))
}

func (s *Service) unlock(ctx context.Context, sh Shielder, rec *Record) twitch.ModOutcome {
	open, err := s.Locked(ctx)
	if err != nil {
		return twitch.ModOutcome{State: Refused, Detail: "The record couldn't be read: " + err.Error()}
	}
	if open == nil || open.Lock == nil {
		return twitch.ModOutcome{State: Refused, Detail: "Chat isn't locked from StreamGremlin: change the chat modes and Shield Mode on Live instead."}
	}
	rec.UnlockOf = open.ID
	d := open.Lock
	var parts []part
	if d.ShieldOn {
		st, out, err := sh.SetShieldMode(ctx, false)
		if err != nil {
			out = twitch.ModOutcome{State: Refused, Detail: err.Error()}
		}
		if out.State == Done {
			rec.Shield = &st
		}
		parts = append(parts, part{"Shield Mode", out})
	}
	if d.ChatOn {
		// Back as they were; when Twitch couldn't say, off.
		emote, followers := false, false
		var minutes *int
		if b := d.Before; b != nil {
			emote, followers = b.EmoteOnly, b.FollowersOnly
			if followers {
				minutes = b.FollowerMinutes
				if minutes == nil {
					zero := 0
					minutes = &zero
				}
			}
		}
		cs, out, err := s.tw.ChangeChatSettings(ctx, twitch.ChatSettingsChange{EmoteOnly: &emote, FollowersOnly: &followers, FollowerMinutes: minutes})
		if err != nil {
			out = twitch.ModOutcome{State: Refused, Detail: err.Error()}
		}
		if out.State == Done {
			rec.Settings = &cs
		}
		parts = append(parts, part{"the chat modes", out})
	}
	return combine(parts, "Unlocked: chat is back as it was before the lock.")
}

// Locked returns the lock in place, or nil: the newest lock or unlock
// that wasn't refused, when it's a lock.
func (s *Service) Locked(ctx context.Context) (*Record, error) {
	rec, err := scan(s.db.QueryRowContext(ctx, selectRecord+`WHERE action IN ('lock', 'unlock') AND state IN ('done', 'unknown')
		ORDER BY created_at DESC, rowid DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rec.Action != Lock {
		return nil, nil
	}
	return &rec, nil
}
