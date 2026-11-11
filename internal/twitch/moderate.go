package twitch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Moderating from chat (M7 slice 3; D-079): Twitch's documented
// moderation endpoints, called as the streamer. Twitch gives these no
// idempotency key, so nothing here is retried: an answer that doesn't come
// leaves the outcome unknown, and the caller says so.

// Scopes moderating asks for.
const (
	BansScope        = "moderator:manage:banned_users"
	ChatDeleteScope  = "moderator:manage:chat_messages"
	WarningsScope    = "moderator:manage:warnings"
	ShoutoutsScope   = "moderator:manage:shoutouts"
	VIPsManageScope  = "channel:manage:vips"
	ModsManageScope  = "channel:manage:moderators"
	MaxTimeout       = 1_209_600 // two weeks, in seconds
	MaxModReason     = 500
	SuspiciousManage = "moderator:manage:suspicious_users"
)

// Moderation actions.
const (
	ModDelete     = "delete"
	ModTimeout    = "timeout"
	ModBan        = "ban"
	ModUnban      = "unban"
	ModWarn       = "warn"
	ModShoutout   = "shoutout"
	ModVIP        = "vip"
	ModUnVIP      = "unvip"
	ModMod        = "mod"
	ModUnmod      = "unmod"
	ModMonitor    = "monitor"
	ModRestrict   = "restrict"
	ModUnsuspect  = "unsuspect"
	modStateDone  = "done"
	modStateNo    = "refused"
	modStateMaybe = "unknown"
)

// ModActions are every action, in the order screens list them.
var ModActions = []string{ModDelete, ModTimeout, ModBan, ModUnban, ModWarn, ModShoutout, ModVIP, ModUnVIP, ModMod, ModUnmod,
	ModMonitor, ModRestrict, ModUnsuspect}

// Mod is one moderation request to Twitch.
type Mod struct {
	Action    string
	UserID    string // everything but delete
	MessageID string // delete
	Seconds   int    // timeout: 1 to MaxTimeout
	Reason    string // ban, timeout (optional), warn (required)
}

// ModOutcome is how Twitch answered: done, refused (nothing happened,
// with why) or unknown (no answer: it may or may not have happened).
type ModOutcome struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Outcome states.
const (
	ModDone    = modStateDone
	ModRefused = modStateNo
	ModUnknown = modStateMaybe
)

// CheckMod reports what's wrong with a request before anything is sent.
func CheckMod(m Mod) error {
	bad := func(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }
	known := false
	for _, a := range ModActions {
		known = known || a == m.Action
	}
	switch {
	case !known:
		return bad("unknown action %q", m.Action)
	case m.Action == ModDelete && !validMessageID(m.MessageID):
		return bad("deleting needs the message's id")
	case m.Action != ModDelete && !userIDPattern(m.UserID):
		return bad("a Twitch user id is digits")
	case m.Action == ModTimeout && (m.Seconds < 1 || m.Seconds > MaxTimeout):
		return bad("a timeout is 1 second to 2 weeks (%d seconds)", MaxTimeout)
	case m.Action != ModTimeout && m.Seconds != 0:
		return bad("only a timeout has a length")
	case m.Action == ModWarn && strings.TrimSpace(m.Reason) == "":
		return bad("a warning needs a reason")
	case len([]rune(m.Reason)) > MaxModReason:
		return bad("a reason is up to %d characters", MaxModReason)
	case m.Reason != "" && m.Action != ModBan && m.Action != ModTimeout && m.Action != ModWarn:
		return bad("only a ban, a timeout or a warning has a reason")
	}
	return nil
}

func validMessageID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		ok := r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !ok {
			return false
		}
	}
	return true
}

// Moderate sends one request to Twitch as the streamer and says how it
// went. It's never retried.
func (s *Service) Moderate(ctx context.Context, m Mod) (ModOutcome, error) {
	if err := CheckMod(m); err != nil {
		return ModOutcome{}, err
	}
	me := s.streamer.Status().UserID
	q := url.Values{"moderator_id": {me}}
	var body any
	method, path, scope := "POST", "", ""
	switch m.Action {
	case ModDelete:
		method, path, scope = "DELETE", "/moderation/chat", ChatDeleteScope
		q.Set("message_id", m.MessageID)
	case ModTimeout, ModBan:
		path, scope = "/moderation/bans", BansScope
		d := map[string]any{"user_id": m.UserID}
		if m.Action == ModTimeout {
			d["duration"] = m.Seconds
		}
		if m.Reason != "" {
			d["reason"] = m.Reason
		}
		body = struct {
			Data map[string]any `json:"data"`
		}{d}
	case ModUnban:
		method, path, scope = "DELETE", "/moderation/bans", BansScope
		q.Set("user_id", m.UserID)
	case ModWarn:
		path, scope = "/moderation/warnings", WarningsScope
		body = struct {
			Data map[string]any `json:"data"`
		}{map[string]any{"user_id": m.UserID, "reason": m.Reason}}
	case ModShoutout:
		path, scope = "/chat/shoutouts", ShoutoutsScope
		q.Set("from_broadcaster_id", me)
		q.Set("to_broadcaster_id", m.UserID)
	case ModVIP, ModUnVIP:
		path, scope = "/channels/vips", VIPsManageScope
		if m.Action == ModUnVIP {
			method = "DELETE"
		}
		q = url.Values{"user_id": {m.UserID}}
	case ModMod, ModUnmod:
		path, scope = "/moderation/moderators", ModsManageScope
		if m.Action == ModUnmod {
			method = "DELETE"
		}
		q = url.Values{"user_id": {m.UserID}}
	case ModMonitor, ModRestrict:
		path, scope = "/moderation/suspicious_users", SuspiciousManage
		status := "ACTIVE_MONITORING"
		if m.Action == ModRestrict {
			status = "RESTRICTED"
		}
		body = struct {
			UserID string `json:"user_id"`
			Status string `json:"status"`
		}{m.UserID, status}
	case ModUnsuspect:
		method, path, scope = "DELETE", "/moderation/suspicious_users", SuspiciousManage
		q.Set("user_id", m.UserID)
	}
	err := s.streamerRaw(ctx, scope, method, path, q, body, nil)
	return modOutcome(err), nil
}

// modOutcome puts Twitch's answer in words. Only a request Twitch answered
// (or one never sent) is known not to have happened.
func modOutcome(err error) ModOutcome {
	if err == nil {
		return ModOutcome{State: ModDone}
	}
	switch {
	case errors.Is(err, errStreamerSignIn):
		return ModOutcome{State: ModRefused, Detail: "Sign in with your Twitch account first."}
	case errors.Is(err, ErrNeedsPermission):
		return ModOutcome{State: ModRefused, Detail: "Sign in to Twitch again and allow StreamGremlin to moderate."}
	case errors.Is(err, ErrNoClientID):
		return ModOutcome{State: ModRefused, Detail: "Twitch isn't set up: add a Client ID on Connections."}
	}
	var he *helixError
	if errors.As(err, &he) {
		switch he.Status {
		case 401:
			return ModOutcome{State: ModRefused, Detail: "Sign in to Twitch again and allow StreamGremlin to moderate (" + he.Message + ")."}
		case 429:
			return ModOutcome{State: ModRefused, Detail: "Twitch says to wait: " + he.Message}
		}
		return ModOutcome{State: ModRefused, Detail: "Twitch refused: " + he.Message + " (" + strconv.Itoa(he.Status) + ")"}
	}
	return ModOutcome{State: ModUnknown, Detail: "Twitch didn't answer, so it may or may not have happened. Check chat before trying again."}
}
