package mockyoutube

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"code.streamgremlin.com/streamgremlin/internal/youtube/livechat"
)

// Live broadcasts and their chat (M6 slice 2): liveBroadcasts.list with
// broadcastStatus=active, and liveChatMessages.streamList over gRPC on the
// same address (HTTP/2 without TLS, which Serve turns on).

// Broadcast is a live broadcast on a channel.
type Broadcast struct {
	ID, Title, ChatID string
	Channel           string
	Started           time.Time
	// ChatOff makes streamList refuse, as for a broadcast with chat turned off.
	ChatOff bool
	ended   bool
}

type chat struct {
	msgs  []livechat.Message
	ended bool
	wake  chan struct{} // closed and replaced when something's added
}

// backlog is how many recent messages a stream without a page token gets
// first, as Google sends recent history.
const backlog = 5

// Serve starts the stand-in on a loopback address with HTTP/1 and HTTP/2
// without TLS, so the gRPC chat stream works. Close it when done.
func Serve(s *Server) *httptest.Server {
	srv := httptest.NewUnstartedServer(s)
	srv.Config.Protocols = Protocols()
	srv.Start()
	return srv
}

// Protocols are what an http.Server serving the stand-in needs.
func Protocols() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	return p
}

func (s *Server) grpcServer() *grpc.Server {
	g := grpc.NewServer(grpc.ForceServerCodecV2(livechat.Codec{}))
	g.RegisterService(&grpc.ServiceDesc{
		ServiceName: livechat.Service,
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName: "StreamList", ServerStreams: true,
			Handler: func(_ any, st grpc.ServerStream) error { return s.streamList(st) },
		}},
	}, struct{}{})
	return g
}

// GoLive starts a broadcast on a channel and returns it.
func (s *Server) GoLive(channel, title string) Broadcast {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	b := &Broadcast{ID: fmt.Sprintf("bc%d", s.seq), Title: title, ChatID: fmt.Sprintf("chat%d", s.seq), Channel: channel, Started: s.now()}
	s.broadcasts = append(s.broadcasts, b)
	s.chats[b.ChatID] = &chat{wake: make(chan struct{})}
	return *b
}

// TurnChatOff makes a broadcast's chat refuse streams, as when the
// streamer turned chat off.
func (s *Server) TurnChatOff(chatID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.broadcasts {
		if b.ChatID == chatID {
			b.ChatOff = true
		}
	}
}

// Say adds a text message to a chat, from a channel ("UCviewer1") with a
// name. It returns the message's ID.
func (s *Server) Say(chatID, channel, name, text string) string {
	return s.add(chatID, livechat.Message{
		Snippet: livechat.Snippet{Type: livechat.TypeText, DisplayMessage: text, HasDisplayContent: true, Text: text, AuthorChannelID: channel},
		Author:  livechat.Author{ChannelID: channel, DisplayName: name, ChannelURL: "http://www.youtube.com/channel/" + channel},
	})
}

// Add adds any message to a chat and returns its ID. A test sets Snippet
// and Author; the ID, chat and time are filled in.
func (s *Server) Add(chatID string, m livechat.Message) string { return s.add(chatID, m) }

func (s *Server) add(chatID string, m livechat.Message) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[chatID]
	if c == nil {
		return ""
	}
	s.seq++
	if m.ID == "" {
		m.ID = fmt.Sprintf("msg%d", s.seq)
	}
	m.Snippet.LiveChatID = chatID
	m.Snippet.PublishedAt = s.now().UTC().Format(time.RFC3339Nano)
	if owner := s.ownerOf(chatID); owner != "" && m.Author.ChannelID == owner {
		m.Author.IsChatOwner = true
	}
	c.msgs = append(c.msgs, m)
	close(c.wake)
	c.wake = make(chan struct{})
	return m.ID
}

func (s *Server) ownerOf(chatID string) string {
	for _, b := range s.broadcasts {
		if b.ChatID == chatID {
			return b.Channel
		}
	}
	return ""
}

// EndBroadcast ends a broadcast: it's no longer active, and its chat sends
// chatEndedEvent and then refuses streams, as Google's does.
func (s *Server) EndBroadcast(id string) {
	s.mu.Lock()
	var chatID string
	for _, b := range s.broadcasts {
		if b.ID == id {
			b.ended, chatID = true, b.ChatID
		}
	}
	s.mu.Unlock()
	if chatID == "" {
		return
	}
	s.add(chatID, livechat.Message{Snippet: livechat.Snippet{Type: livechat.TypeChatEnded}})
	s.mu.Lock()
	s.chats[chatID].ended = true
	s.mu.Unlock()
}

// DropStreams ends every open chat stream with UNAVAILABLE, as a dropped
// connection does. Clients resume with their page token.
func (s *Server) DropStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drop++
	for _, c := range s.chats {
		close(c.wake)
		c.wake = make(chan struct{})
	}
}

// Streams counts streamList calls, and OpenStreams those still open.
func (s *Server) Streams() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls["streamList"] }

// OpenStreams counts streams still open.
func (s *Server) OpenStreams() int { s.mu.Lock(); defer s.mu.Unlock(); return s.open }

// LastPageToken is the page token of the newest stream request.
func (s *Server) LastPageToken() string { s.mu.Lock(); defer s.mu.Unlock(); return s.lastToken }

func (s *Server) liveBroadcasts(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.authed(w, r, "liveBroadcasts")
	if !ok {
		return
	}
	q := r.URL.Query()
	n := 0
	for _, k := range []string{"mine", "id", "broadcastStatus"} {
		if q.Get(k) != "" {
			n++
		}
	}
	if n != 1 || q.Get("broadcastStatus") != "active" {
		apiErr(w, http.StatusBadRequest, "incompatibleParameters", "exactly one of mine, id and broadcastStatus; only broadcastStatus=active is served here")
		return
	}
	items := []any{}
	for _, b := range s.broadcasts {
		if b.Channel != t.channel || b.ended {
			continue
		}
		items = append(items, map[string]any{"kind": "youtube#liveBroadcast", "id": b.ID,
			"snippet": map[string]any{"title": b.Title, "liveChatId": b.ChatID, "actualStartTime": b.Started.UTC().Format(time.RFC3339)},
			"status":  map[string]any{"lifeCycleStatus": "live"}})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "youtube#liveBroadcastListResponse", "items": items})
}

// streamList answers liveChatMessages.streamList: recent messages (or
// those after the page token), then each new one as it's added.
func (s *Server) streamList(st grpc.ServerStream) error {
	var raw livechat.Raw
	if err := st.RecvMsg(&raw); err != nil {
		return err
	}
	req, err := livechat.ParseRequest(raw.B)
	if err != nil || req.LiveChatID == "" {
		return status.Error(codes.InvalidArgument, "liveChatId is required")
	}
	md, _ := metadata.FromIncomingContext(st.Context())
	var bearer string
	if v := md.Get("authorization"); len(v) > 0 {
		bearer, _ = strings.CutPrefix(v[0], "Bearer ")
	}
	s.mu.Lock()
	s.calls["streamList"]++
	s.lastToken = req.PageToken
	tok, known := s.access[bearer]
	switch {
	case !known || s.now().After(tok.expires):
		s.mu.Unlock()
		return status.Error(codes.Unauthenticated, "Request had invalid authentication credentials.")
	case s.quota:
		s.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "The request cannot be completed because you have exceeded your quota.")
	}
	c := s.chats[req.LiveChatID]
	if c == nil {
		s.mu.Unlock()
		return status.Error(codes.NotFound, "The live chat that you are trying to retrieve cannot be found.")
	}
	for _, b := range s.broadcasts {
		if b.ChatID == req.LiveChatID && b.ChatOff {
			s.mu.Unlock()
			return status.Error(codes.FailedPrecondition, "The live chat is disabled or has ended.")
		}
	}
	if c.ended {
		s.mu.Unlock()
		return status.Error(codes.FailedPrecondition, "The live chat is disabled or has ended.")
	}
	next := max(len(c.msgs)-backlog, 0)
	if req.PageToken != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(req.PageToken, "p"))
		if err != nil || n < 0 || n > len(c.msgs) {
			s.mu.Unlock()
			return status.Error(codes.InvalidArgument, "The page token is invalid.")
		}
		next = n
	}
	s.open++
	drop := s.drop
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.open--; s.mu.Unlock() }()

	first := true
	for {
		s.mu.Lock()
		if s.drop != drop {
			s.mu.Unlock()
			return status.Error(codes.Unavailable, "the stand-in dropped the stream")
		}
		items := append([]livechat.Message(nil), c.msgs[next:]...)
		next = len(c.msgs)
		wake := c.wake
		s.mu.Unlock()
		if len(items) > 0 || first {
			resp := livechat.Response{Items: items, NextPageToken: "p" + strconv.Itoa(next)}
			if err := st.SendMsg(&livechat.Raw{B: resp.Marshal()}); err != nil {
				return err
			}
			first = false
			for _, m := range items {
				if m.Snippet.Type == livechat.TypeChatEnded {
					return nil
				}
			}
		}
		select {
		case <-st.Context().Done():
			return st.Context().Err()
		case <-wake:
		}
	}
}

var controls = template.Must(template.New("c").Parse(`<!doctype html><meta charset="utf-8"><title>mockyoutube live</title>
<h1>Live (stand-in for YouTube)</h1>
<form method="post" action="/mock/live"><label>Channel <select name="channel">{{range .Channels}}<option value="{{.ID}}">{{.Title}}</option>{{end}}</select></label>
<label>Title <input name="title" value="Test stream"></label> <button>Go live</button></form>
{{range .Broadcasts}}<h2>{{.Title}} ({{.ID}}){{if .Ended}}: ended{{end}}</h2>{{if not .Ended}}
<form method="post" action="/mock/say"><input type="hidden" name="chat" value="{{.ChatID}}">
<label>From <input name="name" value="Viewer"></label> <label>Message <input name="text" required></label> <button>Say</button></form>
<form method="post" action="/mock/end"><input type="hidden" name="broadcast" value="{{.ID}}"><button>End broadcast</button></form>{{end}}{{end}}
<form method="post" action="/mock/drop"><button>Drop chat streams</button></form>`))

// controlPage lets someone trying StreamGremlin without Google go live,
// chat, end a broadcast and drop streams.
func (s *Server) controlPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		f := r.PostForm
		switch r.URL.Path {
		case "/mock/live":
			s.GoLive(f.Get("channel"), f.Get("title"))
		case "/mock/say":
			name := f.Get("name")
			s.Say(f.Get("chat"), "UC"+strings.ReplaceAll(strings.ToLower(name), " ", ""), name, f.Get("text"))
		case "/mock/end":
			s.EndBroadcast(f.Get("broadcast"))
		case "/mock/drop":
			s.DropStreams()
		}
		http.Redirect(w, r, "/mock", http.StatusSeeOther)
		return
	}
	type row struct {
		ID, Title, ChatID string
		Ended             bool
	}
	s.mu.Lock()
	data := struct {
		Channels   []Channel
		Broadcasts []row
	}{}
	for _, c := range s.channels {
		if !c.NoChannel {
			data.Channels = append(data.Channels, c)
		}
	}
	for _, b := range s.broadcasts {
		data.Broadcasts = append(data.Broadcasts, row{b.ID, b.Title, b.ChatID, b.ended})
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = controls.Execute(w, data)
}
