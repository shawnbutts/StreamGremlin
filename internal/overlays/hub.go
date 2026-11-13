package overlays

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Errors from showing an alert.
var (
	// ErrNotOpen: no page has this overlay open (OBS isn't running, or the
	// source isn't in any scene OBS has loaded).
	ErrNotOpen = errors.New("overlays: the overlay isn't open in OBS")
	// ErrNoAnswer: the page was sent the alert but didn't say it showed it.
	ErrNoAnswer = errors.New("overlays: the overlay didn't say whether it showed the alert")
	// ErrBusy: too many alerts are waiting for this overlay.
	ErrBusy = errors.New("overlays: too many alerts are waiting for this overlay")
	// ErrWaitedTooLong: the alert waited its turn too long and wasn't shown.
	ErrWaitedTooLong = errors.New("overlays: the alert waited too long behind others and wasn't shown")
)

// Timing and limits.
const (
	// AnswerWait is how long a shown alert waits for a page to say it
	// started.
	AnswerWait = 5 * time.Second
	// DoneGrace is how long past its time on screen an alert may take to
	// leave before the next one goes anyway.
	DoneGrace = 5 * time.Second
	// QueueWait is the longest an alert waits behind others.
	QueueWait = 2 * time.Minute
	// MaxWaiting is how many alerts may wait for one overlay.
	MaxWaiting = 25
	// rememberShown: a retried step doesn't show its alert again for this long.
	rememberShown = time.Hour
	sendQueue     = 64
)

// Limits on what an alert carries.
const (
	MaxTitle = 100
	MaxText  = 500
)

// Alert is one alert to show.
type Alert struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Text    string `json:"text"`
	Picture string `json:"picture,omitempty"`
	// Name is the viewer's, for the initial shown in place of a missing
	// picture ("" when the event has no viewer).
	Name    string `json:"name,omitempty"`
	Seconds int    `json:"seconds"`
	// Sound plays in the overlay page as the alert appears, so OBS hears
	// it through the browser source.
	Sound *AlertSound `json:"sound,omitempty"`
}

// AlertSound is a sound from the library, at a volume (0 to 100).
type AlertSound struct {
	Name   string `json:"name"`
	Volume int    `json:"volume"`
}

// Errors about an alert's sound. The alert itself showed.
var (
	ErrSoundFailed   = errors.New("overlays: the alert showed, but its sound didn't play")
	ErrSoundNoAnswer = errors.New("overlays: the alert showed, but the overlay didn't say whether its sound played")
)

// Result is how showing an alert went.
type Result struct {
	ID       string `json:"id,omitempty"`
	Overlay  string `json:"overlay"`
	Shown    bool   `json:"shown"`
	Pages    int    `json:"pages"`              // pages that were sent it
	Previews int    `json:"previews,omitempty"` // the app's previews that were sent it
	Repeated bool   `json:"repeated,omitempty"` // a retried step: not shown again
	Test     bool   `json:"test,omitempty"`     // a test: previews only
	// SoundPlayed: a page started the alert's sound.
	SoundPlayed bool `json:"sound_played,omitempty"`
}

// conn is one open page.
type conn struct {
	overlay string
	kind    string
	preview bool // the app's preview: sent everything, never an audience
	send    chan []byte
	kick    chan struct{} // closed to disconnect it
	kicked  bool
}

// session is an alert being shown.
type session struct {
	alert   Alert
	overlay string
	until   time.Time      // when it should have left the screen
	sentTo  map[*conn]bool // pages (not previews) sent it
	errors  int
	started chan string // gets "" when a page started it, else the reason all failed
	done    chan struct{}
	once    sync.Once
	doneMu  sync.Once
	// The sound: "" when a page started it, else why every page couldn't.
	sound       chan string
	soundOnce   sync.Once
	soundErrors int
}

// queue serialises one overlay's alerts: one on screen at a time.
type queue struct {
	busy    bool
	waiting []chan struct{}
}

// Hub holds the open pages and shows alerts.
type Hub struct {
	now func() time.Time

	mu       sync.Mutex
	conns    map[string]map[*conn]bool // by overlay name
	queues   map[string]*queue
	sessions map[string]*session // by alert id
	shown    map[string]time.Time
	// giveaway is the running giveaway as giveaway overlays show it.
	giveaway *GiveawayView
	// songs is what each Now playing overlay shows, by name.
	songs map[string]SongView
	// themeOrigin is the theme server's, for wheel looks ("" when off).
	themeOrigin string
	themes      *Catalogue
}

// UseThemes says which looks are installed, for wheel overlays' hellos.
func (h *Hub) UseThemes(c *Catalogue) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.themes = c
}

// NewHub returns a Hub. now may be nil.
func NewHub(now func() time.Time) *Hub {
	if now == nil {
		now = time.Now
	}
	return &Hub{now: now, conns: map[string]map[*conn]bool{}, queues: map[string]*queue{},
		sessions: map[string]*session{}, shown: map[string]time.Time{}, songs: map[string]SongView{}}
}

// Open reports how many pages and previews have an overlay open.
func (h *Hub) Open(name string) (pages, previews int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.countLocked(name)
}

func (h *Hub) countLocked(name string) (pages, previews int) {
	for c := range h.conns[name] {
		if c.preview {
			previews++
		} else {
			pages++
		}
	}
	return pages, previews
}

// hello tells a page its overlay and look (with a wheel look's theme),
// and which version of the page this engine serves: a page left over from
// before an update reloads.
func (h *Hub) hello(o Overlay, preview bool) []byte {
	// Where a Now playing overlay's files are on this computer is the
	// engine's business, not the page's.
	o.Look.SongFile, o.Look.ArtFile, o.Look.PausedText = "", "", ""
	body := map[string]any{"protocol": 1, "overlay": o, "preview": preview, "page_version": PageVersion}
	if t := themeView(o, h.themeOrigin, h.themes); t != nil {
		body["theme"] = t
	}
	return message("hello", body)
}

// SetThemeOrigin says where the theme server is, like
// http://127.0.0.1:50123 ("" when it's off: wheels show Classic).
func (h *Hub) SetThemeOrigin(origin string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.themeOrigin = origin
}

func message(kind string, body any) []byte {
	b, _ := json.Marshal(map[string]any{"type": kind, "body": body})
	return b
}

// sendLocked queues a message for a page. A page that can't keep up is
// disconnected; it reconnects and starts afresh.
func (h *Hub) sendLocked(c *conn, b []byte) {
	select {
	case c.send <- b:
	default:
		h.kickLocked(c)
	}
}

func (h *Hub) kickLocked(c *conn) {
	if !c.kicked {
		c.kicked = true
		close(c.kick)
	}
}

// join registers a page and sends it the overlay and any alert on screen.
func (h *Hub) join(o Overlay, preview bool) *conn {
	c := &conn{overlay: o.Name, kind: o.Kind, preview: preview, send: make(chan []byte, sendQueue), kick: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[o.Name] == nil {
		h.conns[o.Name] = map[*conn]bool{}
	}
	h.conns[o.Name][c] = true
	h.sendLocked(c, h.hello(o, preview))
	if v := h.giveaway; v != nil && giveawayFor(c, v.Practice) {
		h.sendLocked(c, message("giveaway", v))
	}
	if o.Kind == KindSong {
		h.sendLocked(c, message("song", h.songs[o.Name]))
	}
	// An alert still on screen is sent with the time it has left. Pages
	// skip alerts they've already played.
	now := h.now()
	for _, s := range h.sessions {
		if s.overlay != o.Name || !now.Before(s.until) {
			continue
		}
		a := s.alert
		if !preview {
			s.sentTo[c] = true
		}
		h.sendLocked(c, message("alert", map[string]any{"alert": a, "ms_left": s.until.Sub(now).Milliseconds()}))
	}
	return c
}

// leave unregisters a page.
func (h *Hub) leave(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns[c.overlay], c)
	if len(h.conns[c.overlay]) == 0 {
		delete(h.conns, c.overlay)
	}
	h.kickLocked(c)
}

// ack records a page's report on an alert: started, done or error.
func (h *Hub) ack(c *conn, id, status, detail string) {
	if c.preview {
		return // a preview is never an audience
	}
	h.mu.Lock()
	s := h.sessions[id]
	if s == nil || !s.sentTo[c] {
		h.mu.Unlock()
		return
	}
	switch status {
	case "started":
		s.once.Do(func() { s.started <- "" })
	case "sound_started":
		s.soundOnce.Do(func() { s.sound <- "" })
	case "sound_error":
		s.soundErrors++
		if s.soundErrors >= len(s.sentTo) {
			if detail == "" {
				detail = "it couldn't be played"
			}
			if len(detail) > 200 {
				detail = detail[:200]
			}
			s.soundOnce.Do(func() { s.sound <- detail })
		}
	case "done":
		s.doneMu.Do(func() { close(s.done) })
	case "error":
		s.errors++
		if s.errors >= len(s.sentTo) {
			if detail == "" {
				detail = "it couldn't be shown"
			}
			if len(detail) > 200 {
				detail = detail[:200]
			}
			s.once.Do(func() { s.started <- detail })
			s.doneMu.Do(func() { close(s.done) })
		}
	}
	h.mu.Unlock()
}

// Changed sends an overlay's new look to its open pages.
func (h *Hub) Changed(o Overlay) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns[o.Name] {
		h.sendLocked(c, h.hello(o, c.preview))
	}
}

// PreviewLook sends a look to an overlay's previews only, to try before
// saving. OBS keeps the saved look.
func (h *Hub) PreviewLook(o Overlay) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for c := range h.conns[o.Name] {
		if c.preview {
			n++
			h.sendLocked(c, h.hello(o, true))
		}
	}
	return n
}

// CloseAll closes every page, as the engine stops.
func (h *Hub) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, cs := range h.conns {
		for c := range cs {
			h.kickLocked(c)
		}
		delete(h.conns, name)
	}
}

// Disconnect closes every page of an overlay: its link changed or it was
// deleted, so the old link mustn't keep showing anything.
func (h *Hub) Disconnect(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns[name] {
		h.kickLocked(c)
	}
	delete(h.conns, name)
}

// Preview shows an alert in the app's previews of an overlay only, for a
// test. It doesn't wait, and OBS never sees it.
func (h *Hub) Preview(name string, a Alert) Result {
	if a.ID == "" {
		a.ID = events.NewID()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	res := Result{ID: a.ID, Overlay: name, Test: true}
	for c := range h.conns[name] {
		if c.preview {
			res.Previews++
			h.sendLocked(c, message("alert", map[string]any{"alert": a, "ms_left": int64(a.Seconds) * 1000}))
		}
	}
	return res
}

// Show shows an alert on an overlay, after any alerts before it, and waits
// until a page says it started. key, when set, is the step's idempotency
// key: the same key never shows twice.
func (h *Hub) Show(ctx context.Context, name string, a Alert, key string) (Result, error) {
	if a.ID == "" {
		a.ID = events.NewID()
	}
	res := Result{ID: a.ID, Overlay: name}
	h.mu.Lock()
	now := h.now()
	for k, at := range h.shown {
		if now.Sub(at) > rememberShown {
			delete(h.shown, k)
		}
	}
	if _, seen := h.shown[key]; key != "" && seen {
		h.mu.Unlock()
		res.ID, res.Repeated = "", true
		return res, nil
	}
	if pages, _ := h.countLocked(name); pages == 0 {
		h.mu.Unlock()
		return res, ErrNotOpen
	}
	q := h.queues[name]
	if q == nil {
		q = &queue{}
		h.queues[name] = q
	}
	if q.busy {
		if len(q.waiting) >= MaxWaiting {
			h.mu.Unlock()
			return res, ErrBusy
		}
		turn := make(chan struct{})
		q.waiting = append(q.waiting, turn)
		h.mu.Unlock()
		wait := time.NewTimer(QueueWait)
		var waitErr error
		select {
		case <-turn:
		case <-wait.C:
			waitErr = ErrWaitedTooLong
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
		wait.Stop()
		if waitErr != nil {
			h.mu.Lock()
			if !h.dropLocked(q, turn) {
				// The turn came just as we gave up: pass it on.
				h.releaseLocked(name)
			}
			h.mu.Unlock()
			return res, waitErr
		}
		h.mu.Lock()
	} else {
		q.busy = true
	}
	// Our turn. The pages may have closed while we waited.
	pages, previews := h.countLocked(name)
	if pages == 0 {
		h.releaseLocked(name)
		h.mu.Unlock()
		return res, ErrNotOpen
	}
	s := &session{alert: a, overlay: name, until: h.now().Add(time.Duration(a.Seconds) * time.Second),
		sentTo: map[*conn]bool{}, started: make(chan string, 1), done: make(chan struct{}), sound: make(chan string, 1)}
	h.sessions[a.ID] = s
	msg := message("alert", map[string]any{"alert": a, "ms_left": int64(a.Seconds) * 1000})
	for c := range h.conns[name] {
		if !c.preview {
			s.sentTo[c] = true
		}
		h.sendLocked(c, msg)
	}
	res.Pages, res.Previews = pages, previews
	if key != "" {
		h.shown[key] = h.now() // sent: a retry mustn't show it again, whatever the answer
	}
	h.mu.Unlock()

	answer := time.NewTimer(AnswerWait)
	defer answer.Stop()
	var reason string
	var err error
	select {
	case reason = <-s.started:
		if reason != "" {
			err = fmt.Errorf("overlays: %s couldn't show the alert: %s", name, reason)
		}
	case <-answer.C:
		err = ErrNoAnswer
	case <-ctx.Done():
		err = ErrNoAnswer // it was sent; whether it showed isn't known
	}
	if reason != "" {
		h.mu.Lock()
		delete(h.shown, key) // nothing showed: a retry may try again
		h.mu.Unlock()
	}
	// The next alert waits until this one leaves the screen, or its time
	// plus a grace when no page says so.
	go h.finish(s, time.Duration(a.Seconds)*time.Second+DoneGrace, err != nil && reason == "")
	if err != nil {
		return res, err
	}
	res.Shown = true
	if a.Sound == nil {
		return res, nil
	}
	// The alert showed; now whether its sound started in the page.
	wait := time.NewTimer(AnswerWait)
	defer wait.Stop()
	select {
	case why := <-s.sound:
		if why != "" {
			return res, fmt.Errorf("%w: %s", ErrSoundFailed, why)
		}
		res.SoundPlayed = true
		return res, nil
	case <-wait.C:
	case <-ctx.Done():
	}
	return res, ErrSoundNoAnswer
}

// finish waits for an alert to leave the screen, then lets the next go.
func (h *Hub) finish(s *session, limit time.Duration, unanswered bool) {
	if unanswered {
		// Nobody answered: don't hold the queue for the whole alert.
		limit = DoneGrace
	}
	t := time.NewTimer(limit)
	select {
	case <-s.done:
	case <-t.C:
	}
	t.Stop()
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, s.alert.ID)
	h.releaseLocked(s.overlay)
}

// releaseLocked hands the overlay's turn to the next waiting alert.
func (h *Hub) releaseLocked(name string) {
	q := h.queues[name]
	if q == nil {
		return
	}
	if len(q.waiting) > 0 {
		next := q.waiting[0]
		q.waiting = q.waiting[1:]
		close(next) // busy stays true: the turn passes on
		return
	}
	q.busy = false
}

// dropLocked removes a waiting turn; false if it was already handed over.
func (h *Hub) dropLocked(q *queue, turn chan struct{}) bool {
	for i, t := range q.waiting {
		if t == turn {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			return true
		}
	}
	return false
}
