package giveaways

import (
	"context"
	"fmt"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
)

// Drawing from recent chat (docs/adr/0028-giveaway-from-chat.md): everyone
// StreamGremlin saw chatting in the window enters through the ordinary
// entry checks, once each, in chat order. Then entries close, the list is
// fingerprinted, and the winners are drawn, so the record checks like any
// other giveaway's. Nobody asked to enter, so nobody is told they're in or
// turned away; the screen lists both.

// checkers is how many chatters are checked against Twitch at once.
const checkers = 4

// fromChat fills, closes and draws a giveaway opened from recent chat. If
// it fails part way, the giveaway stays open for the streamer to draw or
// cancel.
func (s *Service) fromChat(ctx context.Context, g Giveaway) (Giveaway, error) {
	cands, err := s.recentChatters(ctx, &g)
	if err != nil {
		return g, err
	}
	type checked struct {
		q      Qualified
		reason string
	}
	results := make([]checked, len(cands))
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(checkers, len(cands)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				q, reason := s.qualify(ctx, cands[i])
				results[i] = checked{q, reason}
			}
		}()
	}
	for i := range cands {
		next <- i
	}
	close(next)
	wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.current
	if cur == nil || cur.ID != g.ID || cur.State != Open {
		return g, notNow("“%s” was closed or cancelled while chat was being checked", g.Title)
	}
	for i, cd := range cands {
		if err := s.admitLocked(ctx, cd, results[i].q, results[i].reason); err != nil {
			return *cur, err
		}
	}
	closed, err := s.closeLocked(ctx, cur)
	if err != nil {
		return *cur, err
	}
	if closed.EntryCount == 0 {
		why := fmt.Sprintf("no one chatted in the last %s", plural(g.FromChatMinutes, "minute", "minutes"))
		if len(cands) > 0 {
			why = fmt.Sprintf("no one who chatted in the last %s can enter", plural(g.FromChatMinutes, "minute", "minutes"))
		}
		return s.finishLocked(ctx, cur, Cancelled, fmt.Sprintf("%s: %s, so it's cancelled.", g.Title, why),
			map[string]any{"winners": []any{}})
	}
	if _, err := s.drawLocked(ctx, cur, 0, ""); err != nil {
		return *cur, err
	}
	return *cur, nil
}

// recentChatters are those who chatted in the window before the giveaway
// opened, once each (their first message), in chat order: real chat for a
// live giveaway, test chat for practice. The streamer isn't among them.
func (s *Service) recentChatters(ctx context.Context, g *Giveaway) ([]candidate, error) {
	since := g.OpenedAt.Add(-time.Duration(g.FromChatMinutes) * time.Minute)
	cursor, err := s.inbox.SeqBeforeUpTo(ctx, since, g.afterSeq)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []candidate
	for cursor < g.afterSeq {
		recs, err := s.inbox.After(ctx, cursor, 500, followChat)
		if err != nil {
			return nil, err
		}
		if len(recs) == 0 {
			break
		}
		for _, r := range recs {
			cursor = r.Seq
			if r.Seq > g.afterSeq {
				break
			}
			if events.LogOnly(r) || r.ReceivedAt.Before(since) || (r.DeliveryQuality == events.Synthetic) != g.Practice {
				continue
			}
			p, platform, ok := chatOf(r)
			if !ok || p.Chatter.Broadcaster || seen[platform+":"+p.Chatter.ID] {
				continue
			}
			seen[platform+":"+p.Chatter.ID] = true
			out = append(out, candidate{g: *g, r: r, platform: platform, c: p.Chatter, quiet: true})
		}
	}
	return out, nil
}
