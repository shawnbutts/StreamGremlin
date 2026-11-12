package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

var (
	laptop  = Bounds{X: 0, Y: 25, Width: 1440, Height: 875}    // work area under the menu bar
	monitor = Bounds{X: 1440, Y: 0, Width: 2560, Height: 1400} // a second screen to the right
	minSize = Size{Width: 720, Height: 520}
)

// A saved window comes back where it was when that spot is still on a
// screen; it's kept on screen and at least the minimum size; off every
// screen (a monitor unplugged), it isn't placed at all, and opens centred.
func TestPlace(t *testing.T) {
	for _, c := range []struct {
		name  string
		saved Bounds
		want  Bounds
		ok    bool
	}{
		{"as it was", Bounds{100, 80, 1200, 800}, Bounds{100, 80, 1200, 800}, true},
		{"on the second screen", Bounds{2000, 100, 1600, 1000}, Bounds{2000, 100, 1600, 1000}, true},
		{"too small", Bounds{100, 80, 300, 200}, Bounds{100, 80, 720, 520}, true},
		{"taller than the screen", Bounds{0, 25, 1400, 2000}, Bounds{0, 25, 1400, 875}, true},
		{"hanging off the right", Bounds{3500, 100, 1200, 800}, Bounds{2800, 100, 1200, 800}, true},
		{"across two screens: the one with more of it", Bounds{1000, 100, 1200, 800}, Bounds{1440, 100, 1200, 800}, true},
		{"title bar above the screen", Bounds{100, -300, 1200, 800}, Bounds{100, 25, 1200, 800}, true},
		{"on a screen that's gone", Bounds{5000, 100, 1200, 800}, Bounds{}, false},
		{"barely a corner showing", Bounds{-1180, 820, 1200, 800}, Bounds{}, false},
		{"nothing saved", Bounds{}, Bounds{}, false},
	} {
		got, ok := Place(c.saved, []Bounds{laptop, monitor}, minSize)
		if ok != c.ok || ok && got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
	if _, ok := Place(Bounds{100, 80, 1200, 800}, nil, minSize); ok {
		t.Error("no screens known: placed anyway")
	}
}

// Saved and read back; missing, damaged or absurd files are ignored; the
// file is the user's alone.
func TestSaveLoadWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.json")
	if _, ok := LoadWindow(path); ok {
		t.Fatal("loaded nothing")
	}
	want := WindowState{Bounds: Bounds{X: 100, Y: 80, Width: 1200, Height: 800}, Maximised: true}
	if err := SaveWindow(path, want); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoadWindow(path); !ok || got != want {
		t.Fatal(got, ok)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatal(fi.Mode())
		}
	}
	for _, bad := range []string{"{", `{"width":-5,"height":800}`, `{"width":99999999,"height":800}`, `[]`} {
		_ = os.WriteFile(path, []byte(bad), 0o600)
		if _, ok := LoadWindow(path); ok {
			t.Errorf("%s loaded", bad)
		}
	}
}

// Moving and resizing save once things settle, not on every step; closing
// saves at once; a minimised window isn't saved; maximising keeps the size
// to come back to.
func TestWindowSaver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.json")
	var mu sync.Mutex
	writes := 0
	s := NewWindowSaver(path, 30*time.Millisecond, func(err error) { t.Error(err) })
	s.onWrite = func() { mu.Lock(); writes++; mu.Unlock() }
	for i := range 10 {
		s.Note(Bounds{X: 100 + i, Y: 80, Width: 1200, Height: 800}, false, false)
	}
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	if writes != 1 {
		t.Fatalf("writes %d", writes)
	}
	mu.Unlock()
	if got, _ := LoadWindow(path); got.X != 109 {
		t.Fatal(got)
	}
	// Minimised: what's saved stays.
	s.Note(Bounds{X: -32000, Y: -32000, Width: 160, Height: 28}, false, true)
	// Maximised: the normal size is kept, with the flag.
	s.Note(Bounds{X: 0, Y: 25, Width: 1440, Height: 875}, true, false)
	s.Flush()
	if got, _ := LoadWindow(path); got.Bounds != (Bounds{X: 109, Y: 80, Width: 1200, Height: 800}) || !got.Maximised {
		t.Fatal(got)
	}
	s.Note(Bounds{X: 50, Y: 60, Width: 1000, Height: 700}, false, false)
	s.Flush() // closing: at once
	if got, _ := LoadWindow(path); got.X != 50 || got.Maximised {
		t.Fatal(got)
	}
}
