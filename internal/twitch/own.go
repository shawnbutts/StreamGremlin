package twitch

import (
	"strings"
	"sync"
	"time"
)

// ownSends remembers chat that workflows sent as the streamer, so its echo
// from EventSub isn't stored as the streamer chatting: a workflow that
// answers chat would otherwise answer itself, again and again. (The bot's
// messages are never stored at all.)
//
// The echo can arrive before Twitch's answer to the send, so a send is
// expected by its text first, and by its message id once Twitch says it.
type ownSends struct {
	mu   sync.Mutex
	sent []ownSend
}

type ownSend struct {
	text string
	id   string
	at   time.Time
	done bool // its echo arrived
}

// ownSendTTL is how long an echo is waited for. EventSub delivers chat in
// well under a second; this is generous.
const ownSendTTL = 30 * time.Second

func (o *ownSends) prune(now time.Time) {
	kept := o.sent[:0]
	for _, s := range o.sent {
		if now.Sub(s.at) < ownSendTTL && !s.done {
			kept = append(kept, s)
		}
	}
	o.sent = kept
}

// expect records a send about to happen.
func (o *ownSends) expect(text string, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.prune(now)
	o.sent = append(o.sent, ownSend{text: strings.TrimSpace(text), at: now})
}

// settle records the message id Twitch gave the send with this text, or
// forgets the send when it failed (id "").
func (o *ownSends) settle(text, id string, now time.Time) {
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

// claim reports whether a message from the streamer's account is the echo
// of a workflow's send, and if so marks it seen.
func (o *ownSends) claim(id, text string, now time.Time) bool {
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
