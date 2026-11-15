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
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"code.streamgremlin.com/streamgremlin/internal/brand"
	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/desktop"
	"code.streamgremlin.com/streamgremlin/internal/host"
	"code.streamgremlin.com/streamgremlin/internal/logging"
	"code.streamgremlin.com/streamgremlin/internal/lua"
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
	lua.RunWorkerIfAsked() // the engine runs Lua scripts in copies of the app
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
	// A file's path, chosen with the system's own open dialog: only the
	// path goes back to the app; the engine checks it when it's saved.
	chooseFile := func(title, filter, pattern string) (string, error) {
		d := app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(true).CanChooseDirectories(false).AddFilter(filter, pattern)
		if win != nil {
			d = d.AttachToWindow(win)
		}
		return d.PromptForSingleSelection()
	}
	app = application.New(application.Options{
		Name:        brand.Name,
		Description: "Local-first stream automation",
		Icon:        icon,
		Logger:      log,
		Assets: application.AssetOptions{
			Handler: desktop.Handler(assets, state, openURL, func() error { return eng.restart(dir, state) },
				desktop.Dialogs{Save: saveFile, Choose: chooseFile}),
			DisableLogging: level != slog.LevelDebug,
		},
		Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		// Matches the Linux package's .desktop file, so the window gets its
		// icon and groups with the launcher.
		Linux: application.LinuxOptions{ApplicationID: brand.LinuxAppID},
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
	// The window opens where it was last time (D-082): hidden until it's
	// placed, when there's a saved place, so it doesn't jump.
	windowFile := filepath.Join(dir, "window.json")
	saved, haveSaved := desktop.LoadWindow(windowFile)
	saver := desktop.NewWindowSaver(windowFile, 500*time.Millisecond, func(err error) { log.Warn("couldn't save the window's place", "err", err) })
	if haveSaved {
		saver.Start(saved)
	}
	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            brand.Name,
		Width:            1100,
		Height:           760,
		MinWidth:         minWindow.Width,
		MinHeight:        minWindow.Height,
		Hidden:           haveSaved,
		URL:              "/",
		BackgroundColour: application.NewRGB(0x08, 0x19, 0x23),
		Linux:            application.LinuxWindow{Icon: icon},
		Mac: application.MacWindow{
			WebviewPreferences: application.MacWebviewPreferences{EnableAutoplayWithoutUserAction: application.Enabled},
		},
	})

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// Off the main thread: changing the window waits for it.
		go placeWindow(app, win, saved, haveSaved)
	})
	note := func(*application.WindowEvent) {
		go saver.Note(desktop.Bounds(win.Bounds()), win.IsMaximised(), win.IsMinimised() || win.IsFullscreen())
	}
	for _, ev := range []events.WindowEventType{events.Common.WindowDidMove, events.Common.WindowDidResize, events.Common.WindowMaximise, events.Common.WindowRestore} {
		win.OnWindowEvent(ev, note)
	}
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { saver.Flush() })

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
		DataDir: dir, TwitchAuthURL: os.Getenv(brand.Env("TWITCH_AUTH_URL")),
		YouTubeURL: os.Getenv(brand.Env("YOUTUBE_URL")), Logger: e.log,
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

// minWindow is the smallest the window goes.
var minWindow = desktop.Size{Width: 720, Height: 520}

// placeWindow puts the window back where it was, when that's still on a
// screen; otherwise it stays where the system put it. Then it shows.
func placeWindow(app *application.App, win *application.WebviewWindow, saved desktop.WindowState, haveSaved bool) {
	if !haveSaved {
		return
	}
	var screens []desktop.Bounds
	for _, s := range app.Screen.GetAll() {
		screens = append(screens, desktop.Bounds(s.WorkArea))
	}
	if b, ok := desktop.Place(saved.Bounds, screens, minWindow); ok {
		win.SetBounds(application.Rect(b))
	} else {
		win.Center()
	}
	if saved.Maximised {
		win.Maximise()
	}
	win.Show()
}
