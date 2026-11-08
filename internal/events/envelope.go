// Package events is the event inbox: every outside or simulated event is
// checked, stored once in arrival order, and readable from any position
// (docs/adr/0004-events.md). Readers keep their own cursor; the Notifier
// only tells them something new arrived.
package events

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Quality says how sure the engine is that an event happened.
type Quality string

const (
	// Confirmed: the platform or OBS reported it.
	Confirmed Quality = "confirmed"
	// Estimated: worked out locally, such as when an ad break should end.
	Estimated Quality = "estimated"
	// Synthetic: a test event. Workflows never run outside actions for it.
	Synthetic Quality = "synthetic"
)

// Limits.
const (
	MaxPayloadBytes = 64 << 10
	maxIDLen        = 256
	maxTypeLen      = 128
)

// Actor is who caused an event, on one platform.
type Actor struct {
	Platform    string `json:"platform"`
	ID          string `json:"id"`
	Login       string `json:"login,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// Envelope is one event as it arrives. Payload is a JSON object whose shape
// depends on Type and SchemaVersion.
type Envelope struct {
	EventID         string          `json:"event_id"`
	Type            string          `json:"type"`
	SchemaVersion   int             `json:"schema_version"`
	Source          string          `json:"source"`
	SourceEventID   string          `json:"source_event_id,omitempty"`
	OccurredAt      time.Time       `json:"occurred_at"`
	ReceivedAt      time.Time       `json:"received_at"`
	ChannelID       string          `json:"channel_id,omitempty"`
	Actor           *Actor          `json:"actor,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	CorrelationID   string          `json:"correlation_id"`
	CausationID     string          `json:"causation_id,omitempty"`
	DeliveryQuality Quality         `json:"delivery_quality"`
}

// Record is a stored event with its place in the inbox.
type Record struct {
	Seq int64 `json:"seq"`
	Envelope
}

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("events: invalid event")

var (
	typePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){2,}$`)
	sourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(:[a-z0-9][a-z0-9._-]*)?$`)
	// Platforms go in Source, never in the type (StreamWisp ADR-0004).
	platformPrefixes = []string{"provider.", "twitch.", "youtube.", "kick.", "obs."}
)

// ValidType reports whether t is a well-formed event type:
// <domain>.<entity>[.<more>].<verb>, lowercase, with no platform prefix.
func ValidType(t string) bool {
	if len(t) > maxTypeLen || !typePattern.MatchString(t) {
		return false
	}
	for _, p := range platformPrefixes {
		if strings.HasPrefix(t, p) {
			return false
		}
	}
	return true
}

func validID(s string) bool {
	if s == "" || len(s) > maxIDLen {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Validate checks the envelope. It doesn't fill in anything; see Prepare.
func (e *Envelope) Validate() error {
	switch {
	case !validID(e.EventID):
		return invalid("event_id is missing or malformed")
	case !ValidType(e.Type):
		return invalid("type %q isn't <domain>.<entity>.<verb> in lowercase, without a platform name", e.Type)
	case e.SchemaVersion < 1:
		return invalid("schema_version must be at least 1")
	case len(e.Source) > 64 || !sourcePattern.MatchString(e.Source):
		return invalid("source %q is malformed", e.Source)
	case e.SourceEventID != "" && !validID(e.SourceEventID):
		return invalid("source_event_id is malformed")
	case e.OccurredAt.IsZero() || e.ReceivedAt.IsZero():
		return invalid("occurred_at and received_at are required")
	case e.ChannelID != "" && !validID(e.ChannelID):
		return invalid("channel_id is malformed")
	case !validID(e.CorrelationID):
		return invalid("correlation_id is missing or malformed")
	case e.CausationID != "" && !validID(e.CausationID):
		return invalid("causation_id is malformed")
	}
	switch e.DeliveryQuality {
	case Confirmed, Estimated, Synthetic:
	default:
		return invalid("delivery_quality must be confirmed, estimated or synthetic")
	}
	if a := e.Actor; a != nil {
		if !sourcePattern.MatchString(a.Platform) || !validID(a.ID) || len(a.Login) > 256 || len(a.DisplayName) > 256 {
			return invalid("actor needs a platform and an id")
		}
	}
	if len(e.Payload) > MaxPayloadBytes {
		return invalid("payload is over %d KiB", MaxPayloadBytes>>10)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(e.Payload, &obj); err != nil || obj == nil {
		return invalid("payload must be a JSON object")
	}
	return nil
}

// Prepare fills what an adapter usually leaves out: a new event_id, the
// receive time, schema version 1, and the event's own id as correlation.
func (e *Envelope) Prepare(now time.Time) {
	if e.EventID == "" {
		e.EventID = NewID()
	}
	if e.ReceivedAt.IsZero() {
		e.ReceivedAt = now
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = e.ReceivedAt
	}
	if e.SchemaVersion == 0 {
		e.SchemaVersion = 1
	}
	if e.CorrelationID == "" {
		e.CorrelationID = e.EventID
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
}

// NewID returns a random UUID (version 4) from crypto/rand.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on supported systems
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
