package chat

import (
	"strings"
	"sync"
	"time"
)

// OwnSends remembers chat that workflows sent as the streamer, so its echo
// from the platform (Twitch's EventSub, YouTube's chat stream) isn't stored
// as the streamer chatting: a workflow that answers chat would otherwise
// answer itself, again and again. The zero value is ready to use.
//
// The echo can arrive before the platform's answer to the send, so a send
// is expected by its text first, and by its message id once it's known.
type OwnSends struct {
	mu   sync.Mutex
	sent []ownSend
}

type ownSend struct {
	text string
	id   string
	at   time.Time
	done bool // its echo arrived
}

// OwnSendTTL is how long an echo is waited for. EventSub delivers chat in
// well under a second; this is generous.
const OwnSendTTL = 30 * time.Second

func (o *OwnSends) prune(now time.Time) {
	kept := o.sent[:0]
	for _, s := range o.sent {
		if now.Sub(s.at) < OwnSendTTL && !s.done {
			kept = append(kept, s)
		}
	}
	o.sent = kept
}

// Expect records a send about to happen.
func (o *OwnSends) Expect(text string, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.prune(now)
	o.sent = append(o.sent, ownSend{text: strings.TrimSpace(text), at: now})
}

// Settle records the message id Twitch gave the send with this text, or
// forgets the send when it failed (id "").
func (o *OwnSends) Settle(text, id string, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	text = strings.TrimSpace(text)
	for i := range o.sent {
		s := &o.sent[i]
		if s.text == text && s.id == "" && !s.done {
			if id == "" {
				s.done = true // failed: nothing will echo
			} else {
				s.id = id
			}
			break
		}
	}
	o.prune(now)
}

// Claim reports whether a message from the streamer's account is the echo
// of a workflow's send, and if so marks it seen.
func (o *OwnSends) Claim(id, text string, now time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.prune(now)
	text = strings.TrimSpace(text)
	for i := range o.sent {
		if s := &o.sent[i]; s.id != "" && s.id == id {
			s.done = true
			return true
		}
	}
	for i := range o.sent {
		if s := &o.sent[i]; s.id == "" && s.text == text {
			s.done = true
			return true
		}
	}
	return false
}
