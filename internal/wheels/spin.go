package wheels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

// Spins (docs/adr/0030-wheels.md): drawn and recorded at once, then shown on
// wheel overlays one after another, then settled. The result is announced
// (the settled event, and the step's output) only when the wheel stops, so
// nothing gives it away. With no wheel overlay open in OBS, a spin settles
// at once.

// Display is the overlay hub, as wheels use it.
type Display interface {
	WheelSpin(v SpinView)
	WheelPages() int
}

// SpinView is what a wheel overlay is sent: the wedges and where to stop.
// Never more than a viewer could see.
type SpinView struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Choices []Choice `json:"choices"`
	Winner  int      `json:"winner"`
	Draw    int      `json:"draw"`
	Seconds int      `json:"seconds"`
}

// Show timing: after the wheel stops, the result shows this long.
const (
	ResultTime = 4 * time.Second
	maxWaiting = 20
)

// Spin is one spin, as recorded.
type Spin struct {
	ID           string     `json:"id"`
	Wheel        string     `json:"wheel"`
	Title        string     `json:"title"`
	WheelVersion int        `json:"wheel_version"`
	Choices      []Choice   `json:"choices"`
	Total        int        `json:"total"`
	Draw         int        `json:"draw"`
	Winner       int        `json:"winner"`
	Label        string     `json:"label"`
	StartedBy    string     `json:"started_by"`
	SpunAt       time.Time  `json:"spun_at"`
	Seconds      int        `json:"seconds"`
	SettledAt    *time.Time `json:"settled_at,omitempty"`
	// Display: shown (on a wheel overlay), no_audience (none was open), or
	// recovered (StreamGremlin stopped before it settled).
	Display string `json:"display,omitempty"`
}

// Options configure a Service.
type Options struct {
	DB      *storage.DB
	Inbox   *events.Inbox
	Display Display
	Logger  *slog.Logger
	Now     func() time.Time
	// Sleep waits; tests make it quick.
	Sleep func(ctx context.Context, d time.Duration)
}

// Service keeps wheels and spins them.
type Service struct {
	db      *storage.DB
	inbox   *events.Inbox
	display Display
	log     *slog.Logger
	now     func() time.Time
	sleep   func(ctx context.Context, d time.Duration)

	mu      sync.Mutex
	settled map[string]chan struct{} // closed when the spin settles
	actors  map[string]*events.Actor // who started a spin, for its settled event
	queue   chan string
	stop    context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

// New returns a Service. Call Start.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}
	return &Service{db: o.DB, inbox: o.Inbox, display: o.Display, log: o.Logger.With("component", "wheels"), now: o.Now, sleep: o.Sleep,
		settled: map[string]chan struct{}{}, actors: map[string]*events.Actor{}, queue: make(chan string, maxWaiting)}
}

// Start settles spins a stop left unsettled (recovered: the result stands),
// and starts showing spins.
func (s *Service) Start(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM wheel_spins WHERE settled_at IS NULL ORDER BY spun_at")
	if err != nil {
		return fmt.Errorf("wheels: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	_ = rows.Close()
	for _, id := range ids {
		s.settle(ctx, id, "recovered")
	}
	ctx, s.stop = context.WithCancel(context.WithoutCancel(ctx))
	s.done = make(chan struct{})
	go s.run(ctx)
	return nil
}

// Close stops showing spins. A spin waiting is settled when StreamGremlin
// starts again. Closing twice is harmless.
func (s *Service) Close() {
	s.once.Do(func() {
		if s.stop != nil {
			s.stop()
			<-s.done
		}
	})
}

func (s *Service) run(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			s.show(ctx, id)
		}
	}
}

// show spins it on wheel overlays, waits for the wheel to stop, and
// settles it. With none open in OBS, it settles at once.
func (s *Service) show(ctx context.Context, id string) {
	sp, err := s.GetSpin(ctx, id)
	if err != nil {
		s.log.Error("couldn't read a spin", "spin", id, "err", err)
		return
	}
	display := "no_audience"
	if s.display != nil && s.display.WheelPages() > 0 {
		display = "shown"
		s.display.WheelSpin(SpinView{ID: sp.ID, Title: sp.Title, Choices: sp.Choices, Winner: sp.Winner, Draw: sp.Draw, Seconds: sp.Seconds})
		s.sleep(ctx, time.Duration(sp.Seconds)*time.Second+500*time.Millisecond)
		if ctx.Err() != nil {
			return // settled as recovered when StreamGremlin starts again
		}
	} else if s.display != nil {
		// The app's previews still see it.
		s.display.WheelSpin(SpinView{ID: sp.ID, Title: sp.Title, Choices: sp.Choices, Winner: sp.Winner, Draw: sp.Draw, Seconds: sp.Seconds})
	}
	s.settle(ctx, id, display)
}

// settle records how it was shown and announces the result, once.
func (s *Service) settle(ctx context.Context, id, display string) {
	res, err := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE wheel_spins SET settled_at = ?, display = ? WHERE id = ? AND settled_at IS NULL`,
		ts(s.now()), display, id)
	if err != nil {
		s.log.Error("couldn't settle a spin", "spin", id, "err", err)
		return
	}
	s.mu.Lock()
	actor := s.actors[id]
	delete(s.actors, id)
	s.mu.Unlock()
	if n, _ := res.RowsAffected(); n > 0 {
		if sp, err := s.GetSpin(ctx, id); err == nil {
			s.emitActor(ctx, sp, "wheels.spin.settled", "settled", actor, map[string]any{"label": sp.Label, "choice": sp.Winner + 1,
				"display": display, "draw": sp.Draw, "total": sp.Total})
			s.log.Info("spin settled", "spin", id, "wheel", sp.Wheel, "display", display)
		}
	}
	s.mu.Lock()
	if ch, ok := s.settled[id]; ok {
		close(ch)
		delete(s.settled, id)
	}
	s.mu.Unlock()
}

// SpinRequest asks for a spin. Key makes it safe to repeat: the same key
// returns the first spin, never a second one.
type SpinRequest struct {
	Wheel string
	Key   string
	By    string
	Actor *events.Actor
}

// Spin draws a spin with crypto/rand, records it, and queues it to show.
// It returns at once; Wait waits for the wheel to stop.
func (s *Service) Spin(ctx context.Context, req SpinRequest) (Spin, bool, error) {
	if req.Key == "" {
		req.Key = events.NewID()
	}
	if sp, err := s.byKey(ctx, req.Key); err == nil {
		if sp.Wheel != req.Wheel {
			return Spin{}, false, invalid("that request id was used for another wheel")
		}
		return sp, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Spin{}, false, err
	}
	w, err := s.Get(ctx, req.Wheel)
	if err != nil {
		return Spin{}, false, err
	}
	total := Total(w.Choices)
	d, err := draw(total)
	if err != nil {
		return Spin{}, false, err
	}
	win := Winner(w.Choices, d)
	sp := Spin{ID: events.NewID(), Wheel: w.Name, Title: w.Title, WheelVersion: w.Version, Choices: w.Choices, Total: total, Draw: d,
		Winner: win, Label: w.Choices[win].Label, StartedBy: req.By, SpunAt: s.now(), Seconds: w.SpinSeconds}
	if sp.StartedBy == "" {
		sp.StartedBy = "the app"
	}
	raw, _ := json.Marshal(sp.Choices)
	s.mu.Lock()
	if len(s.queue) >= cap(s.queue) {
		s.mu.Unlock()
		return Spin{}, false, fmt.Errorf("%w: %d at most", ErrBusy, maxWaiting)
	}
	ch := make(chan struct{})
	s.settled[sp.ID] = ch
	s.actors[sp.ID] = req.Actor
	s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, `INSERT INTO wheel_spins (id, op_key, wheel, title, wheel_version, choices, total, draw, winner, label,
		started_by, spun_at, seconds) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, sp.ID, req.Key, sp.Wheel, sp.Title, sp.WheelVersion,
		string(raw), sp.Total, sp.Draw, sp.Winner, sp.Label, sp.StartedBy, ts(sp.SpunAt), sp.Seconds)
	if err != nil {
		s.mu.Lock()
		delete(s.settled, sp.ID)
		delete(s.actors, sp.ID)
		s.mu.Unlock()
		// The same key raced in twice: the first one stands.
		if first, ferr := s.byKey(ctx, req.Key); ferr == nil {
			return first, true, nil
		}
		return Spin{}, false, fmt.Errorf("wheels: recording a spin: %w", err)
	}
	// The started event says a spin began, never its result.
	s.emitActor(ctx, sp, "wheels.spin.started", "started", req.Actor, nil)
	s.log.Info("wheel spun", "spin", sp.ID, "wheel", sp.Wheel)
	select {
	case s.queue <- sp.ID:
	default:
		s.settle(ctx, sp.ID, "no_audience") // can't happen: room was checked
	}
	return sp, false, nil
}

// Wait waits until a spin settles, and returns it.
func (s *Service) Wait(ctx context.Context, id string) (Spin, error) {
	s.mu.Lock()
	ch, ok := s.settled[id]
	s.mu.Unlock()
	if ok {
		select {
		case <-ch:
		case <-ctx.Done():
			return Spin{}, ctx.Err()
		}
	}
	return s.GetSpin(ctx, id)
}

const spinColumns = "id, wheel, title, wheel_version, choices, total, draw, winner, label, started_by, spun_at, seconds, settled_at, display"

func scanSpin(sc scanner) (Spin, error) {
	var sp Spin
	var choices, spun string
	var settled sql.NullString
	if err := sc.Scan(&sp.ID, &sp.Wheel, &sp.Title, &sp.WheelVersion, &choices, &sp.Total, &sp.Draw, &sp.Winner, &sp.Label, &sp.StartedBy,
		&spun, &sp.Seconds, &settled, &sp.Display); err != nil {
		return sp, err
	}
	if err := json.Unmarshal([]byte(choices), &sp.Choices); err != nil {
		return sp, fmt.Errorf("wheels: spin %s's choices: %w", sp.ID, err)
	}
	sp.SpunAt, _ = storage.ParseTimestamp(spun)
	if settled.Valid {
		t, _ := storage.ParseTimestamp(settled.String)
		sp.SettledAt = &t
	}
	return sp, nil
}

func (s *Service) byKey(ctx context.Context, key string) (Spin, error) {
	return scanSpin(s.db.QueryRowContext(ctx, "SELECT "+spinColumns+" FROM wheel_spins WHERE op_key = ?", key))
}

// GetSpin returns one spin.
func (s *Service) GetSpin(ctx context.Context, id string) (Spin, error) {
	sp, err := scanSpin(s.db.QueryRowContext(ctx, "SELECT "+spinColumns+" FROM wheel_spins WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return sp, fmt.Errorf("%w: no spin %q", ErrNotFound, id)
	}
	return sp, err
}

// Spins lists the newest spins, of one wheel or all ("").
func (s *Service) Spins(ctx context.Context, wheel string, limit int) ([]Spin, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+spinColumns+" FROM wheel_spins WHERE ? = '' OR wheel = ? ORDER BY spun_at DESC, rowid DESC LIMIT ?",
		wheel, wheel, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Spin{}
	for rows.Next() {
		sp, err := scanSpin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// Verify checks a spin's record: the drawn number lands on the recorded
// choice of the wheel as it was. It returns what doesn't hold, or nothing.
// It can't replay the randomness: crypto/rand has no public seed.
func Verify(sp Spin) []string {
	var bad []string
	total := Total(sp.Choices)
	switch {
	case total != sp.Total:
		bad = append(bad, fmt.Sprintf("the wheel's weights add up to %d, not the %d recorded", total, sp.Total))
	case sp.Draw < 0 || sp.Draw >= total:
		bad = append(bad, fmt.Sprintf("the number drawn, %d, isn't below %d", sp.Draw, total))
	default:
		w := Winner(sp.Choices, sp.Draw)
		if w != sp.Winner || sp.Choices[w].Label != sp.Label {
			bad = append(bad, fmt.Sprintf("number %d lands on choice %d (%s), not %d (%s)", sp.Draw, w+1, sp.Choices[w].Label, sp.Winner+1, sp.Label))
		}
	}
	return bad
}

func (s *Service) emitActor(ctx context.Context, sp Spin, typ, key string, actor *events.Actor, extra map[string]any) {
	if s.inbox == nil {
		return
	}
	p := map[string]any{"spin_id": sp.ID, "wheel": sp.Wheel, "title": sp.Title, "started_by": sp.StartedBy}
	for k, v := range extra {
		p[k] = v
	}
	raw, _ := json.Marshal(p)
	ev := events.Envelope{Type: typ, Source: "core", SourceEventID: "spin:" + sp.ID + ":" + key, OccurredAt: s.now(), Actor: actor,
		DeliveryQuality: events.Confirmed, Payload: raw}
	if _, err := s.inbox.Accept(context.WithoutCancel(ctx), ev); err != nil {
		s.log.Warn("couldn't store a wheel event", "type", typ, "err", err)
	}
}
