package giveaways

import (
	"context"
	"fmt"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/overlays"
)

// Showing giveaways on giveaway overlays (docs/adr/0029-giveaway-overlay.md).
// The overlay is told the state after every change, and each winner after
// their draw commits; it never decides anything. Chat waits for the roll
// only while a giveaway overlay is open in OBS.

// Display is the overlay hub, as giveaways use it.
type Display interface {
	SetGiveaway(v *overlays.GiveawayView)
	GiveawayDraw(d overlays.GiveawayDraw)
	GiveawayPicture(drawID, picture string, practice bool)
	GiveawayPages() int
}

// howToEnter says how to enter, as the opening does.
func howToEnter(g *Giveaway) string {
	switch {
	case g.FromChatMinutes > 0:
		return fmt.Sprintf("Drawn from everyone who chatted in the last %s", plural(g.FromChatMinutes, "minute", "minutes"))
	case g.EnterByChat:
		return fmt.Sprintf("Say anything in chat, or type !%s, to enter", g.Command)
	default:
		return fmt.Sprintf("Type !%s in chat to enter", g.Command)
	}
}

// publishLocked tells the overlays the giveaway's state.
func (s *Service) publishLocked(ctx context.Context, g *Giveaway) {
	if s.display == nil || g == nil {
		return
	}
	v := overlays.GiveawayView{ID: g.ID, Title: g.Title, State: g.State, HowToEnter: howToEnter(g), Who: Who(g), Chances: ChancesWords(g),
		ClosesAt: g.ClosesAt, EndedAt: g.EndedAt, Practice: g.Practice, Winners: []overlays.GiveawayWinner{}}
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM giveaway_entries WHERE giveaway_id = ?", g.ID).Scan(&v.Entries)
	if draws, err := drawsOf(ctx, s.db, g.ID); err == nil {
		for _, d := range draws {
			if d.Outcome == SetAside || d.Outcome == Forfeited {
				continue
			}
			v.Winners = append(v.Winners, overlays.GiveawayWinner{Draw: d.N, Name: d.Winner.DisplayName, Outcome: d.Outcome,
				ClaimUntil: d.ClaimUntil})
		}
	}
	s.display.SetGiveaway(&v)
}

// rollWaits reports whether a live draw's chat should wait for the roll:
// only when a giveaway overlay is open in OBS.
func (s *Service) rollWaits(g *Giveaway) bool {
	return s.display != nil && !g.Practice && s.display.GiveawayPages() > 0
}

// showDraws sends each new winner to the overlays, with names to roll
// through, then their picture when Twitch answers. The names are for show
// and decide nothing; they're shuffled with crypto/rand all the same.
func (s *Service) showDraws(ctx context.Context, g Giveaway, draws []Draw, candidates []Entry) {
	if s.display == nil || len(draws) == 0 {
		return
	}
	claim := ""
	if g.ClaimCommand != "" {
		claim = fmt.Sprintf("Type !%s within %d seconds to claim it.", g.ClaimCommand, g.ClaimSeconds)
	}
	for _, d := range draws {
		var others []string
		for _, e := range candidates {
			if e.Seq != d.EntrySeq {
				others = append(others, e.DisplayName)
			}
		}
		for i := len(others) - 1; i > 0; i-- {
			if j, err := pick(i + 1); err == nil {
				others[i], others[j] = others[j], others[i]
			}
		}
		if len(others) > overlays.MaxRoll-1 {
			others = others[:overlays.MaxRoll-1]
		}
		roll := append(others, d.Winner.DisplayName)
		s.display.GiveawayDraw(overlays.GiveawayDraw{ID: d.ID, Giveaway: g.ID, Title: g.Title, N: d.N, Name: d.Winner.DisplayName,
			Roll: roll, Claim: claim, Practice: g.Practice})
	}
	// Pictures are decoration: asked after the roll has started, and
	// skipped for practice and other platforms.
	if g.Practice || s.tw == nil {
		return
	}
	go func() {
		for _, d := range draws {
			if d.Winner.Platform != "twitch" {
				continue
			}
			pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			pic, err := s.tw.ProfilePicture(pctx, d.Winner.UserID)
			cancel()
			if err == nil && pic != "" {
				s.display.GiveawayPicture(d.ID, pic, g.Practice)
			}
		}
	}()
}
