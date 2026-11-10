// Command desktop is the StreamGremlin desktop app: the engine plus a window.
// All Wails code lives here; the UI reaches the engine only through the
// local API, bridged in-process by internal/desktop (docs/adr/0002-local-api.md).
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/desktop"
	"code.streamgremlin.com/streamgremlin/internal/host"
	"code.streamgremlin.com/streamgremlin/internal/logging"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

//go:embed all:frontend/dist
var dist embed.FS

// appicon.png is a copy of brand-kit/streamgremlin-icon-1024.png; a test
// keeps them identical.
//
//go:embed build/appicon.png
var icon []byte

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	assets, err := fs.Sub(dist, "frontend/dist")
	if err != nil {
		return err
	}
	dir, err := datadir.Default()
	if err != nil {
		return err
	}
	if err := datadir.Ensure(dir); err != nil {
		return err
	}
	level := slog.LevelInfo
	if os.Getenv(brand.Env("DESKTOP_DEBUG")) == "1" {
		level = slog.LevelDebug
	}
	log, closeLog, err := logging.Open(datadir.LogDir(dir), "desktop", level)
	if err != nil {
		return err
	}
	defer func() { _ = closeLog() }()

	state := desktop.NewState()
	eng := &engine{log: log}

	var win *application.WebviewWindow
	var app *application.App
	// The UI asks for allow-listed pages only (desktop.AllowedURL); they open
	// in the streamer's own browser, never inside the app.
	openURL := func(u string) error { return app.Browser.OpenURL(u) }
	// Exports go where the streamer chooses, with the system's own save
	// dialog; nothing is written without it.
	saveFile := func(name string, data []byte) (string, error) {
		d := app.Dialog.SaveFile().SetFilename(name).CanCreateDirectories(true)
		if win != nil {
			d = d.AttachToWindow(win)
		}
		path, err := d.PromptForSingleSelection()
		if err != nil || path == "" {
			return "", err
		}
		return path, os.WriteFile(path, data, 0o600)
	}
	app = application.New(application.Options{
		Name:        brand.Name,
		Description: "Local-first stream automation",
		Icon:        icon,
		Logger:      log,
		Assets: application.AssetOptions{
			Handler:        desktop.Handler(assets, state, openURL, func() error { return eng.restart(dir, state) }, saveFile),
			DisableLogging: level != slog.LevelDebug,
		},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		// The window plays sounds for workflows, with no click first
		// (docs/adr/0016-sounds.md). macOS's is set on the window below.
		Windows: application.WindowsOptions{AdditionalBrowserArgs: []string{"--autoplay-policy=no-user-gesture-required"}},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.streamgremlin.desktop",
			// Opening the app again brings the running window forward.
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if win != nil {
					win.Restore()
					win.Show()
					win.Focus()
				}
			},
		},
		OnShutdown: eng.stop,
	})
	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            brand.Name,
		Width:            1100,
		Height:           760,
		MinWidth:         720,
		MinHeight:        520,
		URL:              "/",
		BackgroundColour: application.NewRGB(0x08, 0x19, 0x23),
		Mac: application.MacWindow{
			WebviewPreferences: application.MacWebviewPreferences{EnableAutoplayWithoutUserAction: application.Enabled},
		},
	})

	go eng.start(dir, state)
	return app.Run()
}

// engine starts the host in the background so the window opens at once,
// and stops it when the app quits, even if it quits while starting.
type engine struct {
	log     *slog.Logger
	mu      sync.Mutex
	h       *host.Host
	stopped bool
}

func (e *engine) start(dir string, state *desktop.State) {
	// STREAMGREMLIN_TWITCH_AUTH_URL points sign-in at mocktwitch, for trying
	// the app without a Twitch app; the Connections screen says so.
	h, err := host.Start(context.Background(), host.Options{
		DataDir: dir, TwitchAuthURL: os.Getenv(brand.Env("TWITCH_AUTH_URL")), Logger: e.log,
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.log.Error("engine failed to start", "err", err)
		state.Failed(explain(err, dir))
		return
	}
	if e.stopped {
		e.close(h)
		return
	}
	e.h = h
	state.Ready(h)
}

// restart stops the engine and starts it again, for a restore chosen in
// the app: it's applied as the engine starts.
func (e *engine) restart(dir string, state *desktop.State) error {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return errors.New("StreamGremlin is closing")
	}
	h := e.h
	e.h = nil
	state.Starting()
	e.mu.Unlock()
	if h != nil {
		e.close(h)
	}
	e.log.Info("restarting the engine")
	go e.start(dir, state)
	return nil
}

func (e *engine) stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopped = true
	if e.h != nil {
		e.close(e.h)
		e.h = nil
	}
}

func (e *engine) close(h *host.Host) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Close(ctx); err != nil {
		e.log.Error("engine stopped with an error", "err", err)
	}
}

// explain turns a start-up error into words for the streamer.
func explain(err error, dir string) string {
	switch {
	case errors.Is(err, datadir.ErrLocked):
		return "Another copy of StreamGremlin is already using your data folder (" + dir +
			"). Close it, then open StreamGremlin again."
	case errors.Is(err, storage.ErrDatabaseNewer):
		return "Your data was saved by a newer version of StreamGremlin. Install that version again to use it."
	case errors.Is(err, storage.ErrChecksumMismatch):
		return "Your database was made by a different development build. " + err.Error()
	default:
		return "Something went wrong while starting: " + err.Error() +
			". The log is in " + datadir.LogDir(dir) + "."
	}
}
