package mockyoutube

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// Moderating (M6 slice 5): liveChatMessages.delete, liveChatBans.insert
// and .delete, liveChatModerators.list, .insert and .delete, as Google
// documents them. The chat's owner may do everything; its moderators may
// delete and ban. A deleted message becomes a tombstone in the stream, and
// a ban a userBannedEvent, as YouTube shows them.

type ban struct {
	chatID, channel string
}

type moderator struct {
	id, chatID, channel, name string
}

func (s *Server) channelName(id string) string {
	for _, c := range s.channels {
		if c.ID == id {
			return c.Title
		}
	}
	return id
}

// canModerate: the chat's owner or one of its moderators. Call with mu held.
func (s *Server) canModerate(chatID, channel string) bool {
	if s.ownerOf(chatID) == channel {
		return true
	}
	for _, m := range s.mods {
		if m.chatID == chatID && m.channel == channel {
			return true
		}
	}
	return false
}

// Deleted lists the ids of messages deleted through the API.
func (s *Server) Deleted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deleted...)
}

// Banned reports whether a channel is banned (or timed out) by the API.
func (s *Server) Banned(channel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.banned[channel]
}

// Moderators lists the channels that are a chat's moderators.
func (s *Server) Moderators(chatID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, m := range s.mods {
		if m.chatID == chatID {
			out = append(out, m.channel)
		}
	}
	return out
}

// AddModerator makes a channel a chat's moderator, as YouTube Studio would.
func (s *Server) AddModerator(chatID, channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("mod%d", s.seq)
	s.mods[id] = moderator{id: id, chatID: chatID, channel: channel, name: s.channelName(channel)}
	return id
}

// deleteMessage is liveChatMessages.delete.
func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	t, ok := s.authed(w, r, "liveChatMessages.delete")
	if !ok {
		s.mu.Unlock()
		return
	}
	id := r.URL.Query().Get("id")
	var chatID, author string
	found := false
	for cid, c := range s.chats {
		for _, m := range c.msgs {
			if m.ID == id && m.Snippet.Type == livechat.TypeText {
				chatID, author, found = cid, m.Author.ChannelID, true
			}
		}
	}
	switch {
	case !found:
		s.mu.Unlock()
		apiErr(w, http.StatusNotFound, "liveChatMessageNotFound", "The specified chat message could not be found.")
		return
	case !s.canModerate(chatID, t.channel):
		s.mu.Unlock()
		apiErr(w, http.StatusForbidden, "forbidden", "You don't have the permissions required to delete the specified message.")
		return
	case author == s.ownerOf(chatID) && t.channel != author:
		s.mu.Unlock()
		apiErr(w, http.StatusForbidden, "modificationNotAllowed", "The message cannot be deleted.")
		return
	}
	if slices.Contains(s.deleted, id) { // already a tombstone
		s.mu.Unlock()
		apiErr(w, http.StatusNotFound, "liveChatMessageNotFound", "The specified chat message could not be found.")
		return
	}
	s.deleted = append(s.deleted, id)
	s.mu.Unlock()
	s.add(chatID, livechat.Message{ID: id, Snippet: livechat.Snippet{Type: livechat.TypeTombstone}})
	w.WriteHeader(http.StatusNoContent)
}

// bans is liveChatBans.insert (POST) and .delete (DELETE).
func (s *Server) bans(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.mu.Lock()
		defer s.mu.Unlock()
		t, ok := s.authed(w, r, "liveChatBans.delete")
		if !ok {
			return
		}
		b, known := s.bansByID[r.URL.Query().Get("id")]
		switch {
		case !known:
			apiErr(w, http.StatusNotFound, "liveChatBanNotFound", "The specified ban could not be found.")
		case !s.canModerate(b.chatID, t.channel):
			apiErr(w, http.StatusForbidden, "insufficientPermissions", "You can't remove this ban.")
		default:
			delete(s.bansByID, r.URL.Query().Get("id"))
			delete(s.banned, b.channel)
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	var in struct {
		Snippet struct {
			LiveChatID         string `json:"liveChatId"`
			Type               string `json:"type"`
			BanDurationSeconds uint64 `json:"banDurationSeconds"`
			BannedUserDetails  struct {
				ChannelID string `json:"channelId"`
			} `json:"bannedUserDetails"`
		} `json:"snippet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apiErr(w, http.StatusBadRequest, "parseError", "not JSON")
		return
	}
	sn := in.Snippet
	s.mu.Lock()
	t, ok := s.authed(w, r, "liveChatBans.insert")
	if !ok {
		s.mu.Unlock()
		return
	}
	c := s.chats[sn.LiveChatID]
	target := sn.BannedUserDetails.ChannelID
	fail := func(status int, reason, msg string) { s.mu.Unlock(); apiErr(w, status, reason, msg) }
	switch {
	case r.URL.Query().Get("part") != "snippet":
		fail(http.StatusBadRequest, "invalidPart", "part must be snippet")
		return
	case sn.LiveChatID == "":
		fail(http.StatusBadRequest, "liveChatIdRequired", "The liveChatId is required.")
		return
	case target == "":
		fail(http.StatusBadRequest, "bannedUserChannelIdRequired", "The banned user's channel id is required.")
		return
	case sn.Type != "permanent" && sn.Type != "temporary":
		fail(http.StatusBadRequest, "invalidValue", "type must be permanent or temporary")
		return
	case c == nil:
		fail(http.StatusNotFound, "liveChatNotFound", "The live chat could not be found.")
		return
	case !s.canModerate(sn.LiveChatID, t.channel):
		fail(http.StatusForbidden, "insufficientPermissions", "You can't ban in this chat.")
		return
	case s.canModerate(sn.LiveChatID, target):
		fail(http.StatusForbidden, "liveChatBanInsertionNotAllowed", "You can't ban the chat owner or a moderator.")
		return
	}
	s.seq++
	id := fmt.Sprintf("ban%d", s.seq)
	s.bansByID[id] = ban{chatID: sn.LiveChatID, channel: target}
	s.banned[target] = true
	byName, targetName := s.channelName(t.channel), s.channelName(target)
	for _, cc := range s.chats {
		for _, m := range cc.msgs {
			if m.Author.ChannelID == target && m.Author.DisplayName != "" {
				targetName = m.Author.DisplayName
			}
		}
	}
	s.mu.Unlock()
	ub := &livechat.UserBanned{ChannelID: target, DisplayName: targetName, BanType: livechat.BanPermanent}
	if sn.Type == "temporary" {
		ub.BanType, ub.DurationSeconds = livechat.BanTemporary, sn.BanDurationSeconds
		if ub.DurationSeconds == 0 {
			ub.DurationSeconds = 300 // Google's default
		}
	}
	s.add(sn.LiveChatID, livechat.Message{Author: livechat.Author{ChannelID: t.channel, DisplayName: byName},
		Snippet: livechat.Snippet{Type: livechat.TypeUserBanned, UserBanned: ub}})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "youtube#liveChatBan", "id": id, "snippet": in.Snippet})
}

// moderators is liveChatModerators.list (GET), .insert (POST) and
// .delete (DELETE): the chat's owner only.
func (s *Server) moderators(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		t, ok := s.authed(w, r, "liveChatModerators.list")
		if !ok {
			return
		}
		chatID := r.URL.Query().Get("liveChatId")
		if s.ownerOf(chatID) != t.channel {
			apiErr(w, http.StatusForbidden, "forbidden", "Only the chat's owner can list its moderators.")
			return
		}
		items := []map[string]any{}
		for _, m := range s.mods {
			if m.chatID == chatID {
				items = append(items, map[string]any{"id": m.id, "snippet": map[string]any{"liveChatId": chatID,
					"moderatorDetails": map[string]any{"channelId": m.channel, "displayName": m.name}}})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "youtube#liveChatModeratorListResponse", "items": items})
	case http.MethodPost:
		t, ok := s.authed(w, r, "liveChatModerators.insert")
		if !ok {
			return
		}
		var in struct {
			Snippet struct {
				LiveChatID       string `json:"liveChatId"`
				ModeratorDetails struct {
					ChannelID string `json:"channelId"`
				} `json:"moderatorDetails"`
			} `json:"snippet"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Snippet.ModeratorDetails.ChannelID == "" {
			apiErr(w, http.StatusBadRequest, "invalidValue", "a moderator's channel id is required")
			return
		}
		chatID := in.Snippet.LiveChatID
		if s.chats[chatID] == nil {
			apiErr(w, http.StatusNotFound, "liveChatNotFound", "The live chat could not be found.")
			return
		}
		if s.ownerOf(chatID) != t.channel {
			apiErr(w, http.StatusForbidden, "forbidden", "Only the chat's owner can add moderators.")
			return
		}
		s.seq++
		id := fmt.Sprintf("mod%d", s.seq)
		ch := in.Snippet.ModeratorDetails.ChannelID
		s.mods[id] = moderator{id: id, chatID: chatID, channel: ch, name: s.channelName(ch)}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "youtube#liveChatModerator", "id": id})
	case http.MethodDelete:
		t, ok := s.authed(w, r, "liveChatModerators.delete")
		if !ok {
			return
		}
		m, known := s.mods[r.URL.Query().Get("id")]
		switch {
		case !known:
			apiErr(w, http.StatusNotFound, "liveChatModeratorNotFound", "The moderator could not be found.")
		case s.ownerOf(m.chatID) != t.channel:
			apiErr(w, http.StatusForbidden, "forbidden", "Only the chat's owner can remove moderators.")
		default:
			delete(s.mods, m.id)
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		apiErr(w, http.StatusMethodNotAllowed, "methodNotAllowed", "GET, POST or DELETE")
	}
}
