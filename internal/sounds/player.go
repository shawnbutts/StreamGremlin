package sounds

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// The player is the desktop app's window: it asks for what to play with a
// long poll, plays it, and reports whether it started. Nothing is played
// when no window is asking, and the engine says so instead of guessing.

// Errors from playing.
var (
	// ErrNoPlayer: no StreamGremlin window is open to play sounds.
	ErrNoPlayer = errors.New("sounds: no StreamGremlin window is open to play sounds")
	// ErrNoAnswer: the app was asked, but didn't say whether it played.
	ErrNoAnswer = errors.New("sounds: the app didn't say whether the sound played")
)

// Command is one thing for the player to do.
type Command struct {
	Seq    int64     `json:"seq"`
	ID     string    `json:"id"`
	Kind   string    `json:"kind"` // play or stop
	Sound  string    `json:"sound,omitempty"`
	Format string    `json:"format,omitempty"`
	Volume int       `json:"volume,omitempty"`
	At     time.Time `json:"at"`
}

// Report is the player's answer to a play.
type Report struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Timing.
const (
	// PlayerGone: with no poll for this long, no window is playing.
	PlayerGone = 40 * time.Second
	// Stale commands aren't played: a window that wakes after a long pause
	// shouldn't play a burst of old sounds.
	Stale = 10 * time.Second
	// AnswerWait is how long a play waits for the window's report.
	AnswerWait = 5 * time.Second
	// rememberPlays: a retried step doesn't play again for this long.
	rememberPlays = time.Hour
	keepCommands  = 50
)

// Player hands commands to the window and collects its reports.
type Player struct {
	now func() time.Time

	mu       sync.Mutex
	seq      int64
	commands []Command
	polling  int // polls waiting now
	lastPoll time.Time
	notify   chan struct{}
	waiting  map[string]chan Report
	played   map[string]time.Time // idempotency keys of plays sent
}

// NewPlayer returns a Player. now may be nil.
func NewPlayer(now func() time.Time) *Player {
	if now == nil {
		now = time.Now
	}
	return &Player{now: now, notify: make(chan struct{}), waiting: map[string]chan Report{}, played: map[string]time.Time{}}
}

// Connected reports whether a window is asking for sounds.
func (p *Player) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connectedLocked()
}

func (p *Player) connectedLocked() bool {
	return p.polling > 0 || (!p.lastPoll.IsZero() && p.now().Sub(p.lastPoll) < PlayerGone)
}

func (p *Player) pushLocked(c Command) {
	p.seq++
	c.Seq, c.At = p.seq, p.now()
	p.commands = append(p.commands, c)
	if len(p.commands) > keepCommands {
		p.commands = p.commands[len(p.commands)-keepCommands:]
	}
	close(p.notify)
	p.notify = make(chan struct{})
}

// Next returns the commands after seq, waiting up to wait for one. after
// below 0 asks only for the current position: a window that just opened
// plays nothing from before it.
func (p *Player) Next(ctx context.Context, after int64, wait time.Duration) ([]Command, int64) {
	p.mu.Lock()
	p.polling++
	p.lastPoll = p.now()
	defer func() {
		p.mu.Lock()
		p.polling--
		p.lastPoll = p.now()
		p.mu.Unlock()
	}()
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		if after < 0 {
			head := p.seq
			p.mu.Unlock()
			return []Command{}, head
		}
		out := []Command{}
		for _, c := range p.commands {
			if c.Seq > after && p.now().Sub(c.At) < Stale {
				out = append(out, c)
			}
		}
		head := p.seq
		if len(out) > 0 || head > after {
			p.mu.Unlock()
			return out, head
		}
		ch := p.notify
		p.mu.Unlock()
		select {
		case <-ch:
		case <-deadline.C:
			return []Command{}, head
		case <-ctx.Done():
			return []Command{}, head
		}
		p.mu.Lock()
	}
}

// Report records the window's answer to a play.
func (p *Player) Report(r Report) bool {
	p.mu.Lock()
	ch, ok := p.waiting[r.ID]
	delete(p.waiting, r.ID)
	p.mu.Unlock()
	if ok {
		ch <- r
	}
	return ok
}

// Result is how a play went.
type Result struct {
	ID       string `json:"id"`
	Sound    string `json:"sound"`
	Volume   int    `json:"volume"`
	Played   bool   `json:"played"`
	Repeated bool   `json:"repeated,omitempty"` // a retried step: not played again
}

// Play asks the window to play a sound at a volume (0 to 100), and waits
// for it to say it started. key, when set, is the step's idempotency key:
// the same key never plays twice.
func (p *Player) Play(ctx context.Context, s Sound, volume int, key string) (Result, error) {
	res := Result{Sound: s.Name, Volume: volume}
	p.mu.Lock()
	now := p.now()
	for k, at := range p.played {
		if now.Sub(at) > rememberPlays {
			delete(p.played, k)
		}
	}
	if _, seen := p.played[key]; key != "" && seen {
		p.mu.Unlock()
		res.Repeated = true
		return res, nil
	}
	if !p.connectedLocked() {
		p.mu.Unlock()
		return res, ErrNoPlayer
	}
	res.ID = events.NewID()
	ch := make(chan Report, 1)
	p.waiting[res.ID] = ch
	p.pushLocked(Command{ID: res.ID, Kind: "play", Sound: s.Name, Format: s.Format, Volume: volume})
	if key != "" {
		// Sent: a retry mustn't play it again, whatever the answer.
		p.played[key] = now
	}
	p.mu.Unlock()

	timer := time.NewTimer(AnswerWait)
	defer timer.Stop()
	select {
	case r := <-ch:
		if !r.OK {
			p.mu.Lock()
			delete(p.played, key) // nothing played: a retry may try again
			p.mu.Unlock()
			if r.Error == "" {
				r.Error = "it didn't play"
			}
			return res, fmt.Errorf("sounds: the app couldn't play %s: %s", s.Name, r.Error)
		}
		res.Played = true
		return res, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	p.mu.Lock()
	delete(p.waiting, res.ID)
	p.mu.Unlock()
	return res, ErrNoAnswer
}

// Stop asks the window to stop every sound. It doesn't wait.
func (p *Player) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connectedLocked() {
		return ErrNoPlayer
	}
	p.pushLocked(Command{ID: events.NewID(), Kind: "stop"})
	return nil
}
