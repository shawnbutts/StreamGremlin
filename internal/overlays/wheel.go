package overlays

import "code.streamgremlin.com/streamgremlin/internal/wheels"

// Wheel overlays (docs/adr/0030-wheels.md) spin to the result the engine
// already drew. Every spin goes to every wheel overlay; the page plays them
// one after another and is otherwise empty.

// KindWheel spins wheels.
const KindWheel = "wheel"

// WheelSpin sends a spin to wheel overlays, pages and previews.
func (h *Hub) WheelSpin(v wheels.SpinView) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b := message("spin", v)
	for _, cs := range h.conns {
		for c := range cs {
			if c.kind == KindWheel {
				h.sendLocked(c, b)
			}
		}
	}
}

// WheelPages counts wheel overlays open in OBS (not the app's previews):
// with none, a spin settles at once.
func (h *Hub) WheelPages() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, cs := range h.conns {
		for c := range cs {
			if c.kind == KindWheel && !c.preview {
				n++
			}
		}
	}
	return n
}

// SampleWheel spins a made-up wheel on one overlay: in the app's previews
// only, or in OBS as well. Nothing is recorded.
func (h *Hub) SampleWheel(name string, previewsOnly bool) (pages, previews int) {
	v := wheels.SpinView{ID: "sample-" + h.now().Format("150405.000"), Title: "A sample wheel", Seconds: 6, Winner: 2, Draw: 4,
		Choices: []wheels.Choice{{Label: "Mario", Weight: 2}, {Label: "Zelda", Weight: 1}, {Label: "Tetris", Weight: 2},
			{Label: "Doom", Weight: 1}, {Label: "Mystery pick", Weight: 1}}}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns[name] {
		if c.kind != KindWheel || (previewsOnly && !c.preview) {
			continue
		}
		if c.preview {
			previews++
		} else {
			pages++
		}
		h.sendLocked(c, message("spin", v))
	}
	return pages, previews
}
