// Package obs connects to OBS Studio over obs-websocket 5: scenes, sources,
// filters, audio, text, media, recording, the replay buffer and screenshots,
// plus scene snapshots that switch back only while StreamGremlin still owns
// the scene (docs/adr/0007-obs.md).
package obs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// DefaultURL is where OBS listens unless the streamer changed it.
const DefaultURL = "ws://127.0.0.1:4455"

// Subscriptions: no high-volume events (volume meters fire every 50 ms).
const (
	subGeneral     = 1 << 0
	subScenes      = 1 << 2
	subInputs      = 1 << 3
	subFilters     = 1 << 5
	subOutputs     = 1 << 6
	subSceneItems  = 1 << 7
	subMediaInputs = 1 << 8
	subscriptions  = subGeneral | subScenes | subInputs | subFilters | subOutputs | subSceneItems | subMediaInputs
)

const requestTimeout = 10 * time.Second

var (
	// ErrAuthFailed: OBS refused the password (close code 4009).
	ErrAuthFailed = errors.New("obs: OBS refused the password")
	// ErrKicked: the streamer disconnected StreamGremlin from OBS's own
	// settings (4011). The protocol says not to reconnect on our own.
	ErrKicked = errors.New("obs: OBS disconnected StreamGremlin (Kick in OBS's WebSocket settings)")
	// ErrUnsupported: OBS's WebSocket server is too old (4010).
	ErrUnsupported = errors.New("obs: this OBS's WebSocket server speaks another protocol version; StreamGremlin needs obs-websocket 5 (OBS 28 or newer)")
	// ErrOutcomeUnknown: the connection was lost after a request was sent,
	// so it may or may not have happened. Never treated as either.
	ErrOutcomeUnknown = errors.New("obs: lost the connection before OBS answered; it may or may not have happened")
	errClosed         = errors.New("obs: the connection is closed")
)

// RequestError is OBS refusing a request.
type RequestError struct {
	Request string
	Code    int
	Comment string
}

func (e *RequestError) Error() string {
	if e.Comment != "" {
		return fmt.Sprintf("obs: %s failed: %s (%d)", e.Request, e.Comment, e.Code)
	}
	return fmt.Sprintf("obs: %s failed (%d)", e.Request, e.Code)
}

// Request status codes used here.
const (
	codeOutputRunning    = 500
	codeOutputNotRunning = 501
	codeResourceNotFound = 600
)

// IsNotFound reports whether OBS said a scene, source or filter doesn't exist.
func IsNotFound(err error) bool {
	var re *RequestError
	return errors.As(err, &re) && re.Code == codeResourceNotFound
}

func isCode(err error, code int) bool {
	var re *RequestError
	return errors.As(err, &re) && re.Code == code
}

// Event is one OBS event.
type Event struct {
	Type string
	Data json.RawMessage
}

type message struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

type hello struct {
	OBSStudioVersion    string `json:"obsStudioVersion"`
	OBSWebSocketVersion string `json:"obsWebSocketVersion"`
	RPCVersion          int    `json:"rpcVersion"`
	Authentication      *struct {
		Challenge string `json:"challenge"`
		Salt      string `json:"salt"`
	} `json:"authentication"`
}

type response struct {
	RequestID     string `json:"requestId"`
	RequestStatus struct {
		Result  bool   `json:"result"`
		Code    int    `json:"code"`
		Comment string `json:"comment"`
	} `json:"requestStatus"`
	ResponseData json.RawMessage `json:"responseData"`
}

// client is one identified obs-websocket session.
type client struct {
	conn   *websocket.Conn
	hello  hello
	events chan Event

	mu      sync.Mutex
	pending map[string]chan response
	err     error
	done    chan struct{}
	nextID  atomic.Uint64
}

// auth computes obs-websocket's challenge answer:
// base64(sha256(base64(sha256(password + salt)) + challenge)).
func auth(password, salt, challenge string) string {
	s := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(s[:])
	a := sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(a[:])
}

// dial connects and identifies. The password is used only for the challenge.
func dial(ctx context.Context, url, password string) (*client, error) {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dctx, url, &websocket.DialOptions{
		Subprotocols: []string{"obswebsocket.json"},
		HTTPHeader:   http.Header{},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("obs: couldn't reach OBS at %s: %w", url, err)
	}
	conn.SetReadLimit(16 << 20)
	c := &client{conn: conn, events: make(chan Event, 1024), pending: map[string]chan response{}, done: make(chan struct{})}

	var h hello
	if err := c.expect(dctx, 0, &h); err != nil {
		_ = conn.CloseNow()
		return nil, err
	}
	c.hello = h
	id := map[string]any{"rpcVersion": 1, "eventSubscriptions": subscriptions}
	if h.Authentication != nil {
		if password == "" {
			_ = conn.CloseNow()
			return nil, fmt.Errorf("%w: OBS asks for a password, and none is saved", ErrAuthFailed)
		}
		id["authentication"] = auth(password, h.Authentication.Salt, h.Authentication.Challenge)
	}
	if err := c.write(dctx, 1, id); err != nil {
		_ = conn.CloseNow()
		return nil, err
	}
	if err := c.expect(dctx, 2, nil); err != nil {
		_ = conn.CloseNow()
		return nil, err
	}
	go c.readLoop() //nolint:gosec // the session outlives the dial context; close() and fail() end it
	return c, nil
}

func (c *client) write(ctx context.Context, op int, d any) error {
	b, err := json.Marshal(map[string]any{"op": op, "d": d})
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, b)
}

// expect reads one handshake message of the given op.
func (c *client) expect(ctx context.Context, op int, into any) error {
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return closeErr(err)
	}
	var m message
	if err := json.Unmarshal(data, &m); err != nil || m.Op != op {
		return fmt.Errorf("obs: unexpected message during the handshake (op %d)", m.Op)
	}
	if into != nil {
		return json.Unmarshal(m.D, into)
	}
	return nil
}

func closeErr(err error) error {
	switch websocket.CloseStatus(err) {
	case 4009:
		return ErrAuthFailed
	case 4010:
		return ErrUnsupported
	case 4011:
		return ErrKicked
	case websocket.StatusGoingAway:
		return errors.New("obs: OBS closed the connection (shutting down?)")
	case -1:
		return fmt.Errorf("obs: the connection was lost: %w", err)
	}
	return fmt.Errorf("obs: OBS closed the connection: %w", err)
}

func (c *client) readLoop() {
	for {
		_, data, err := c.conn.Read(context.Background())
		if err != nil {
			c.fail(closeErr(err))
			return
		}
		var m message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Op {
		case 5:
			var e struct {
				EventType string          `json:"eventType"`
				EventData json.RawMessage `json:"eventData"`
			}
			if json.Unmarshal(m.D, &e) != nil {
				continue
			}
			select {
			case c.events <- Event{Type: e.EventType, Data: e.EventData}:
			case <-c.done:
				return
			default:
				// Never lose events silently: drop the session and resync.
				c.fail(errors.New("obs: fell behind on OBS's events; reconnecting to catch up"))
				return
			}
		case 7:
			var r response
			if json.Unmarshal(m.D, &r) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[r.RequestID]
			delete(c.pending, r.RequestID)
			c.mu.Unlock()
			if ch != nil {
				ch <- r
			}
		}
	}
}

// fail ends the session once, waking every waiting request.
func (c *client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	close(c.done) // readers of events select on done; events is never closed
	_ = c.conn.CloseNow()
}

func (c *client) close() {
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	c.fail(errClosed)
}

// request sends one request and decodes its answer into out.
func (c *client) request(ctx context.Context, typ string, data, out any) error {
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	d := map[string]any{"requestType": typ, "requestId": id}
	if data != nil {
		d["requestData"] = data
	}
	if err := c.write(ctx, 6, d); err != nil {
		return ErrOutcomeUnknown
	}
	select {
	case r := <-ch:
		if !r.RequestStatus.Result {
			return &RequestError{Request: typ, Code: r.RequestStatus.Code, Comment: r.RequestStatus.Comment}
		}
		if out != nil && len(r.ResponseData) > 0 {
			return json.Unmarshal(r.ResponseData, out)
		}
		return nil
	case <-c.done:
		return ErrOutcomeUnknown
	case <-ctx.Done():
		return ErrOutcomeUnknown
	}
}
