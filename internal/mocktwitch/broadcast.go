package mocktwitch

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The channel's own requests (D-086): Modify Channel Information, Get Games,
// Search Categories, Get Streams, Create Clip, Get Clips and Create Stream
// Marker, as Twitch's reference documents them.

// Games are the categories the stand-in knows, by name.
var Games = map[string]string{
	"Just Chatting":                 "509658",
	"Minecraft":                     "27471",
	"Software and Game Development": "1469308723",
	"Elden Ring":                    "512953",
	"Elden Ring Nightreign":         "1817418245",
}

type broadcastState struct {
	clips     map[string]Clip // by id
	markers   []Marker
	clipDelay time.Duration // how long a clip takes to appear in Get Clips
	clipFail  bool          // clips never appear
	viewers   map[string]int
	edits     int
}

// Clip is a clip made on the stand-in.
type Clip struct {
	ID, Broadcaster, Title string
	Duration               float64
	CreatedAt              time.Time
}

// Marker is a stream marker made on the stand-in.
type Marker struct {
	Broadcaster, Description string
	Position                 int
}

func (s *Server) broadcastInit() {
	if s.bc.clips == nil {
		s.bc.clips = map[string]Clip{}
		s.bc.viewers = map[string]int{}
	}
}

// Clips returns the clips made, in no order.
func (s *Server) Clips() []Clip {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastInit()
	out := []Clip{}
	for _, c := range s.bc.clips {
		out = append(out, c)
	}
	return out
}

// Markers returns the markers made, in order.
func (s *Server) Markers() []Marker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Marker(nil), s.bc.markers...)
}

// ChannelEdits counts Modify Channel Information requests that changed something.
func (s *Server) ChannelEdits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bc.edits
}

// Channel returns a channel's title and category.
func (s *Server) Channel(login string) (title, category string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatInit()
	ch := s.chat.channels[strings.ToLower(login)]
	return ch.title, ch.category
}

// SetClipDelay makes clips take d to appear in Get Clips; fail makes them
// never appear, as when Twitch fails to make one.
func (s *Server) SetClipDelay(d time.Duration, fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bc.clipDelay, s.bc.clipFail = d, fail
}

// SetViewers sets a live channel's viewer count.
func (s *Server) SetViewers(login string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastInit()
	s.bc.viewers[s.userIDLocked(login)] = n
}

func (s *Server) gameName(id string) string {
	for name, gid := range Games {
		if gid == id {
			return name
		}
	}
	return ""
}

// broadcastLocked answers the channel's requests; false when r isn't one.
func (s *Server) broadcastLocked(w http.ResponseWriter, r *http.Request) bool {
	s.broadcastInit()
	q := r.URL.Query()
	switch {
	case r.Method == "PATCH" && r.URL.Path == "/helix/channels":
		g, ok := s.helixAuth(w, r, "channel:manage:broadcast")
		if !ok {
			return true
		}
		if q.Get("broadcaster_id") != g.userID {
			writeErr(w, 400, "The ID in broadcaster_id must match the user ID found in the request's OAuth token.")
			return true
		}
		var in struct {
			Title  *string `json:"title"`
			GameID *string `json:"game_id"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || (in.Title == nil && in.GameID == nil) {
			writeErr(w, 400, "The request must update at least one property.")
			return true
		}
		ch := s.chat.channels[g.login]
		if in.Title != nil {
			switch {
			case *in.Title == "":
				writeErr(w, 400, "The title field may not contain an empty string.")
				return true
			case utf8.RuneCountInString(*in.Title) > 140:
				writeErr(w, 400, "Title exceeds the 140 character limit.")
				return true
			}
			ch.title = *in.Title
		}
		if in.GameID != nil {
			switch name := s.gameName(*in.GameID); {
			case *in.GameID == "" || *in.GameID == "0":
				ch.category = ""
			case name == "":
				writeErr(w, 400, "The ID in game_id is not valid.")
				return true
			default:
				ch.category = name
			}
		}
		s.chat.channels[g.login] = ch
		s.bc.edits++
		login, title, cat := g.login, ch.title, ch.category
		go s.Notify(login, "channel.update", map[string]any{"title": title, "language": "en", "category_id": Games[cat],
			"category_name": cat, "content_classification_labels": []any{}}, "")
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "GET" && r.URL.Path == "/helix/games":
		if _, ok := s.helixAuth(w, r, ""); !ok {
			return true
		}
		if len(q["name"])+len(q["id"]) == 0 {
			writeErr(w, 400, "The request must specify the id or name query parameter.")
			return true
		}
		data := []any{}
		for _, n := range q["name"] {
			if id, ok := Games[n]; ok { // the name must match exactly
				data = append(data, map[string]string{"id": id, "name": n, "box_art_url": "", "igdb_id": ""})
			}
		}
		writeJSON(w, map[string]any{"data": data})
	case r.Method == "GET" && r.URL.Path == "/helix/search/categories":
		if _, ok := s.helixAuth(w, r, ""); !ok {
			return true
		}
		query := strings.ToLower(q.Get("query"))
		if query == "" {
			writeErr(w, 400, "The query parameter is required.")
			return true
		}
		data := []any{}
		for _, n := range sortedGames() {
			for _, word := range strings.Fields(strings.ToLower(n)) {
				if strings.HasPrefix(strings.ToLower(n), query) || strings.HasPrefix(word, query) {
					data = append(data, map[string]string{"id": Games[n], "name": n, "box_art_url": ""})
					break
				}
			}
		}
		writeJSON(w, map[string]any{"data": data, "pagination": map[string]any{}})
	case r.Method == "GET" && r.URL.Path == "/helix/streams":
		if _, ok := s.helixAuth(w, r, ""); !ok {
			return true
		}
		s.adsInit()
		data := []any{}
		for _, id := range q["user_id"] {
			if started, live := s.ad.live[id]; live {
				login := s.loginOfLocked(id)
				ch := s.chat.channels[login]
				data = append(data, map[string]any{"id": "stream-" + id, "user_id": id, "user_login": login, "user_name": login,
					"thumbnail_url": "https://static-cdn.jtvnw.net/previews-ttv/live_user_" + login + "-{width}x{height}.jpg",
					"game_id":       Games[ch.category], "game_name": ch.category, "type": "live", "title": ch.title, "tags": []any{},
					"viewer_count": s.bc.viewers[id], "started_at": started.Format(time.RFC3339), "language": "en",
					"tag_ids": []any{}, "is_mature": false})
			}
		}
		writeJSON(w, map[string]any{"data": data, "pagination": map[string]any{}})
	case r.Method == "POST" && r.URL.Path == "/helix/clips":
		if _, ok := s.helixAuth(w, r, "clips:edit"); !ok {
			return true
		}
		id := q.Get("broadcaster_id")
		if id == "" {
			writeErr(w, 400, "Missing broadcaster_id")
			return true
		}
		s.adsInit()
		if _, live := s.ad.live[id]; !live {
			writeErr(w, 404, "The broadcaster in the broadcaster_id query parameter must be broadcasting live.")
			return true
		}
		d := 30.0
		if v := q.Get("duration"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 5 || f > 60 {
				writeErr(w, 400, "duration must be 5 to 60")
				return true
			}
			d = f
		}
		c := Clip{ID: "Clip" + random(8), Broadcaster: s.loginOfLocked(id), Title: q.Get("title"), Duration: d, CreatedAt: s.now().UTC()}
		if !s.bc.clipFail {
			s.bc.clips[c.ID] = c
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": c.ID,
			"edit_url": "https://clips.twitch.tv/" + c.ID + "/edit"}}})
	case r.Method == "GET" && r.URL.Path == "/helix/clips":
		if _, ok := s.helixAuth(w, r, ""); !ok {
			return true
		}
		data := []any{}
		for _, id := range q["id"] {
			c, ok := s.bc.clips[id]
			if !ok || s.now().Sub(c.CreatedAt) < s.bc.clipDelay {
				continue
			}
			data = append(data, map[string]any{"id": c.ID, "url": "https://clips.twitch.tv/" + c.ID,
				"embed_url": "https://clips.twitch.tv/embed?clip=" + c.ID, "broadcaster_id": s.userIDLocked(c.Broadcaster),
				"title": c.Title, "duration": c.Duration, "created_at": c.CreatedAt.Format(time.RFC3339), "view_count": 0})
		}
		writeJSON(w, map[string]any{"data": data, "pagination": map[string]any{}})
	case r.Method == "POST" && r.URL.Path == "/helix/streams/markers":
		g, ok := s.helixAuth(w, r, "channel:manage:broadcast")
		if !ok {
			return true
		}
		var in struct {
			UserID      string `json:"user_id"`
			Description string `json:"description"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.UserID == "" {
			writeErr(w, 400, "Missing user_id")
			return true
		}
		if utf8.RuneCountInString(in.Description) > 140 {
			writeErr(w, 400, "The description is too long.")
			return true
		}
		if in.UserID != g.userID {
			writeErr(w, 403, "The user in the access token is not authorized to create video markers for the user in user_id.")
			return true
		}
		s.adsInit()
		started, live := s.ad.live[in.UserID]
		if !live {
			writeErr(w, 404, "The user in the user_id query parameter is not streaming live.")
			return true
		}
		m := Marker{Broadcaster: g.login, Description: in.Description, Position: int(s.now().Sub(started).Seconds())}
		s.bc.markers = append(s.bc.markers, m)
		writeJSON(w, map[string]any{"data": []any{map[string]any{"id": random(8), "created_at": s.now().UTC().Format(time.RFC3339),
			"description": m.Description, "position_seconds": m.Position}}})
	default:
		return false
	}
	return true
}

func sortedGames() []string {
	names := make([]string, 0, len(Games))
	for n := range Games {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
