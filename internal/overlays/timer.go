package overlays

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Clock and timer overlays (D-092): one overlay shows a clock (the time,
// and the date if chosen), a countdown, or a stopwatch. The engine keeps
// a countdown's and a stopwatch's state, so it survives a restart, and
// says when a countdown ends; the page counts the seconds itself from
// that state. A clock needs nothing from the engine: the page shows this
// computer's time.

// KindTimer shows a clock, a countdown or a stopwatch.
const KindTimer = "timer"

// What a timer overlay shows: Look.TimerMode.
const (
	TimerClock     = "clock"
	TimerCountdown = "countdown"
	TimerStopwatch = "stopwatch"
)

// TimerModes are the modes, in the order the app offers them.
var TimerModes = []string{TimerClock, TimerCountdown, TimerStopwatch}

// What a clock shows under the time: Look.ClockDate.
var ClockDates = []string{"", "short", "long"}

// Limits.
const (
	MaxTimerSeconds = 24 * 60 * 60 // a countdown's length, and what it can grow to
	MaxTimerText    = 60
	// DefaultTimerSeconds is a countdown's length when none is set.
	DefaultTimerSeconds = 5 * 60
)

// TimerEnded is the event a countdown sends when it reaches zero.
const TimerEnded = "overlays.timer.ended"

func checkTimer(l Look) error {
	switch {
	case l.TimerMode != "" && !oneOf(l.TimerMode, TimerModes):
		return invalid("a timer shows a clock, a countdown or a stopwatch")
	case !oneOf(l.ClockDate, ClockDates):
		return invalid("the clock's date is none, short or long")
	case l.TimerLength < 0 || l.TimerLength > MaxTimerSeconds:
		return invalid("a countdown is up to 24 hours")
	case !oneLine(l.TimerLabel, MaxTimerText) || !oneLine(l.TimerEndText, MaxTimerText):
		return invalid("a timer's label and end text are one line, up to %d characters", MaxTimerText)
	}
	return nil
}

func oneLine(s string, max int) bool {
	return utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\r\n\x00")
}

// timerLook reports whether a look sets anything only timers use.
func timerLook(l Look) bool {
	return l.TimerMode != "" || l.TimerLabel != "" || l.TimerEndText != "" || l.TimerLength != 0 || l.Clock24 || l.ClockSeconds || l.ClockDate != "" ||
		l.TimerAutoStart
}

// TimerState is a countdown's or a stopwatch's state. A countdown has
// BaseMS left at Since and counts down while running; a stopwatch has
// counted BaseMS at Since and counts up.
type TimerState struct {
	Running bool      `json:"running"`
	BaseMS  int64     `json:"base_ms"`
	Since   time.Time `json:"since"`
	// Ended: a countdown reached zero (and said so).
	Ended bool `json:"ended,omitempty"`
}

// value is the state's milliseconds at now: left for a countdown (never
// below zero), counted for a stopwatch.
func (s TimerState) value(mode string, now time.Time) int64 {
	if !s.Running {
		return s.BaseMS
	}
	d := now.Sub(s.Since).Milliseconds()
	if mode == TimerCountdown {
		return max(0, s.BaseMS-d)
	}
	return s.BaseMS + d
}

// TimerView is what a timer overlay's page is sent: the state, and the
// engine's time, so the page counts from the same clock.
type TimerView struct {
	Running bool  `json:"running"`
	BaseMS  int64 `json:"base_ms"`
	SinceMS int64 `json:"since_ms"`
	NowMS   int64 `json:"now_ms"`
	Ended   bool  `json:"ended,omitempty"`
}

// SetTimer sends a timer overlay its state, and keeps it for pages that
// open later. nil: the overlay's own starting state (a countdown at its
// full length, a stopwatch at zero).
func (h *Hub) SetTimer(name string, v *TimerView) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if v == nil {
		delete(h.timers, name)
	} else {
		h.timers[name] = *v
	}
	b := message("timer", v)
	for c := range h.conns[name] {
		if c.kind == KindTimer {
			h.sendLocked(c, b)
		}
	}
}

// OnTimerLoaded says what to call when OBS loads a timer overlay's page.
func (h *Hub) OnTimerLoaded(f func(name string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timerLoaded = f
}

// loaded: a page said it just loaded. Only OBS's pages of timer
// overlays count; the app's previews never start anything.
func (h *Hub) loaded(c *conn) {
	if c.preview || c.kind != KindTimer {
		return
	}
	h.mu.Lock()
	f := h.timerLoaded
	h.mu.Unlock()
	if f != nil {
		f(c.overlay)
	}
}

// Errors from controlling a timer.
var ErrNotTimer = errors.New("overlays: that overlay isn't a countdown or stopwatch")

// TimerOp is what to do to a timer overlay.
type TimerOp struct {
	// Action is start, pause, reset or add.
	Action string `json:"action"`
	// Seconds: for start, a countdown's new length (0 keeps it); for
	// add, how much longer a countdown runs.
	Seconds int `json:"seconds,omitempty"`
	// Until: for start, count down to this time today (or tomorrow, if
	// it's passed), like 19:00.
	Until string `json:"until,omitempty"`
}

// Settings keeps timers' states.
type Settings interface {
	Load(ctx context.Context, key string, v any) (bool, error)
	Save(ctx context.Context, key string, v any) error
}

// Timers keeps countdowns' and stopwatches' states, and says when a
// countdown ends.
type Timers struct {
	store    *Store
	hub      *Hub
	settings Settings
	inbox    *events.Inbox
	log      *slog.Logger
	now      func() time.Time

	mu          sync.Mutex
	ends        map[string]*time.Timer
	autoStarted map[string]time.Time
}

// NewTimers returns Timers; Start loads saved states.
func NewTimers(store *Store, hub *Hub, settings Settings, inbox *events.Inbox, log *slog.Logger) *Timers {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Timers{store: store, hub: hub, settings: settings, inbox: inbox, log: log, now: time.Now, ends: map[string]*time.Timer{},
		autoStarted: map[string]time.Time{}}
}

func timerKey(name string) string { return "overlays.timer." + name }

// Start loads every timer overlay's saved state and sends it to the hub.
// A countdown that ended while StreamGremlin was off is ended, without
// saying so late.
func (t *Timers) Start(ctx context.Context) error {
	all, err := t.store.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.Kind != KindTimer {
			continue
		}
		st, ok, err := t.load(ctx, o.Name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		t.mu.Lock()
		if mode(o) == TimerCountdown && st.Running && !st.Ended && st.value(TimerCountdown, t.now()) == 0 {
			st = TimerState{BaseMS: 0, Since: t.now(), Ended: true}
			_ = t.settings.Save(ctx, timerKey(o.Name), st)
		}
		t.publishLocked(o, &st)
		t.mu.Unlock()
	}
	return nil
}

// Close stops waiting for countdowns to end.
func (t *Timers) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for name, e := range t.ends {
		e.Stop()
		delete(t.ends, name)
	}
}

func mode(o Overlay) string {
	if o.Look.TimerMode == "" {
		return TimerClock
	}
	return o.Look.TimerMode
}

func length(o Overlay) int64 {
	if o.Look.TimerLength > 0 {
		return int64(o.Look.TimerLength) * 1000
	}
	return DefaultTimerSeconds * 1000
}

// load reads a saved state. Reset saves null: nothing saved.
func (t *Timers) load(ctx context.Context, name string) (TimerState, bool, error) {
	var st *TimerState
	ok, err := t.settings.Load(ctx, timerKey(name), &st)
	if err != nil || !ok || st == nil {
		return TimerState{}, false, err
	}
	return *st, true, nil
}

// State is a timer overlay's state: nil when it's at its start.
func (t *Timers) State(ctx context.Context, name string) (*TimerState, error) {
	st, ok, err := t.load(ctx, name)
	if err != nil || !ok {
		return nil, err
	}
	return &st, nil
}

// Do starts, pauses, resets or adds time to a timer overlay.
func (t *Timers) Do(ctx context.Context, name string, op TimerOp) (TimerState, error) {
	o, err := t.store.Get(ctx, name)
	if err != nil {
		return TimerState{}, err
	}
	m := mode(o)
	if o.Kind != KindTimer || m == TimerClock {
		return TimerState{}, ErrNotTimer
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st, saved, err := t.load(ctx, name)
	if err != nil {
		return TimerState{}, err
	}
	now := t.now()
	if !saved {
		st = TimerState{Since: now}
		if m == TimerCountdown {
			st.BaseMS = length(o)
		}
	}
	at := st.value(m, now)
	switch op.Action {
	case "start":
		switch {
		case op.Until != "" && m == TimerCountdown:
			end, err := until(op.Until, now)
			if err != nil {
				return TimerState{}, err
			}
			st = TimerState{Running: true, BaseMS: end.Sub(now).Milliseconds(), Since: now}
		case op.Until != "":
			return TimerState{}, invalid("only a countdown counts to a time")
		case op.Seconds < 0 || op.Seconds > MaxTimerSeconds:
			return TimerState{}, invalid("a countdown is up to 24 hours")
		case op.Seconds > 0 && m == TimerCountdown:
			st = TimerState{Running: true, BaseMS: int64(op.Seconds) * 1000, Since: now}
		case op.Seconds > 0:
			return TimerState{}, invalid("only a countdown has a length")
		case m == TimerCountdown && at == 0:
			st = TimerState{Running: true, BaseMS: length(o), Since: now} // ended: again from the top
		default:
			st = TimerState{Running: true, BaseMS: at, Since: now} // from where it is
		}
	case "pause":
		st = TimerState{BaseMS: at, Since: now, Ended: st.Ended}
	case "reset":
		t.stopEndLocked(name)
		if err := t.settings.Save(ctx, timerKey(name), (*TimerState)(nil)); err != nil {
			return TimerState{}, err
		}
		t.hub.SetTimer(name, nil)
		st = TimerState{Since: now}
		if m == TimerCountdown {
			st.BaseMS = length(o)
		}
		return st, nil
	case "add":
		if m != TimerCountdown {
			return TimerState{}, invalid("time is added to a countdown")
		}
		if op.Seconds < 1 || op.Seconds > MaxTimerSeconds || at+int64(op.Seconds)*1000 > MaxTimerSeconds*1000 {
			return TimerState{}, invalid("a countdown is up to 24 hours")
		}
		st = TimerState{Running: st.Running, BaseMS: at + int64(op.Seconds)*1000, Since: now}
	default:
		return TimerState{}, invalid("a timer is started, paused, reset or added to")
	}
	if err := t.settings.Save(ctx, timerKey(name), st); err != nil {
		return TimerState{}, err
	}
	t.publishLocked(o, &st)
	return st, nil
}

// autoStartGap: pages loading this close together (OBS loading two
// scenes with the same source) start it once.
const autoStartGap = 3 * time.Second

// Loaded: OBS loaded a timer overlay's page (not a reconnect, never the
// app's preview). With auto-start, a countdown starts afresh from its
// length and a stopwatch from zero.
func (t *Timers) Loaded(ctx context.Context, name string) {
	o, err := t.store.Get(ctx, name)
	if err != nil || o.Kind != KindTimer || !o.Look.TimerAutoStart || mode(o) == TimerClock {
		return
	}
	t.mu.Lock()
	now := t.now()
	if last, ok := t.autoStarted[name]; ok && now.Sub(last) < autoStartGap {
		t.mu.Unlock()
		return
	}
	t.autoStarted[name] = now
	st := TimerState{Running: true, Since: now}
	if mode(o) == TimerCountdown {
		st.BaseMS = length(o)
	}
	if err := t.settings.Save(ctx, timerKey(name), st); err != nil {
		t.mu.Unlock()
		t.log.Warn("couldn't start a timer as its page loaded", "overlay", name, "err", err)
		return
	}
	t.publishLocked(o, &st)
	t.mu.Unlock()
	t.log.Info("timer started as its page loaded", "overlay", name)
}

// Forget drops a deleted overlay's state.
func (t *Timers) Forget(ctx context.Context, name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopEndLocked(name)
	_ = t.settings.Save(ctx, timerKey(name), (*TimerState)(nil))
	t.hub.SetTimer(name, nil)
}

// Changed: an overlay's look changed. A timer's mode changing starts it
// afresh.
func (t *Timers) Changed(ctx context.Context, before, after Overlay) {
	if after.Kind != KindTimer || mode(before) == mode(after) {
		return
	}
	t.Forget(ctx, after.Name)
}

func (t *Timers) stopEndLocked(name string) {
	if e := t.ends[name]; e != nil {
		e.Stop()
		delete(t.ends, name)
	}
}

// publishLocked sends the state to the hub, and waits for a running
// countdown to end.
func (t *Timers) publishLocked(o Overlay, st *TimerState) {
	now := t.now()
	t.hub.SetTimer(o.Name, &TimerView{Running: st.Running, BaseMS: st.BaseMS, SinceMS: st.Since.UnixMilli(), NowMS: now.UnixMilli(), Ended: st.Ended})
	t.stopEndLocked(o.Name)
	if mode(o) != TimerCountdown || !st.Running || st.Ended {
		return
	}
	left := time.Duration(st.value(TimerCountdown, now)) * time.Millisecond
	name, since := o.Name, st.Since
	t.ends[name] = time.AfterFunc(left, func() { t.ended(name, since) })
}

// ended: a countdown reached zero. Unless it was changed since, it's
// saved as ended and says so.
func (t *Timers) ended(name string, since time.Time) {
	ctx := context.Background()
	t.mu.Lock()
	st, ok, err := t.load(ctx, name)
	if err != nil || !ok || !st.Running || st.Ended || !st.Since.Equal(since) {
		t.mu.Unlock()
		return
	}
	o, err := t.store.Get(ctx, name)
	if err != nil {
		t.mu.Unlock()
		return
	}
	now := t.now()
	st = TimerState{BaseMS: 0, Since: now, Ended: true}
	_ = t.settings.Save(ctx, timerKey(name), st)
	t.publishLocked(o, &st)
	delete(t.ends, name)
	t.mu.Unlock()
	if t.inbox == nil {
		return
	}
	raw, _ := json.Marshal(map[string]any{"overlay": name, "label": o.Look.TimerLabel})
	ev := events.Envelope{Type: TimerEnded, Source: "core", SourceEventID: fmt.Sprintf("timer:%s:%d", name, since.UnixMilli()), OccurredAt: now,
		DeliveryQuality: events.Confirmed, Payload: raw}
	if _, err := t.inbox.Accept(ctx, ev); err != nil {
		t.log.Warn("couldn't store a countdown's end", "overlay", name, "err", err)
	}
}

// until is the next time it's hh:mm on this computer's clock.
func until(hhmm string, now time.Time) (time.Time, error) {
	at, err := time.ParseInLocation("15:04", strings.TrimSpace(hhmm), now.Location())
	if err != nil {
		return time.Time{}, invalid("a time is like 19:00 or 7:30, on a 24-hour clock")
	}
	end := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
	if !end.After(now) {
		end = end.AddDate(0, 0, 1)
	}
	return end, nil
}
