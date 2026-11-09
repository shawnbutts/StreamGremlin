package mocktwitch

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Ads, as Twitch's API reference describes them (read 2026-11-09): Start
// Commercial (Affiliates and Partners, live, at most 180 seconds, a new ad
// no sooner than 8 minutes after the last), Get Ad Schedule, and Snooze
// Next Ad (live, with an upcoming ad and a snooze left: 5 minutes later).
// An accepted commercial sends channel.ad_break.begin unless HoldAdEvents.

type adState struct {
	live        map[string]time.Time // channel id → went live
	lastAd      map[string]time.Time
	nextAd      map[string]time.Time
	snoozes     map[string]int
	holdEvents  bool
	commercials int
}

func (s *Server) adsInit() {
	if s.ad.live == nil {
		s.ad = adState{live: map[string]time.Time{}, lastAd: map[string]time.Time{}, nextAd: map[string]time.Time{}, snoozes: map[string]int{}}
	}
}

// SetLive says whether a channel is live, for ads (GoLive and GoOffline
// set it too).
func (s *Server) SetLive(channelLogin string, live bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setLiveLocked(s.userIDLocked(channelLogin), live)
}

func (s *Server) setLiveLocked(id string, live bool) {
	s.adsInit()
	if !live {
		delete(s.ad.live, id)
		delete(s.ad.nextAd, id)
		return
	}
	now := s.now().UTC()
	s.ad.live[id] = now
	s.ad.nextAd[id] = now.Add(30 * time.Minute)
	if _, ok := s.ad.snoozes[id]; !ok {
		s.ad.snoozes[id] = 3
	}
}

// HoldAdEvents stops (or restarts) sending channel.ad_break.begin after an
// accepted commercial, as when Twitch is slow to start the break.
func (s *Server) HoldAdEvents(hold bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ad.holdEvents = hold
}

// Commercials counts Start Commercial requests that reached the mock.
func (s *Server) Commercials() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ad.commercials
}

// AdBreak sends channel.ad_break.begin, as Ads Manager does for an
// automatic break (automatic true) or as Twitch does after a commercial.
func (s *Server) AdBreak(channelLogin string, seconds int, automatic bool) string {
	s.mu.Lock()
	id := s.userIDLocked(channelLogin)
	s.adsInit()
	now := s.now().UTC()
	s.ad.lastAd[id] = now
	s.mu.Unlock()
	l := strings.ToLower(channelLogin)
	// As the reference's example shows it: numbers and booleans as strings.
	return s.Notify(channelLogin, "channel.ad_break.begin", map[string]any{"duration_seconds": itoa(seconds),
		"started_at": now.Format(time.RFC3339), "is_automatic": map[bool]string{true: "true", false: "false"}[automatic],
		"requester_user_id": id, "requester_user_login": l, "requester_user_name": channelLogin}, "")
}

func itoa(n int) string { return strconv.Itoa(n) }

func (s *Server) commercial(w http.ResponseWriter, r *http.Request) {
	g, ok := s.pollAuth(w, r, "channel:edit:commercial")
	if !ok {
		return
	}
	var in struct {
		BroadcasterID string `json:"broadcaster_id"`
		Length        int    `json:"length"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.BroadcasterID != g.userID {
		writeErr(w, 400, "The broadcaster_id field is required.")
		return
	}
	s.adsInit()
	s.ad.commercials++
	if _, live := s.ad.live[g.userID]; !live {
		writeErr(w, 400, "To start a commercial, the broadcaster must be streaming live.")
		return
	}
	now := s.now().UTC()
	if last, ok := s.ad.lastAd[g.userID]; ok && now.Sub(last) < 8*time.Minute {
		wait := int((8*time.Minute - now.Sub(last)).Seconds())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"Too Many Requests","status":429,"message":"The broadcaster may not run another commercial until the cooldown period expires.","retry_after":` + itoa(wait) + `}`))
		return
	}
	length := in.Length
	if length > 180 {
		length = 180
	}
	if length < 1 {
		writeErr(w, 400, "The length field is required.")
		return
	}
	s.ad.lastAd[g.userID] = now
	writeJSON(w, map[string]any{"data": []any{map[string]any{"length": length, "message": "", "retry_after": 480}}})
	if !s.ad.holdEvents {
		login := s.loginOf(g.userID)
		go s.AdBreak(login, length, false)
	}
}

func (s *Server) adSchedule(w http.ResponseWriter, r *http.Request) {
	g, ok := s.pollAuth(w, r, "channel:read:ads")
	if !ok {
		return
	}
	s.adsInit()
	at := func(t time.Time, ok bool) string {
		if !ok {
			return ""
		}
		return t.Format(time.RFC3339)
	}
	last, hasLast := s.ad.lastAd[g.userID]
	next, hasNext := s.ad.nextAd[g.userID]
	if _, live := s.ad.live[g.userID]; !live {
		hasLast, hasNext = false, false
	}
	writeJSON(w, map[string]any{"data": []any{map[string]any{"snooze_count": s.ad.snoozes[g.userID],
		"snooze_refresh_at": s.now().UTC().Add(time.Hour).Format(time.RFC3339), "next_ad_at": at(next, hasNext), "duration": 90,
		"last_ad_at": at(last, hasLast), "preroll_free_time": 0}}})
}

func (s *Server) snooze(w http.ResponseWriter, r *http.Request) {
	g, ok := s.pollAuth(w, r, "channel:manage:ads")
	if !ok {
		return
	}
	s.adsInit()
	if _, live := s.ad.live[g.userID]; !live {
		writeErr(w, 400, "The channel is not currently live.")
		return
	}
	next, ok := s.ad.nextAd[g.userID]
	if !ok {
		writeErr(w, 400, "Channel does not have an upcoming scheduled ad break.")
		return
	}
	if s.ad.snoozes[g.userID] <= 0 {
		writeErr(w, 429, "Channel has no snoozes left.")
		return
	}
	s.ad.snoozes[g.userID]--
	next = next.Add(5 * time.Minute)
	s.ad.nextAd[g.userID] = next
	writeJSON(w, map[string]any{"data": []any{map[string]any{"snooze_count": s.ad.snoozes[g.userID],
		"snooze_refresh_at": s.now().UTC().Add(time.Hour).Format(time.RFC3339), "next_ad_at": next.Format(time.RFC3339)}}})
}
