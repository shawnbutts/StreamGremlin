// Command screenshots photographs the desktop app for the website and its
// Help (M9 slice 4, D-112). It starts a throwaway engine against the
// stand-in Twitch, in a temporary folder with a file secret store (never
// the real keyring or the streamer's data folder), fills it with made-up
// streamers, viewers and chat through the same API the app uses, serves
// the built app to headless Chromium, and saves one picture a screen.
//
//	go run ./cmd/screenshots -out website/screenshots
//
// It needs the built app (make desktop-ui) and Chromium or Chrome (CHROME
// names one). -keep leaves it all running, to look around in a browser.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/desktop"
	"code.streamgremlin.com/streamgremlin/internal/host"
	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
)

// The window size the pictures are taken at, in CSS pixels, and the pixel
// ratio: twice, so they're sharp on high-density screens.
const (
	width  = 1280
	height = 800
	scale  = 2
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "screenshots:", err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("out", "website/screenshots", "where to save the pictures")
	ui := flag.String("ui", "cmd/desktop/frontend/dist", "the built app")
	only := flag.String("only", "", "take only these pictures (comma-separated names)")
	keep := flag.Bool("keep", false, "keep running after the pictures, to look around")
	flag.Parse()

	if _, err := os.Stat(filepath.Join(*ui, "index.html")); err != nil {
		return fmt.Errorf("no built app in %s: run make desktop-ui first", *ui)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil { //nolint:gosec // pictures for the website, read by all
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dir, err := os.MkdirTemp("", "sg-shots-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	tw := mocktwitch.New()
	twSrv := httptest.NewServer(tw)
	defer twSrv.Close()
	tw.SetBase(twSrv.URL)

	h, err := host.Start(ctx, host.Options{
		DataDir: dir, Addr: "127.0.0.1:0", OverlayAddr: "127.0.0.1:0",
		SecretStore: secrets.KindFile, TwitchAuthURL: twSrv.URL,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.Close(c)
	}()
	c, err := client.ForDataDir(dir)
	if err != nil {
		return err
	}

	fmt.Println("Filling a made-up channel…")
	if err := seed(ctx, c, tw); err != nil {
		return fmt.Errorf("seeding: %w", err)
	}

	state := desktop.NewState()
	state.Ready(h)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	appURL := "http://" + ln.Addr().String()
	noop := func() error { return nil }
	srv := &http.Server{
		Handler:           desktop.Handler(os.DirFS(*ui), state, func(string) error { return nil }, noop),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	b, err := startBrowser(ctx, width, height, scale)
	if err != nil {
		return err
	}
	defer b.close()

	want := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	for _, s := range shots {
		if len(want) > 0 && !want[s.name] {
			continue
		}
		if s.before != nil {
			if err := s.before(ctx, c, tw); err != nil {
				return fmt.Errorf("%s: %w", s.name, err)
			}
		}
		if err := take(ctx, b, appURL, s, filepath.Join(*out, s.name+".webp")); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		fmt.Println("  ", s.name)
	}

	if *keep {
		fmt.Printf("The app is at %s, the stand-in Twitch's chat at %s/chat. Ctrl-C stops.\n", appURL, twSrv.URL)
		<-ctx.Done()
	}
	return nil
}

// take opens the app afresh, goes to the shot's screen, runs its steps,
// waits for the screen to settle and saves the picture.
func take(ctx context.Context, b *browser, appURL string, s shot, path string) error {
	if err := b.navigate(ctx, appURL+"/"); err != nil {
		return err
	}
	if err := waitFor(ctx, b, `document.querySelector('nav') !== null && !document.body.innerText.includes('is starting')`); err != nil {
		return errors.New("the app didn't open")
	}
	if _, err := b.eval(ctx, helpers); err != nil {
		return err
	}
	if _, err := b.eval(ctx, fmt.Sprintf(`__sg.go(%q)`, s.screen)); err != nil {
		return err
	}
	pause(ctx, 1200)
	for _, step := range s.steps {
		if _, err := b.eval(ctx, step); err != nil {
			return fmt.Errorf("step %q: %w", step, err)
		}
		pause(ctx, 700)
	}
	pause(ctx, 600)
	return b.shoot(ctx, path)
}

func waitFor(ctx context.Context, b *browser, cond string) error {
	for i := 0; i < 100; i++ {
		raw, err := b.eval(ctx, cond)
		var ok bool
		if err == nil && json.Unmarshal(raw, &ok) == nil && ok {
			return nil
		}
		pause(ctx, 100)
	}
	return errors.New("timed out")
}

func pause(ctx context.Context, ms int) {
	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
	case <-ctx.Done():
	}
}

// helpers are a few lines of script the shots use: go to a screen by its
// sidebar label (opening More… if it's there), click a button by its
// words, and hide something.
const helpers = `window.__sg = {
  buttons() { return [...document.querySelectorAll('button, a, summary, [role=tab]')]; },
  find(text, within) {
    const root = within ? document.querySelector(within) : document;
    return [...root.querySelectorAll('button, a, summary, [role=tab]')].find((b) => b.textContent.trim() === text)
      ?? [...root.querySelectorAll('button, a, summary, [role=tab]')].find((b) => b.textContent.trim().startsWith(text));
  },
  async go(label) {
    let b = this.find(label, 'nav');
    if (!b) {
      document.querySelector('nav .nav-more')?.click();
      await new Promise((r) => setTimeout(r, 300));
      b = this.find(label, 'nav');
    }
    if (!b) throw new Error('no screen ' + label);
    b.click();
  },
  click(text, within) {
    const b = this.find(text, within);
    if (!b) throw new Error('no button ' + text);
    b.click();
  },
  nth(text, i) {
    const b = [...document.querySelectorAll('button')].filter((b) => b.textContent.trim() === text)[i];
    if (!b) throw new Error('no button ' + text + ' ' + i);
    return b;
  },
  hide(sel) { document.querySelectorAll(sel).forEach((e) => (e.style.display = 'none')); },
};`
