// Package mockobs is a stand-in for OBS's obs-websocket 5 server, for tests
// and for trying StreamGremlin without OBS. It keeps scenes, sources,
// filters, inputs and outputs in memory, answers the requests StreamGremlin
// uses, and sends events. Like OBS, it may send a request's event before its
// answer. Run it standalone with cmd/mockobs; its /panel page changes scenes
// by hand, as a streamer would in OBS.
package mockobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Item is a source placed in a scene.
type Item struct {
	ID      int
	Source  string
	Enabled bool
}

// Scene is an OBS scene; Items are listed bottom first, as OBS stores them.
type Scene struct {
	Name  string
	Items []Item
}

// Input is an OBS input.
type Input struct {
	Kind     string
	Muted    bool
	Settings map[string]any
}

// FilterState is one filter.
type FilterState struct {
	Name, Kind string
	Enabled    bool
}

// Server is a mock OBS.
type Server struct {
	mu        sync.Mutex
	password  string
	scenes    []*Scene // index 0 is the bottom of OBS's list
	current   string
	inputs    map[string]*Input
	filters   map[string][]*FilterState
	recording bool
	replay    bool
	clients   map[*conn]bool
	failNext  map[string]int // request type -> status code to answer once
	requests  []string       // every request type received, for tests
	nextItem  int
	lastShot  string
}

type conn struct {
	c    *websocket.Conn
	out  chan []byte
	subs int
	done chan struct{}
	code websocket.StatusCode
	once sync.Once
}

func (c *conn) close(code websocket.StatusCode) {
	c.once.Do(func() { c.code = code; close(c.done) })
}

// New returns a mock OBS with a typical streamer's setup.
func New() *Server {
	s := &Server{
		inputs:   map[string]*Input{},
		filters:  map[string][]*FilterState{},
		clients:  map[*conn]bool{},
		failNext: map[string]int{},
	}
	s.AddScene("Be Right Back", "BRB Text", "Music")
	s.AddScene("Gaming", "Game Capture", "Webcam", "Alerts")
	s.AddScene("Just Chatting", "Webcam", "Chat Box", "Alerts")
	s.AddScene("Starting Soon", "Countdown", "Music")
	s.current = "Starting Soon"
	s.inputs["Mic/Aux"] = &Input{Kind: "coreaudio_input_capture", Settings: map[string]any{}}
	s.inputs["Desktop Audio"] = &Input{Kind: "coreaudio_output_capture", Settings: map[string]any{}}
	s.inputs["BRB Text"] = &Input{Kind: "text_ft2_source_v2", Settings: map[string]any{"text": "Be right back!"}}
	s.inputs["Music"] = &Input{Kind: "ffmpeg_source", Settings: map[string]any{}}
	s.inputs["Webcam"] = &Input{Kind: "av_capture_input", Settings: map[string]any{}}
	s.filters["Webcam"] = []*FilterState{{Name: "Color Correction", Kind: "color_filter_v2", Enabled: true}, {Name: "Blur", Kind: "blur", Enabled: false}}
	s.filters["Mic/Aux"] = []*FilterState{{Name: "Noise Suppression", Kind: "noise_suppress_filter_v2", Enabled: true}}
	return s
}

// AddScene adds a scene at the top of the list, with sources given top first.
func (s *Server) AddScene(name string, sources ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := &Scene{Name: name}
	for i := len(sources) - 1; i >= 0; i-- { // given top first; stored bottom first
		s.nextItem++
		sc.Items = append(sc.Items, Item{ID: s.nextItem, Source: sources[i], Enabled: true})
	}
	s.scenes = append(s.scenes, sc) // a new scene goes on top; index 0 is the bottom
}

// SetPassword turns authentication on ("" turns it off).
func (s *Server) SetPassword(p string) { s.mu.Lock(); s.password = p; s.mu.Unlock() }

// Current returns the program scene.
func (s *Server) Current() string { s.mu.Lock(); defer s.mu.Unlock(); return s.current }

// Requests lists every request type received, in order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// FailNext makes the next request of type typ fail with code.
func (s *Server) FailNext(typ string, code int) { s.mu.Lock(); s.failNext[typ] = code; s.mu.Unlock() }

// SwitchByHand changes the scene as the streamer would in OBS.
func (s *Server) SwitchByHand(scene string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = scene
	s.broadcast(1<<2, "CurrentProgramSceneChanged", map[string]any{"sceneName": scene, "sceneUuid": uuidFor(scene)})
}

// Item returns whether source is visible in scene.
func (s *Server) Visible(scene, source string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.scenes {
		if sc.Name == scene {
			for _, it := range sc.Items {
				if it.Source == source {
					return it.Enabled
				}
			}
		}
	}
	return false
}

// InputState returns a copy of an input.
func (s *Server) InputState(name string) (Input, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.inputs[name]
	if !ok {
		return Input{}, false
	}
	return *in, true
}

// FilterEnabled reports a filter's state.
func (s *Server) FilterEnabled(source, filter string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.filters[source] {
		if f.Name == filter {
			return f.Enabled
		}
	}
	return false
}

// Recording reports whether it's recording.
func (s *Server) Recording() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.recording }

// SetReplayBuffer starts or stops the replay buffer.
func (s *Server) SetReplayBuffer(on bool) { s.mu.Lock(); s.replay = on; s.mu.Unlock() }

// LastScreenshot is the path of the last screenshot asked for.
func (s *Server) LastScreenshot() string { s.mu.Lock(); defer s.mu.Unlock(); return s.lastShot }

// StartStream sends the events OBS sends when the streamer goes live.
func (s *Server) StartStream() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcast(1<<6, "StreamStateChanged", map[string]any{"outputActive": false, "outputState": "OBS_WEBSOCKET_OUTPUT_STARTING"})
	s.broadcast(1<<6, "StreamStateChanged", map[string]any{"outputActive": true, "outputState": "OBS_WEBSOCKET_OUTPUT_STARTED"})
}

// Clients counts identified connections.
func (s *Server) Clients() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.clients) }

// Kick disconnects every client with 4011, like OBS's Kick button.
func (s *Server) Kick() { s.closeAll(4011) }

// Drop closes every connection at once, like a network failure.
func (s *Server) Drop() { s.closeAll(websocket.StatusGoingAway) }

// Exit sends ExitStarted and closes, like OBS quitting.
func (s *Server) Exit() {
	s.mu.Lock()
	s.broadcast(1, "ExitStarted", map[string]any{})
	s.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	s.closeAll(websocket.StatusGoingAway)
}

func (s *Server) closeAll(code websocket.StatusCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		c.close(code)
		delete(s.clients, c)
	}
}

func uuidFor(name string) string {
	h := sha256.Sum256([]byte(name))
	return hex.EncodeToString(h[:16])
}

func (s *Server) broadcast(sub int, typ string, data map[string]any) {
	b, _ := json.Marshal(map[string]any{"op": 5, "d": map[string]any{"eventType": typ, "eventIntent": sub, "eventData": data}})
	for c := range s.clients {
		if c.subs&sub != 0 {
			select {
			case c.out <- b:
			default:
			}
		}
	}
}

// ServeHTTP serves the WebSocket at any path, and the /panel page.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/panel" {
		s.panel(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"obswebsocket.json"}})
	if err != nil {
		return
	}
	defer func() { _ = ws.CloseNow() }()
	ctx := r.Context()
	if ws.Subprotocol() != "obswebsocket.json" {
		_ = ws.Close(4002, "use obswebsocket.json")
		return
	}
	s.mu.Lock()
	pw := s.password
	s.mu.Unlock()
	h := map[string]any{"obsStudioVersion": "31.0.0", "obsWebSocketVersion": "5.5.0", "rpcVersion": 1}
	var salt, challenge string
	if pw != "" {
		salt, challenge = randomB64(), randomB64()
		h["authentication"] = map[string]string{"salt": salt, "challenge": challenge}
	}
	if !writeMsg(ctx, ws, 0, h) {
		return
	}
	var m struct {
		Op int `json:"op"`
		D  struct {
			RPCVersion     int    `json:"rpcVersion"`
			Authentication string `json:"authentication"`
			Subs           *int   `json:"eventSubscriptions"`
		} `json:"d"`
	}
	_, data, err := ws.Read(ctx)
	if err != nil || json.Unmarshal(data, &m) != nil || m.Op != 1 {
		_ = ws.Close(4007, "not identified")
		return
	}
	if m.D.RPCVersion != 1 {
		_ = ws.Close(4010, "unsupported rpc version")
		return
	}
	if pw != "" && m.D.Authentication != expected(pw, salt, challenge) {
		_ = ws.Close(4009, "authentication failed")
		return
	}
	subs := 0x7ff
	if m.D.Subs != nil {
		subs = *m.D.Subs
	}
	if !writeMsg(ctx, ws, 2, map[string]any{"negotiatedRpcVersion": 1}) {
		return
	}
	c := &conn{c: ws, out: make(chan []byte, 256), subs: subs, done: make(chan struct{})}
	s.mu.Lock()
	s.clients[c] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
	}()
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for {
			_, data, err := ws.Read(cctx)
			if err != nil {
				cancel()
				return
			}
			s.handle(c, data)
		}
	}()
	for {
		select {
		case <-cctx.Done():
			return
		case <-c.done:
			_ = ws.Close(c.code, "")
			return
		case b := <-c.out:
			wctx, wc := context.WithTimeout(cctx, 5*time.Second)
			err := ws.Write(wctx, websocket.MessageText, b)
			wc()
			if err != nil {
				return
			}
		}
	}
}

func randomB64() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func expected(password, salt, challenge string) string {
	sec := sha256.Sum256([]byte(password + salt))
	a := sha256.Sum256([]byte(base64.StdEncoding.EncodeToString(sec[:]) + challenge))
	return base64.StdEncoding.EncodeToString(a[:])
}

func writeMsg(ctx context.Context, ws *websocket.Conn, op int, d any) bool {
	b, _ := json.Marshal(map[string]any{"op": op, "d": d})
	return ws.Write(ctx, websocket.MessageText, b) == nil
}

// handle answers one request. Events it causes go out first, as OBS may.
func (s *Server) handle(c *conn, data []byte) {
	var m struct {
		Op int `json:"op"`
		D  struct {
			Type string         `json:"requestType"`
			ID   string         `json:"requestId"`
			Data map[string]any `json:"requestData"`
		} `json:"d"`
	}
	if json.Unmarshal(data, &m) != nil || m.Op != 6 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, m.D.Type)
	reply := func(code int, comment string, resp any) {
		status := map[string]any{"result": code == 100, "code": code}
		if comment != "" {
			status["comment"] = comment
		}
		d := map[string]any{"requestType": m.D.Type, "requestId": m.D.ID, "requestStatus": status}
		if resp != nil {
			d["responseData"] = resp
		}
		b, _ := json.Marshal(map[string]any{"op": 7, "d": d})
		select {
		case c.out <- b:
		default:
		}
	}
	if code, ok := s.failNext[m.D.Type]; ok {
		delete(s.failNext, m.D.Type)
		reply(code, "failed by mockobs", nil)
		return
	}
	code, comment, resp := s.answer(m.D.Type, m.D.Data)
	reply(code, comment, resp)
}

func str(d map[string]any, k string) string { v, _ := d[k].(string); return v }

func (s *Server) scene(name string) *Scene {
	for _, sc := range s.scenes {
		if sc.Name == name {
			return sc
		}
	}
	return nil
}

// answer runs a request against the in-memory OBS.
func (s *Server) answer(typ string, d map[string]any) (int, string, any) {
	notFound := func(what string) (int, string, any) {
		return 600, "No source was found by the name of `" + what + "`.", nil
	}
	switch typ {
	case "GetVersion":
		return 100, "", map[string]any{"obsVersion": "31.0.0", "obsWebSocketVersion": "5.5.0", "rpcVersion": 1}
	case "GetCurrentProgramScene":
		return 100, "", map[string]any{"sceneName": s.current, "sceneUuid": uuidFor(s.current), "currentProgramSceneName": s.current}
	case "SetCurrentProgramScene":
		name := str(d, "sceneName")
		if s.scene(name) == nil {
			return notFound(name)
		}
		if s.current != name {
			s.current = name
			s.broadcast(1<<2, "CurrentProgramSceneChanged", map[string]any{"sceneName": name, "sceneUuid": uuidFor(name)})
		}
		return 100, "", nil
	case "GetSceneList":
		list := []any{}
		for i, sc := range s.scenes {
			list = append(list, map[string]any{"sceneName": sc.Name, "sceneUuid": uuidFor(sc.Name), "sceneIndex": i})
		}
		return 100, "", map[string]any{"currentProgramSceneName": s.current, "scenes": list}
	case "GetSceneItemList":
		sc := s.scene(str(d, "sceneName"))
		if sc == nil {
			return notFound(str(d, "sceneName"))
		}
		items := []any{}
		for i, it := range sc.Items {
			items = append(items, map[string]any{"sceneItemId": it.ID, "sourceName": it.Source, "sceneItemEnabled": it.Enabled, "sceneItemIndex": i, "isGroup": nil})
		}
		return 100, "", map[string]any{"sceneItems": items}
	case "GetSceneItemId", "GetSceneItemEnabled", "SetSceneItemEnabled":
		sc := s.scene(str(d, "sceneName"))
		if sc == nil {
			return notFound(str(d, "sceneName"))
		}
		for i := range sc.Items {
			it := &sc.Items[i]
			id, _ := d["sceneItemId"].(float64)
			if (typ == "GetSceneItemId" && it.Source == str(d, "sourceName")) || (typ != "GetSceneItemId" && it.ID == int(id)) {
				switch typ {
				case "GetSceneItemId":
					return 100, "", map[string]any{"sceneItemId": it.ID}
				case "GetSceneItemEnabled":
					return 100, "", map[string]any{"sceneItemEnabled": it.Enabled}
				}
				on, _ := d["sceneItemEnabled"].(bool)
				if it.Enabled != on {
					it.Enabled = on
					s.broadcast(1<<7, "SceneItemEnableStateChanged", map[string]any{"sceneName": sc.Name, "sceneUuid": uuidFor(sc.Name), "sceneItemId": it.ID, "sceneItemEnabled": on})
				}
				return 100, "", nil
			}
		}
		return notFound(str(d, "sourceName"))
	case "GetInputList":
		list := []any{}
		for name, in := range s.inputs {
			list = append(list, map[string]any{"inputName": name, "inputKind": in.Kind})
		}
		return 100, "", map[string]any{"inputs": list}
	case "GetInputMute", "SetInputMute", "ToggleInputMute":
		in, ok := s.inputs[str(d, "inputName")]
		if !ok {
			return notFound(str(d, "inputName"))
		}
		was := in.Muted
		switch typ {
		case "SetInputMute":
			in.Muted, _ = d["inputMuted"].(bool)
		case "ToggleInputMute":
			in.Muted = !in.Muted
		}
		if in.Muted != was {
			s.broadcast(1<<3, "InputMuteStateChanged", map[string]any{"inputName": str(d, "inputName"), "inputMuted": in.Muted})
		}
		return 100, "", map[string]any{"inputMuted": in.Muted}
	case "GetInputSettings":
		in, ok := s.inputs[str(d, "inputName")]
		if !ok {
			return notFound(str(d, "inputName"))
		}
		return 100, "", map[string]any{"inputSettings": in.Settings, "inputKind": in.Kind}
	case "SetInputSettings":
		in, ok := s.inputs[str(d, "inputName")]
		if !ok {
			return notFound(str(d, "inputName"))
		}
		if overlay, ok := d["overlay"].(bool); ok && !overlay {
			in.Settings = map[string]any{}
		}
		set, _ := d["inputSettings"].(map[string]any)
		for k, v := range set {
			in.Settings[k] = v
		}
		return 100, "", nil
	case "GetSourceFilterList", "GetSourceFilter", "SetSourceFilterEnabled":
		source := str(d, "sourceName")
		fs, ok := s.filters[source]
		if !ok && s.scene(source) == nil && s.inputs[source] == nil {
			return notFound(source)
		}
		if typ == "GetSourceFilterList" {
			list := []any{}
			for i, f := range fs {
				list = append(list, map[string]any{"filterName": f.Name, "filterKind": f.Kind, "filterEnabled": f.Enabled, "filterIndex": i})
			}
			return 100, "", map[string]any{"filters": list}
		}
		for _, f := range fs {
			if f.Name == str(d, "filterName") {
				if typ == "GetSourceFilter" {
					return 100, "", map[string]any{"filterEnabled": f.Enabled, "filterKind": f.Kind}
				}
				on, _ := d["filterEnabled"].(bool)
				if f.Enabled != on {
					f.Enabled = on
					s.broadcast(1<<5, "SourceFilterEnableStateChanged", map[string]any{"sourceName": source, "filterName": f.Name, "filterEnabled": on})
				}
				return 100, "", nil
			}
		}
		return 600, "No filter was found by the name of `" + str(d, "filterName") + "`.", nil
	case "TriggerMediaInputAction":
		name := str(d, "inputName")
		if _, ok := s.inputs[name]; !ok {
			return notFound(name)
		}
		switch str(d, "mediaAction") {
		case "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_PLAY", "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_RESTART":
			s.broadcast(1<<8, "MediaInputPlaybackStarted", map[string]any{"inputName": name})
		case "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_STOP":
			s.broadcast(1<<8, "MediaInputPlaybackEnded", map[string]any{"inputName": name})
		case "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_PAUSE", "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_NEXT", "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_PREVIOUS":
		default:
			return 400, "invalid mediaAction", nil
		}
		return 100, "", nil
	case "GetRecordStatus":
		return 100, "", map[string]any{"outputActive": s.recording, "outputPaused": false}
	case "StartRecord":
		if s.recording {
			return 500, "", nil
		}
		s.recording = true
		s.broadcast(1<<6, "RecordStateChanged", map[string]any{"outputActive": true, "outputState": "OBS_WEBSOCKET_OUTPUT_STARTED", "outputPath": nil})
		return 100, "", nil
	case "StopRecord":
		if !s.recording {
			return 501, "", nil
		}
		s.recording = false
		path := "/Users/streamer/Movies/2026-11-08 20-00-00.mkv"
		s.broadcast(1<<6, "RecordStateChanged", map[string]any{"outputActive": false, "outputState": "OBS_WEBSOCKET_OUTPUT_STOPPED", "outputPath": path})
		return 100, "", map[string]any{"outputPath": path}
	case "GetReplayBufferStatus":
		return 100, "", map[string]any{"outputActive": s.replay}
	case "SaveReplayBuffer":
		if !s.replay {
			return 501, "", nil
		}
		s.broadcast(1<<6, "ReplayBufferSaved", map[string]any{"savedReplayPath": "/Users/streamer/Movies/Replay 2026-11-08 20-00-00.mkv"})
		return 100, "", nil
	case "SaveSourceScreenshot":
		name := str(d, "sourceName")
		if s.scene(name) == nil && s.inputs[name] == nil {
			return notFound(name)
		}
		s.lastShot = str(d, "imageFilePath")
		return 100, "", nil
	}
	return 204, "Your request type is not valid.", nil
}

var panelPage = template.Must(template.New("p").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>mockobs</title>
<style>body{font:16px system-ui;max-width:32rem;margin:3rem auto;padding:0 1rem}button{font:inherit;padding:.4rem .7rem;margin:.2rem}
.note{background:#fff3cd;padding:.6rem .8rem;border-radius:6px}.on{font-weight:700}</style></head><body>
<p class="note">This is <strong>mockobs</strong>, a test stand-in for OBS.</p>
<h1>Scenes</h1>
<p>Click a scene to switch to it by hand, as you would in OBS.</p>
<form method="post" action="/panel">{{range .Scenes}}<button name="scene" value="{{.}}"{{if eq . $.Current}} class="on"{{end}}>{{.}}</button>{{end}}</form>
<p>{{.Clients}} StreamGremlin connection(s).</p>
</body></html>`))

func (s *Server) panel(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		_ = r.ParseForm()
		if sc := r.PostForm.Get("scene"); sc != "" {
			s.SwitchByHand(sc)
		}
	}
	s.mu.Lock()
	data := struct {
		Scenes  []string
		Current string
		Clients int
	}{Current: s.current, Clients: len(s.clients)}
	for i := len(s.scenes) - 1; i >= 0; i-- {
		data.Scenes = append(data.Scenes, s.scenes[i].Name)
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = panelPage.Execute(w, data)
}

// String describes the mock, for logs.
func (s *Server) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := []string{}
	for _, sc := range s.scenes {
		names = append(names, sc.Name)
	}
	return fmt.Sprintf("mockobs: %s (on %s)", strings.Join(names, ", "), s.current)
}
