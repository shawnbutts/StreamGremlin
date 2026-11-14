// Package links joins a Twitch viewer and a YouTube viewer who are one
// person (M6 slice 6, D-101, docs/adr/0048-viewer-links.md). Linked
// viewers share one points balance: the Twitch viewer's, which the ledger
// reads through viewer_links. A link is proved in chat: "!link NAME" on one
// platform, naming the person's account on the other, gives a code; that
// account, and only it, types "!link CODE" within ten minutes. The
// streamer can also link and unlink from the app. Unlinking leaves the
// shared balance with the Twitch viewer.
package links

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/points"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/viewers"
)

// Limits.
const (
	CodeTTL    = 10 * time.Minute
	codeDigits = 6
	maxPending = 1000
	// MaxTries is how many wrong codes one account may type in CodeTTL.
	MaxTries = 5
)

// Errors. Their words are said in chat, so they're written for viewers.
var (
	ErrInvalid  = errors.New("links: invalid")
	ErrNotFound = errors.New("links: not linked")
	ErrLinked   = errors.New("links: already linked")
	ErrCode     = errors.New("links: that code isn't right")
)

// Platforms.
const (
	Twitch  = "twitch"
	YouTube = "youtube"
)

// Link is a Twitch viewer and a YouTube viewer joined.
type Link struct {
	ID         string         `json:"id"`
	Twitch     viewers.Viewer `json:"twitch"`
	YouTube    viewers.Viewer `json:"youtube"`
	LinkedAt   time.Time      `json:"linked_at"`
	LinkedBy   string         `json:"linked_by"`
	UnlinkedAt *time.Time     `json:"unlinked_at,omitempty"`
	UnlinkedBy string         `json:"unlinked_by,omitempty"`
}

// Asked is a code waiting for the other account.
type Asked struct {
	Code string `json:"code"`
	// Platform and Login are the account that must type it.
	Platform string    `json:"platform"`
	Login    string    `json:"login"`
	Expires  time.Time `json:"expires"`
}

type pending struct {
	Asked
	viewer string // who asked
}

// Service links viewers.
type Service struct {
	db     *storage.DB
	vs     *viewers.Service
	ledger *points.Ledger
	log    *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	pending map[string]*pending // by code
	tries   map[string][]time.Time
}

// New returns a Service.
func New(db *storage.DB, vs *viewers.Service, ledger *points.Ledger, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, vs: vs, ledger: ledger, log: log.With("component", "links"), now: time.Now,
		pending: map[string]*pending{}, tries: map[string][]time.Time{}}
}

func invalid(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(f, a...)) }

var (
	codePattern = regexp.MustCompile(`^[0-9]{6}$`)
	namePattern = regexp.MustCompile(`^[a-z0-9_.\-]{1,64}$`)
)

// IsCode reports whether text is a link code.
func IsCode(text string) bool { return codePattern.MatchString(strings.TrimSpace(text)) }

func other(platform string) string {
	if platform == Twitch {
		return YouTube
	}
	return Twitch
}

// Where says a platform in words.
func Where(platform string) string {
	if platform == YouTube {
		return "YouTube"
	}
	return "Twitch"
}

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", codeDigits, n.Int64()), nil
}

func (s *Service) expireLocked(now time.Time) {
	for c, p := range s.pending {
		if !now.Before(p.Expires) {
			delete(s.pending, c)
		}
	}
	for k, ts := range s.tries {
		kept := ts[:0]
		for _, t := range ts {
			if now.Sub(t) < CodeTTL {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(s.tries, k)
		} else {
			s.tries[k] = kept
		}
	}
}

// Ask starts a link from a viewer in chat, naming their account on the
// other platform: a Twitch login, or a YouTube @handle. The code it gives
// works once, within CodeTTL, typed by that account only, so a code seen
// in chat is no use to anyone else. Asking again replaces the code.
func (s *Service) Ask(ctx context.Context, a events.Actor, name string) (Asked, error) {
	if a.Platform != Twitch && a.Platform != YouTube {
		return Asked{}, invalid("linking is for Twitch and YouTube chat")
	}
	login := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "@"))
	if !namePattern.MatchString(login) {
		if a.Platform == Twitch {
			return Asked{}, invalid("say your YouTube handle, like !link @yourname")
		}
		return Asked{}, invalid("say your Twitch name, like !link yourname")
	}
	v, err := s.vs.Ensure(ctx, a)
	if err != nil {
		return Asked{}, err
	}
	if l, err := s.Of(ctx, v.ID); err == nil {
		return Asked{}, fmt.Errorf("%w: you're already linked to %s on %s; the streamer can unlink you", ErrLinked,
			nameOf(l.partner(v.ID)), Where(other(a.Platform)))
	} else if !errors.Is(err, ErrNotFound) {
		return Asked{}, err
	}
	code, err := newCode()
	if err != nil {
		return Asked{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.expireLocked(now)
	for c, p := range s.pending {
		if p.viewer == v.ID {
			delete(s.pending, c)
		}
	}
	for s.pending[code] != nil {
		if code, err = newCode(); err != nil {
			return Asked{}, err
		}
	}
	if len(s.pending) >= maxPending {
		return Asked{}, invalid("too many links are waiting just now; try again in a few minutes")
	}
	p := &pending{Asked: Asked{Code: code, Platform: other(a.Platform), Login: login, Expires: now.Add(CodeTTL)}, viewer: v.ID}
	s.pending[code] = p
	s.log.Info("link asked", "viewer", v.ID, "for", p.Platform)
	return p.Asked, nil
}

// Confirm finishes a link: the account named when it was asked types the
// code on its platform.
func (s *Service) Confirm(ctx context.Context, a events.Actor, code string) (Link, error) {
	code = strings.TrimSpace(code)
	if a.Platform == YouTube && a.Login == "" {
		return Link{}, fmt.Errorf("%w: your YouTube name has no @handle, so start from YouTube instead: !link yourtwitchname", ErrCode)
	}
	key := a.Platform + ":" + a.ID
	s.mu.Lock()
	now := s.now()
	s.expireLocked(now)
	if len(s.tries[key]) >= MaxTries {
		s.mu.Unlock()
		return Link{}, fmt.Errorf("%w: too many wrong codes; wait a few minutes", ErrCode)
	}
	p := s.pending[code]
	if p == nil || p.Platform != a.Platform || p.Login != strings.ToLower(a.Login) {
		s.tries[key] = append(s.tries[key], now)
		s.mu.Unlock()
		if p != nil && p.Platform != a.Platform {
			return Link{}, fmt.Errorf("%w: type it on %s, as the account you named", ErrCode, Where(p.Platform))
		}
		return Link{}, fmt.Errorf("%w: it may have run out, or be for someone else; start again with !link", ErrCode)
	}
	delete(s.pending, code) // once
	s.mu.Unlock()
	me, err := s.vs.Ensure(ctx, a)
	if err != nil {
		return Link{}, err
	}
	return s.Link(ctx, p.viewer, me.ID, "chat")
}

// Link joins two viewers, one on Twitch and one on YouTube, neither linked
// already. The YouTube viewer's points join the Twitch viewer's balance.
func (s *Service) Link(ctx context.Context, a, b, by string) (Link, error) {
	va, err := s.vs.Get(ctx, a)
	if err != nil {
		return Link{}, err
	}
	vb, err := s.vs.Get(ctx, b)
	if err != nil {
		return Link{}, err
	}
	tw, yt := va, vb
	if tw.Platform == YouTube {
		tw, yt = vb, va
	}
	switch {
	case tw.Platform != Twitch || yt.Platform != YouTube:
		return Link{}, invalid("a link joins a Twitch viewer and a YouTube viewer")
	case tw.ForgottenAt != nil || yt.ForgottenAt != nil:
		return Link{}, invalid("a forgotten viewer can't be linked")
	}
	for _, v := range []viewers.Viewer{tw, yt} {
		if l, err := s.Of(ctx, v.ID); err == nil {
			return Link{}, fmt.Errorf("%w: %s is already linked to %s", ErrLinked, nameOf(v), nameOf(l.partner(v.ID)))
		} else if !errors.Is(err, ErrNotFound) {
			return Link{}, err
		}
	}
	if by == "" {
		by = "the app"
	}
	l := Link{ID: newID(), Twitch: tw, YouTube: yt, LinkedAt: s.now().UTC(), LinkedBy: by}
	memo := fmt.Sprintf("linked: %s on Twitch and %s on YouTube share one balance", nameOf(tw), nameOf(yt))
	err = s.ledger.Share(ctx, "link:"+l.ID, yt.ID, tw.ID, memo, by, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO viewer_links (id, twitch_viewer, youtube_viewer, linked_at, linked_by) VALUES (?, ?, ?, ?, ?)`,
			l.ID, tw.ID, yt.ID, storage.Timestamp(l.LinkedAt), by)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("%w: one of them was linked just now", ErrLinked)
		}
		return err
	})
	if err != nil {
		return Link{}, err
	}
	s.log.Info("viewers linked", "link", l.ID, "twitch", tw.ID, "youtube", yt.ID, "by", by)
	return l, nil
}

// Unlink ends a viewer's link. The shared balance stays with the Twitch
// viewer; the YouTube viewer starts again from nothing.
func (s *Service) Unlink(ctx context.Context, viewer, by string) (Link, error) {
	l, err := s.Of(ctx, viewer)
	if err != nil {
		return Link{}, err
	}
	if by == "" {
		by = "the app"
	}
	at := s.now().UTC()
	res, err := s.db.ExecContext(ctx, "UPDATE viewer_links SET unlinked_at = ?, unlinked_by = ? WHERE id = ? AND unlinked_at IS NULL",
		storage.Timestamp(at), by, l.ID)
	if err != nil {
		return Link{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Link{}, ErrNotFound
	}
	l.UnlinkedAt, l.UnlinkedBy = &at, by
	s.log.Info("viewers unlinked", "link", l.ID, "by", by)
	return l, nil
}

// Of is a viewer's link now, or ErrNotFound.
func (s *Service) Of(ctx context.Context, viewer string) (Link, error) {
	var l Link
	var tw, yt, at string
	err := s.db.QueryRowContext(ctx, `SELECT id, twitch_viewer, youtube_viewer, linked_at, linked_by FROM viewer_links
		WHERE (twitch_viewer = ?1 OR youtube_viewer = ?1) AND unlinked_at IS NULL`, viewer).Scan(&l.ID, &tw, &yt, &at, &l.LinkedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	if err != nil {
		return Link{}, err
	}
	l.LinkedAt, _ = storage.ParseTimestamp(at)
	if l.Twitch, err = s.vs.Get(ctx, tw); err != nil {
		return Link{}, err
	}
	if l.YouTube, err = s.vs.Get(ctx, yt); err != nil {
		return Link{}, err
	}
	return l, nil
}

// Partner is the account linked to a platform account, if any: for a
// giveaway to count one person once.
func (s *Service) Partner(ctx context.Context, platform, userID string) (events.Actor, bool, error) {
	v, err := s.vs.ByAccount(ctx, platform, userID)
	if errors.Is(err, viewers.ErrNotFound) {
		return events.Actor{}, false, nil
	} else if err != nil {
		return events.Actor{}, false, err
	}
	l, err := s.Of(ctx, v.ID)
	if errors.Is(err, ErrNotFound) {
		return events.Actor{}, false, nil
	} else if err != nil {
		return events.Actor{}, false, err
	}
	p := l.partner(v.ID)
	return events.Actor{Platform: p.Platform, ID: p.UserID, Login: p.Login, DisplayName: p.DisplayName}, true, nil
}

func (l Link) partner(viewer string) viewers.Viewer {
	if l.Twitch.ID == viewer {
		return l.YouTube
	}
	return l.Twitch
}

func nameOf(v viewers.Viewer) string {
	if v.DisplayName != "" {
		return v.DisplayName
	}
	return v.Login
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "l_" + hex.EncodeToString(b)
}
