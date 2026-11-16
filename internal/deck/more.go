package deck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/ads"
	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/overlays"
	"code.streamgremlin.com/streamgremlin/internal/twitch"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// Run a workflow, OBS scene, countdown and ad keys (D-124).

// Source is where the Stream Deck's events come from.
const Source = "stream_deck"

// AdsEvery is how often the ad schedule is read from Twitch, at most; keys
// count down from it in between.
const AdsEvery = 30 * time.Second

// Inbox records a run's event.
type Inbox interface {
	Accept(ctx context.Context, env events.Envelope) (events.Result, error)
	BySource(ctx context.Context, source, id string) (events.Record, error)
}

// Scener is what scene keys need of OBS.
type Scener interface {
	Scenes(ctx context.Context) (current string, scenes []string, err error)
	SwitchScene(ctx context.Context, scene, by string) (previous string, err error)
}

// OverlayStore finds a clock overlay.
type OverlayStore interface {
	Get(ctx context.Context, name string) (overlays.Overlay, error)
}

// Clocks reads and changes clock overlays' countdowns and stopwatches.
type Clocks interface {
	State(ctx context.Context, name string) (*overlays.TimerState, error)
	Do(ctx context.Context, name string, op overlays.TimerOp) (overlays.TimerState, error)
}

// AdsSource reads Twitch's ad schedule and snoozes the next ad.
type AdsSource interface {
	Schedule(ctx context.Context) (twitch.AdSchedule, error)
	Snooze(ctx context.Context, opKey, by string) (ads.Request, error)
}

// RunState is a workflow a key may run: On is whether it's on (a run
// starts only then); NeedsViewer says its trigger brings a viewer or
// their words, which a run from the Stream Deck doesn't.
type RunState struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	On          bool   `json:"on"`
	Gone        bool   `json:"gone,omitempty"`
	NeedsViewer bool   `json:"needs_viewer,omitempty"`
}

// Scenes is OBS's scenes and the one on air; Known is false while OBS
// can't say.
type Scenes struct {
	Known   bool     `json:"known"`
	Current string   `json:"current"`
	List    []string `json:"list"`
}

// CountdownState is a clock overlay's countdown or stopwatch: MS is the
// time left (a countdown) or counted (a stopwatch) at At.
type CountdownState struct {
	Name    string    `json:"name"`
	Label   string    `json:"label"`
	Mode    string    `json:"mode"`
	Running bool      `json:"running"`
	MS      int64     `json:"ms"`
	At      time.Time `json:"at"`
	Ended   bool      `json:"ended,omitempty"`
	Gone    bool      `json:"gone,omitempty"`
}

// Ads is Twitch's ad schedule as last read: Known is false when Twitch
// couldn't say (Detail says why).
type Ads struct {
	Known    bool       `json:"known"`
	NextAdAt *time.Time `json:"next_ad_at,omitempty"`
	Snoozes  int        `json:"snoozes"`
	ReadAt   time.Time  `json:"read_at"`
	Detail   string     `json:"detail,omitempty"`
}

// more fills the run, scene, countdown and ad keys' parts of v.
func (s *Service) more(ctx context.Context, set Settings, v *View) error {
	for _, id := range set.Runs {
		r, err := s.runState(ctx, id)
		if err != nil {
			return err
		}
		v.Runs = append(v.Runs, r)
	}
	if set.Scenes && s.o.Scener != nil {
		sc := &Scenes{List: []string{}}
		if cur, list, err := s.o.Scener.Scenes(ctx); err == nil {
			sc.Known, sc.Current, sc.List = true, cur, list
		}
		v.Scenes = sc
	}
	for _, name := range set.Countdowns {
		c, err := s.countdown(ctx, name)
		if err != nil {
			return err
		}
		v.Countdowns = append(v.Countdowns, c)
	}
	if set.Ads && s.o.AdsSource != nil {
		v.Ads = s.ads(ctx, false)
	}
	return nil
}

func (s *Service) runState(ctx context.Context, id string) (RunState, error) {
	w, err := s.o.Workflows.Get(ctx, id)
	if errors.Is(err, workflow.ErrNotFound) {
		return RunState{ID: id, Label: "Workflow gone", Gone: true}, nil
	}
	if err != nil {
		return RunState{}, err
	}
	r := RunState{ID: id, Label: w.Name, On: w.Running}
	for _, t := range w.Triggers {
		if bringsViewer(t.Type) {
			r.NeedsViewer = true
		}
	}
	return r, nil
}

// bringsViewer says whether an event type carries a viewer or their words.
func bringsViewer(typ string) bool {
	k, ok := events.KindOf(typ)
	if !ok {
		return false
	}
	if k.Sensitive {
		return true
	}
	env, err := (events.Simulation{Type: typ, RequestID: "deck"}).Build()
	return err == nil && env.Actor != nil
}

// Run runs a chosen workflow now, as from client, once per request id: a
// run event that only that workflow starts from. It answers whether the
// workflow is on (so a run starts) and whether this request was a repeat.
func (s *Service) Run(ctx context.Context, id, requestID, client string) (RunState, bool, error) {
	if requestID == "" || len(requestID) > maxID {
		return RunState{}, false, invalid("a request id is 1 to %d characters", maxID)
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return RunState{}, false, err
	}
	if !has(set.Runs, id) {
		return RunState{}, false, ErrNotAllowed
	}
	st, err := s.runState(ctx, id)
	if err != nil {
		return st, false, err
	}
	if st.Gone {
		return st, false, ErrGone
	}
	sourceID := client + ":" + requestID
	if _, err := s.o.Inbox.BySource(ctx, Source, sourceID); err == nil {
		return st, true, nil
	} else if !errors.Is(err, events.ErrNotFound) {
		return st, false, err
	}
	raw, err := json.Marshal(map[string]any{"workflow_id": id, "client_id": client})
	if err != nil {
		return st, false, err
	}
	res, err := s.o.Inbox.Accept(ctx, events.Envelope{
		Type: events.DeckRun, Source: Source, SourceEventID: sourceID,
		OccurredAt: s.o.Now(), Payload: raw, DeliveryQuality: events.Confirmed,
	})
	if errors.Is(err, events.ErrConflict) {
		return st, true, nil // the same request at the same moment, stored first by the other
	}
	if err != nil {
		return st, false, fmt.Errorf("deck: record a run of %s: %w", id, err)
	}
	if !res.Duplicate {
		s.o.Logger.Info("workflow run from the stream deck", "workflow", id, "on", st.On, "client", client)
	}
	return st, res.Duplicate, nil
}

// Scene switches OBS to a scene, as from the Stream Deck.
func (s *Service) Scene(ctx context.Context, scene, client string) (Scenes, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return Scenes{}, err
	}
	if !set.Scenes || s.o.Scener == nil {
		return Scenes{}, ErrNotAllowed
	}
	if _, err := s.o.Scener.SwitchScene(ctx, scene, Source); err != nil {
		return Scenes{}, err
	}
	s.o.Logger.Info("scene switched from the stream deck", "scene", scene, "client", client)
	// Switched; if OBS can't list its scenes now, what's on air is unknown.
	out := Scenes{List: []string{}}
	if cur, list, lerr := s.o.Scener.Scenes(ctx); lerr == nil {
		out = Scenes{Known: true, Current: cur, List: list}
	}
	return out, nil
}

func (s *Service) countdown(ctx context.Context, name string) (CountdownState, error) {
	c := CountdownState{Name: name, Label: name, At: s.o.Now()}
	if s.o.Overlays == nil || s.o.Clocks == nil {
		c.Gone = true
		return c, nil
	}
	o, err := s.o.Overlays.Get(ctx, name)
	if errors.Is(err, overlays.ErrNotFound) {
		c.Gone = true
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.Mode = o.Look.TimerMode
	if o.Kind != overlays.KindTimer || (c.Mode != overlays.TimerCountdown && c.Mode != overlays.TimerStopwatch) {
		c.Gone = true // a clock now, or not a timer at all
		return c, nil
	}
	if o.Look.TimerLabel != "" {
		c.Label = o.Look.TimerLabel
	}
	st, err := s.o.Clocks.State(ctx, name)
	if err != nil {
		return c, err
	}
	if st == nil {
		if c.Mode == overlays.TimerCountdown {
			secs := o.Look.TimerLength
			if secs <= 0 {
				secs = overlays.DefaultTimerSeconds
			}
			c.MS = int64(secs) * 1000
		}
		return c, nil
	}
	c.Running, c.Ended, c.MS = st.Running, st.Ended, st.BaseMS
	if st.Running {
		d := c.At.Sub(st.Since).Milliseconds()
		if c.Mode == overlays.TimerCountdown {
			c.MS = max(0, st.BaseMS-d)
		} else {
			c.MS = st.BaseMS + d
		}
	}
	return c, nil
}

// Countdown starts, pauses or resets a chosen countdown or stopwatch.
// Starting one that's running, or pausing one that's paused, changes
// nothing, so asking again is harmless.
func (s *Service) Countdown(ctx context.Context, name, action, client string) (CountdownState, error) {
	switch action {
	case "start", "pause", "reset":
	default:
		return CountdownState{}, invalid("an action is start, pause or reset")
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return CountdownState{}, err
	}
	if !has(set.Countdowns, name) {
		return CountdownState{}, ErrNotAllowed
	}
	c, err := s.countdown(ctx, name)
	if err != nil {
		return c, err
	}
	if c.Gone {
		return c, ErrGone
	}
	if (action == "start" && c.Running) || (action == "pause" && !c.Running) {
		return c, nil
	}
	if _, err := s.o.Clocks.Do(ctx, name, overlays.TimerOp{Action: action}); err != nil {
		return c, err
	}
	s.o.Logger.Info("countdown from the stream deck", "overlay", name, "action", action, "client", client)
	return s.countdown(ctx, name)
}

// ads is the schedule, read from Twitch at most every AdsEvery unless
// fresh is asked for.
func (s *Service) ads(ctx context.Context, fresh bool) *Ads {
	s.adMu.Lock()
	defer s.adMu.Unlock()
	now := s.o.Now()
	if !fresh && s.adCache != nil && now.Sub(s.adReadAt) < AdsEvery {
		a := *s.adCache
		return &a
	}
	a := &Ads{ReadAt: now}
	sc, err := s.o.AdsSource.Schedule(ctx)
	if err != nil {
		a.Detail = err.Error()
	} else {
		a.Known, a.NextAdAt, a.Snoozes = true, sc.NextAdAt, sc.SnoozeCount
	}
	s.adCache, s.adReadAt = a, now
	out := *a
	return &out
}

// Snooze pushes Twitch's next ad back, once per request id.
func (s *Service) Snooze(ctx context.Context, requestID, client string) (Ads, error) {
	if requestID == "" || len(requestID) > maxID {
		return Ads{}, invalid("a request id is 1 to %d characters", maxID)
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return Ads{}, err
	}
	if !set.Ads || s.o.AdsSource == nil {
		return Ads{}, ErrNotAllowed
	}
	if _, err := s.o.AdsSource.Snooze(ctx, "deck:"+client+":"+requestID, "the Stream Deck"); err != nil {
		return Ads{}, err
	}
	s.o.Logger.Info("ad snoozed from the stream deck", "client", client)
	return *s.ads(ctx, true), nil
}
