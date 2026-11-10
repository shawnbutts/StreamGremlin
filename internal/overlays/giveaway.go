package overlays

import (
	"net/url"
	"strings"
	"time"
)

// Giveaway overlays (docs/adr/0029-giveaway-overlay.md) show the running
// giveaway and roll to each winner the engine has already drawn. They
// decide nothing, and they're never needed: chat carries everything.
// Practice giveaways show only in the app's previews, never in OBS.

// Roll timing, the same in the page: names roll for RollTime, then the
// winner shows for WinnerTime. A live winner's chat message waits for
// ChatWait after the draw, and each later winner of the same draw one
// DrawTime more, so chat names them as the roll lands.
const (
	RollTime   = 3500 * time.Millisecond
	WinnerTime = 5000 * time.Millisecond
	DrawTime   = RollTime + WinnerTime
	ChatWait   = RollTime + 500*time.Millisecond
	// MaxRoll is how many names a roll shows, the winner last.
	MaxRoll = 24
)

// GiveawayView is what a giveaway overlay shows. Public only: what chat
// was told.
type GiveawayView struct {
	ID         string           `json:"id"`
	Title      string           `json:"title"`
	State      string           `json:"state"` // open, closed, ended or cancelled
	HowToEnter string           `json:"how_to_enter"`
	Who        string           `json:"who,omitempty"`
	Chances    string           `json:"chances"`
	Entries    int              `json:"entries"`
	ClosesAt   *time.Time       `json:"closes_at,omitempty"`
	EndedAt    *time.Time       `json:"ended_at,omitempty"`
	Winners    []GiveawayWinner `json:"winners"`
	Practice   bool             `json:"practice,omitempty"`
}

// GiveawayWinner is a winner as shown: their name and how it went.
type GiveawayWinner struct {
	Draw       int        `json:"draw"`
	Name       string     `json:"name"`
	Outcome    string     `json:"outcome"`
	ClaimUntil *time.Time `json:"claim_until,omitempty"`
}

// GiveawayDraw is one winner to roll to. Roll is names for show, the
// winner last; the winner was drawn before this is sent.
type GiveawayDraw struct {
	ID       string   `json:"id"`
	Giveaway string   `json:"giveaway"`
	Title    string   `json:"title"`
	N        int      `json:"n"`
	Name     string   `json:"name"`
	Picture  string   `json:"picture,omitempty"`
	Roll     []string `json:"roll"`
	Claim    string   `json:"claim,omitempty"` // how to claim, as chat says it
	Practice bool     `json:"practice,omitempty"`
}

// giveawayFor reports whether a page should get a giveaway's messages:
// giveaway overlays only, and for practice, the app's previews only.
func giveawayFor(c *conn, practice bool) bool {
	return c.kind == KindGiveaway && (!practice || c.preview)
}

// SetGiveaway sends the giveaway's state to giveaway overlays, and keeps it
// for pages that open later. nil clears it.
func (h *Hub) SetGiveaway(v *GiveawayView) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.giveaway = v
	b := message("giveaway", v)
	for _, cs := range h.conns {
		for c := range cs {
			if c.kind == KindGiveaway && (v == nil || giveawayFor(c, v.Practice)) {
				h.sendLocked(c, b)
			}
		}
	}
}

// GiveawayDraw sends a winner to roll to.
func (h *Hub) GiveawayDraw(d GiveawayDraw) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(d.Roll) > MaxRoll {
		d.Roll = d.Roll[len(d.Roll)-MaxRoll:]
	}
	b := message("draw", d)
	for _, cs := range h.conns {
		for c := range cs {
			if giveawayFor(c, d.Practice) {
				h.sendLocked(c, b)
			}
		}
	}
}

// GiveawayPicture sends a drawn winner's picture, once it's known. Only a
// picture on Twitch's picture host is sent.
func (h *Hub) GiveawayPicture(drawID, picture string, practice bool) {
	if !goodPicture(picture) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	b := message("picture", map[string]string{"id": drawID, "picture": picture})
	for _, cs := range h.conns {
		for c := range cs {
			if giveawayFor(c, practice) {
				h.sendLocked(c, b)
			}
		}
	}
}

// GiveawayPages counts giveaway overlays open in OBS (not the app's
// previews): when there are none, chat doesn't wait for a roll.
func (h *Hub) GiveawayPages() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, cs := range h.conns {
		for c := range cs {
			if c.kind == KindGiveaway && !c.preview {
				n++
			}
		}
	}
	return n
}

// SampleGiveaway shows a made-up giveaway and a draw on one overlay, to see
// how it looks: in the app's previews only, or in OBS as well. It doesn't
// change the running giveaway's state; its next change replaces it.
func (h *Hub) SampleGiveaway(name string, previewsOnly bool) (pages, previews int) {
	now := h.now()
	closes := now.Add(4 * time.Minute)
	v := GiveawayView{ID: "sample", Title: "a sample prize", State: "open", HowToEnter: "Type !join in chat to enter",
		Chances: "every entry has the same chance", Entries: 42, ClosesAt: &closes, Winners: []GiveawayWinner{}}
	d := GiveawayDraw{ID: "sample-" + now.Format("150405.000"), Giveaway: "sample", Title: v.Title, N: 1, Name: "SampleWinner",
		Roll:  []string{"Ann", "Bob", "Cat", "Dan", "Eve", "Fay", "Gus", "Hal", "Ivy", "Jo", "Kim", "Lou", "SampleWinner"},
		Claim: "Type !claim within 60 seconds to claim it."}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns[name] {
		if c.kind != KindGiveaway || (previewsOnly && !c.preview) {
			continue
		}
		if c.preview {
			previews++
		} else {
			pages++
		}
		h.sendLocked(c, message("giveaway", v))
		h.sendLocked(c, message("draw", d))
	}
	return pages, previews
}

// goodPicture reports whether a picture is on a host the page's CSP allows.
func goodPicture(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return false
	}
	for _, h := range PictureHosts {
		if u.Scheme+"://"+u.Host == h && strings.HasPrefix(u.Path, "/") {
			return true
		}
	}
	return false
}
