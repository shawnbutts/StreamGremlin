package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The window's size and place are remembered between runs (D-082): saved
// to <data>/window.json as the window moves and resizes, and put back at
// the next start when that spot is still on a screen. This is the desktop
// shell's own state, not the engine's: nothing else reads it.

// Bounds is a window's or a screen's rectangle, in the desktop's
// device-independent pixels.
type Bounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Size is a width and height.
type Size struct{ Width, Height int }

// WindowState is what's remembered: the window's normal bounds, and
// whether it was maximised.
type WindowState struct {
	Bounds
	Maximised bool `json:"maximised"`
}

// Enough of a window must show on a screen to grab it again.
const (
	minVisibleWidth  = 120
	minVisibleHeight = 40
	maxSide          = 100_000
)

func overlap(a, b Bounds) (int, int) {
	w := min(a.X+a.Width, b.X+b.Width) - max(a.X, b.X)
	h := min(a.Y+a.Height, b.Y+b.Height) - max(a.Y, b.Y)
	return max(w, 0), max(h, 0)
}

// Place puts saved back on the screen it most overlaps (screens are their
// work areas), at least minSize and no bigger than that screen, moved in
// so it's whole. It answers false when too little of it would show on any
// screen, such as a monitor that's no longer there: then the window opens
// where the system puts it.
func Place(saved Bounds, screens []Bounds, minSize Size) (Bounds, bool) {
	if saved.Width <= 0 || saved.Height <= 0 {
		return Bounds{}, false
	}
	best, bestArea := -1, 0
	for i, s := range screens {
		w, h := overlap(saved, s)
		if w >= minVisibleWidth && h >= minVisibleHeight && w*h > bestArea {
			best, bestArea = i, w*h
		}
	}
	if best < 0 {
		return Bounds{}, false
	}
	s := screens[best]
	b := saved
	b.Width = min(max(b.Width, minSize.Width), s.Width)
	b.Height = min(max(b.Height, minSize.Height), s.Height)
	b.X = min(max(b.X, s.X), s.X+s.Width-b.Width)
	b.Y = min(max(b.Y, s.Y), s.Y+s.Height-b.Height)
	return b, true
}

// LoadWindow reads the saved window; false when there's none or it's damaged.
func LoadWindow(path string) (WindowState, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return WindowState{}, false
	}
	var w WindowState
	if err := json.Unmarshal(raw, &w); err != nil {
		return WindowState{}, false
	}
	if w.Width <= 0 || w.Height <= 0 || w.Width > maxSide || w.Height > maxSide || abs(w.X) > maxSide || abs(w.Y) > maxSide {
		return WindowState{}, false
	}
	return w, true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// SaveWindow writes the window's state (mode 0600), whole or not at all.
func SaveWindow(path string, w WindowState) error {
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".window-*.json")
	if err != nil {
		return fmt.Errorf("desktop: save the window: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("desktop: save the window: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("desktop: save the window: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("desktop: save the window: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}

// WindowSaver saves the window's state once moving or resizing has
// settled, and at once when asked (closing).
type WindowSaver struct {
	path   string
	settle time.Duration
	report func(error)

	mu      sync.Mutex
	state   WindowState
	have    bool
	timer   *time.Timer
	onWrite func() // tests count writes
}

// NewWindowSaver saves to path, settle after the last change; report
// hears failed writes.
func NewWindowSaver(path string, settle time.Duration, report func(error)) *WindowSaver {
	return &WindowSaver{path: path, settle: settle, report: report}
}

// Start begins from a state loaded at start, so maximising before any
// move keeps the size to come back to.
func (s *WindowSaver) Start(w WindowState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.have = w, true
}

// Note records the window as it is now. A minimised window changes
// nothing; a maximised one keeps the last normal bounds.
func (s *WindowSaver) Note(b Bounds, maximised, minimised bool) {
	if minimised {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !maximised && b.Width > 0 && b.Height > 0 {
		s.state.Bounds = b
	}
	s.state.Maximised = maximised
	s.have = s.state.Width > 0
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.settle, s.write)
}

// Flush saves now, for closing.
func (s *WindowSaver) Flush() {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	s.write()
}

func (s *WindowSaver) write() {
	s.mu.Lock()
	state, have, onWrite := s.state, s.have, s.onWrite
	s.mu.Unlock()
	if !have {
		return
	}
	if err := SaveWindow(s.path, state); err != nil && s.report != nil {
		s.report(err)
	}
	if onWrite != nil {
		onWrite()
	}
}
