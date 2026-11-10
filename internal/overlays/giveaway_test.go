package overlays

import (
	"encoding/json"
	"testing"
	"time"
)

// drain returns the types of the messages a page was sent, and the bodies.
func drain(c *conn) (types []string, bodies []json.RawMessage) {
	for {
		select {
		case b := <-c.send:
			var m struct {
				Type string          `json:"type"`
				Body json.RawMessage `json:"body"`
			}
			_ = json.Unmarshal(b, &m)
			types = append(types, m.Type)
			bodies = append(bodies, m.Body)
		default:
			return types, bodies
		}
	}
}

func count(types []string, t string) int {
	n := 0
	for _, x := range types {
		if x == t {
			n++
		}
	}
	return n
}

// A live giveaway reaches giveaway overlays (pages and previews), never
// alert overlays; practice reaches only previews; a page that opens later
// gets the state; only Twitch pictures are sent; and only OBS pages count
// for chat waiting.
func TestGiveawaysReachGiveawayOverlays(t *testing.T) {
	h := NewHub(nil)
	gw := Overlay{Name: "giveaway", Kind: KindGiveaway, Look: DefaultLook()}
	page, preview := h.join(gw, false), h.join(gw, true)
	alert := h.join(Overlay{Name: "alerts", Kind: KindAlert, Look: DefaultLook()}, false)
	for _, c := range []*conn{page, preview, alert} {
		drain(c)
	}
	if h.GiveawayPages() != 1 {
		t.Fatal(h.GiveawayPages())
	}
	h.SetGiveaway(&GiveawayView{ID: "g", Title: "a mug", State: "open", Winners: []GiveawayWinner{}})
	h.GiveawayDraw(GiveawayDraw{ID: "d1", Name: "Ann", Roll: []string{"Bob", "Ann"}})
	h.GiveawayPicture("d1", "https://static-cdn.jtvnw.net/x.png", false)
	h.GiveawayPicture("d1", "https://evil.example/x.png", false)
	for name, c := range map[string]*conn{"page": page, "preview": preview} {
		types, _ := drain(c)
		if count(types, "giveaway") != 1 || count(types, "draw") != 1 || count(types, "picture") != 1 {
			t.Fatal(name, types)
		}
	}
	if types, _ := drain(alert); len(types) != 0 {
		t.Fatal("an alert overlay was sent", types)
	}
	// Practice: previews only.
	h.SetGiveaway(&GiveawayView{ID: "p", State: "open", Practice: true})
	h.GiveawayDraw(GiveawayDraw{ID: "d2", Name: "Tester", Roll: []string{"Tester"}, Practice: true})
	if types, _ := drain(page); count(types, "draw") != 0 || count(types, "giveaway") != 0 {
		t.Fatal("OBS was sent practice:", types)
	}
	if types, _ := drain(preview); count(types, "draw") != 1 || count(types, "giveaway") != 1 {
		t.Fatal(types)
	}
	// A live state again, and a page that opens later gets it.
	h.SetGiveaway(&GiveawayView{ID: "g2", Title: "later", State: "open"})
	late := h.join(gw, false)
	types, bodies := drain(late)
	if len(types) != 2 || types[0] != "hello" || types[1] != "giveaway" {
		t.Fatal(types)
	}
	var v GiveawayView
	_ = json.Unmarshal(bodies[1], &v)
	if v.ID != "g2" {
		t.Fatal(v)
	}
	// A roll is capped, the winner kept last.
	roll := make([]string, 40)
	for i := range roll {
		roll[i] = "x"
	}
	roll[39] = "Win"
	h.GiveawayDraw(GiveawayDraw{ID: "d3", Name: "Win", Roll: roll})
	_, bodies = drain(late)
	var d GiveawayDraw
	_ = json.Unmarshal(bodies[0], &d)
	if len(d.Roll) != MaxRoll || d.Roll[MaxRoll-1] != "Win" {
		t.Fatal(d.Roll)
	}
}

// A sample shows on one giveaway overlay: previews only, or OBS too.
func TestGiveawaySamples(t *testing.T) {
	h := NewHub(func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) })
	gw := Overlay{Name: "giveaway", Kind: KindGiveaway}
	page, preview := h.join(gw, false), h.join(gw, true)
	other := h.join(Overlay{Name: "other", Kind: KindGiveaway}, true)
	drain(page)
	drain(preview)
	drain(other)
	if pages, previews := h.SampleGiveaway("giveaway", true); pages != 0 || previews != 1 {
		t.Fatal(pages, previews)
	}
	if types, _ := drain(page); len(types) != 0 {
		t.Fatal("OBS got a preview sample", types)
	}
	if pages, _ := h.SampleGiveaway("giveaway", false); pages != 1 {
		t.Fatal(pages)
	}
	if types, _ := drain(other); len(types) != 0 {
		t.Fatal("another overlay got the sample", types)
	}
}
