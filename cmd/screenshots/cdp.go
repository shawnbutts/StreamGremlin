package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// browser is a headless Chromium driven over the DevTools protocol: just
// enough to open a page, run a line of script and take a picture.
type browser struct {
	cmd  *exec.Cmd
	dir  string
	conn *websocket.Conn

	mu      sync.Mutex
	next    int
	waiting map[int]chan reply
}

type reply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// findChrome is $CHROME, or a Chromium or Chrome in the usual places.
func findChrome() (string, error) {
	if p := os.Getenv("CHROME"); p != "" {
		return p, nil
	}
	for _, p := range []string{
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"chromium", "chromium-browser", "google-chrome",
	} {
		if q, err := exec.LookPath(p); err == nil {
			return q, nil
		}
	}
	return "", errors.New("no Chromium or Chrome found: set CHROME to one")
}

func startBrowser(ctx context.Context, width, height int, scale float64) (*browser, error) {
	exe, err := findChrome()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "sg-shots-chrome-")
	if err != nil {
		return nil, err
	}
	b := &browser{dir: dir, waiting: map[int]chan reply{}}
	b.cmd = exec.CommandContext(ctx, exe, //nolint:gosec // the browser the developer chose "--headless=new", "--remote-debugging-port=0", "--user-data-dir="+dir,
		"--no-first-run", "--no-default-browser-check", "--hide-scrollbars", "--mute-audio",
		"--disable-extensions", "--disable-background-networking", "--disable-sync", "--lang=en-US",
		"--font-render-hinting=none", "about:blank")
	if err := b.cmd.Start(); err != nil {
		return nil, err
	}
	// Chromium writes the port it chose to DevToolsActivePort.
	var port string
	for i := 0; i < 100 && port == ""; i++ {
		if raw, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort")); err == nil {
			port = strings.SplitN(string(raw), "\n", 2)[0]
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if port == "" {
		b.close()
		return nil, errors.New("chromium didn't start its DevTools server")
	}
	resp, err := http.Get("http://127.0.0.1:" + port + "/json/list")
	if err != nil {
		b.close()
		return nil, err
	}
	var targets []struct {
		Type string `json:"type"`
		WS   string `json:"webSocketDebuggerUrl"`
	}
	defer func() { _ = resp.Body.Close() }()
	err = json.NewDecoder(resp.Body).Decode(&targets)
	ws := ""
	for _, t := range targets {
		if t.Type == "page" {
			ws = t.WS
			break
		}
	}
	if err != nil || ws == "" {
		b.close()
		return nil, fmt.Errorf("no page to drive: %w", err)
	}
	var wsResp *http.Response
	b.conn, wsResp, err = websocket.Dial(ctx, ws, nil)
	if wsResp != nil && wsResp.Body != nil {
		_ = wsResp.Body.Close()
	}
	if err != nil {
		b.close()
		return nil, err
	}
	b.conn.SetReadLimit(64 << 20)
	go b.read(ctx)
	if _, err := b.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": height, "deviceScaleFactor": scale, "mobile": false}); err != nil {
		b.close()
		return nil, err
	}
	return b, nil
}

func (b *browser) read(ctx context.Context) {
	for {
		_, raw, err := b.conn.Read(ctx)
		if err != nil {
			b.mu.Lock()
			for id, ch := range b.waiting {
				close(ch)
				delete(b.waiting, id)
			}
			b.mu.Unlock()
			return
		}
		var msg struct {
			ID int `json:"id"`
			reply
		}
		if json.Unmarshal(raw, &msg) != nil || msg.ID == 0 {
			continue // an event
		}
		b.mu.Lock()
		ch := b.waiting[msg.ID]
		delete(b.waiting, msg.ID)
		b.mu.Unlock()
		if ch != nil {
			ch <- msg.reply
		}
	}
}

func (b *browser) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	b.mu.Lock()
	b.next++
	id := b.next
	ch := make(chan reply, 1)
	b.waiting[id] = ch
	b.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := b.conn.Write(ctx, websocket.MessageText, raw); err != nil {
		return nil, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, errors.New("chromium went away")
		}
		if r.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, r.Error.Message)
		}
		return r.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *browser) navigate(ctx context.Context, url string) error {
	_, err := b.call(ctx, "Page.navigate", map[string]any{"url": url})
	return err
}

// eval runs script in the page, waiting for a promise, and returns its
// value as JSON. A thrown error is an error.
func (b *browser) eval(ctx context.Context, script string) (json.RawMessage, error) {
	raw, err := b.call(ctx, "Runtime.evaluate", map[string]any{"expression": script, "awaitPromise": true, "returnByValue": true})
	if err != nil {
		return nil, err
	}
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.Exception != nil {
		return nil, fmt.Errorf("script: %s %s", out.Exception.Text, out.Exception.Exception.Description)
	}
	return out.Result.Value, nil
}

// shoot saves the page as WebP.
func (b *browser) shoot(ctx context.Context, path string) error {
	raw, err := b.call(ctx, "Page.captureScreenshot", map[string]any{"format": "webp", "quality": 88})
	if err != nil {
		return err
	}
	var out struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	img, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, img, 0o644) //nolint:gosec // pictures for the website, read by all
}

func (b *browser) close() {
	if b.conn != nil {
		_ = b.conn.Close(websocket.StatusNormalClosure, "")
	}
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
		_ = b.cmd.Wait()
	}
	_ = os.RemoveAll(b.dir)
}
