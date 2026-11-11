// Package themesounds makes the built-in wheel looks' sounds
// (docs/adr/0036-packages-plugins-and-themes.md, D-065). They're
// synthesised here, from StreamGremlin's own code, so where every sample
// came from is plain: no recordings, no outside sound packs. The files in
// internal/overlays/themes/<look>/ are this package's output; its test
// checks they still match, and rewrites them with -update.
package themesounds

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Rate is the sample rate: mono, 16-bit, 22,050 a second, which keeps the
// files small and is plenty for short effects.
const Rate = 22050

// Cues are the sounds each look has, played by the overlay page as a spin
// starts, as it stops, and as the result shows.
var Cues = []string{"start", "end", "cheer"}

// Looks are the built-in looks with sounds, by folder.
var Looks = []string{"workshop", "neon-arcade", "selector-board"}

// Files returns every look's sounds as WAV files, by path under
// internal/overlays/themes/, like "workshop/start.wav". The same bytes
// every time.
func Files() map[string][]byte {
	makers := map[string]map[string]func() []float64{
		"workshop":       {"start": workshopStart, "end": workshopEnd, "cheer": workshopCheer},
		"neon-arcade":    {"start": neonStart, "end": neonEnd, "cheer": neonCheer},
		"selector-board": {"start": boardStart, "end": boardEnd, "cheer": boardCheer},
	}
	out := map[string][]byte{}
	for look, cues := range makers {
		for cue, gen := range cues {
			out[look+"/"+cue+".wav"] = wav(finish(gen()))
		}
	}
	return out
}

// --- building blocks --------------------------------------------------------

func samples(seconds float64) []float64 { return make([]float64, int(seconds*Rate)) }

// noise is a fixed sequence (xorshift), so the files never change. Not for
// anything a viewer could win: it makes clicks.
type noise uint32

func (n *noise) next() float64 {
	x := uint32(*n)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*n = noise(x)
	return float64(x)/float64(math.MaxUint32)*2 - 1
}

// wave shapes, for a phase in turns.
func sine(p float64) float64 { return math.Sin(2 * math.Pi * p) }

func square(duty float64) func(float64) float64 {
	return func(p float64) float64 {
		if p-math.Floor(p) < duty {
			return 1
		}
		return -1
	}
}

func triangle(p float64) float64 {
	f := p - math.Floor(p)
	return 4*math.Abs(f-0.5) - 1
}

// tone adds a note at a time: shape at freq(t) Hz, rising over attack
// seconds and dying away with decay (the time to fall to about a third).
func tone(buf []float64, at, length, amp, attack, decay float64, shape func(float64) float64, freq func(t float64) float64) {
	start := int(at * Rate)
	phase := 0.0
	for i := 0; i < int(length*Rate) && start+i < len(buf); i++ {
		t := float64(i) / Rate
		env := math.Exp(-t / decay)
		if attack > 0 && t < attack {
			env *= t / attack
		}
		buf[start+i] += amp * env * shape(phase)
		phase += freq(t) / Rate
	}
}

func fixed(hz float64) func(float64) float64 { return func(float64) float64 { return hz } }

// burst adds a short noise burst, smoothed toward a lower pitch by tone
// (0 bright, nearer 1 duller): the body of a click.
func burst(buf []float64, n *noise, at, length, amp, decay, dull float64) {
	start := int(at * Rate)
	prev := 0.0
	for i := 0; i < int(length*Rate) && start+i < len(buf); i++ {
		t := float64(i) / Rate
		prev = dull*prev + (1-dull)*n.next()
		buf[start+i] += amp * math.Exp(-t/decay) * prev
	}
}

// bell adds a struck bell: a fundamental with the uneven overtones that
// make metal ring, the high ones dying first.
func bell(buf []float64, at, hz, amp, ring float64) {
	for _, p := range []struct{ ratio, amp, decay float64 }{
		{1, 1, 1}, {2, 0.45, 0.7}, {2.76, 0.35, 0.5}, {5.40, 0.18, 0.25}, {8.93, 0.08, 0.12},
	} {
		tone(buf, at, ring*3, amp*p.amp, 0.002, ring*p.decay, sine, fixed(hz*p.ratio))
	}
}

// note is an equal-tempered pitch: 0 is A4 (440 Hz), in semitones.
func note(semitones float64) float64 { return 440 * math.Pow(2, semitones/12) }

// finish scales to a peak of about -3 dBFS and fades the last 20 ms, so
// nothing clips or clicks at the end.
func finish(buf []float64) []float64 {
	peak := 0.0
	for _, v := range buf {
		peak = math.Max(peak, math.Abs(v))
	}
	if peak == 0 {
		return buf
	}
	fade := int(0.02 * Rate)
	for i := range buf {
		buf[i] *= 0.7 / peak
		if left := len(buf) - i; left < fade {
			buf[i] *= float64(left) / float64(fade)
		}
	}
	return buf
}

// wav writes 16-bit mono PCM.
func wav(buf []float64) []byte {
	var b bytes.Buffer
	data := uint32(len(buf)) * 2 //nolint:gosec // a few seconds of samples, far below 4 GB
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(36 + data)
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(1)) // mono
	w(uint32(Rate))
	w(uint32(Rate * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(data)
	for _, v := range buf {
		w(int16(math.Round(math.Max(-1, math.Min(1, v)) * 32767)))
	}
	return b.Bytes()
}

// --- Workshop: wood and brass ----------------------------------------------

// A wooden ratchet winding up, its clicks getting quicker.
func workshopStart() []float64 {
	buf := samples(1.0)
	n := noise(0x5eed1)
	at, gap := 0.0, 0.11
	for at < 0.9 {
		burst(buf, &n, at, 0.03, 0.5, 0.004, 0.55)
		tone(buf, at, 0.08, 0.35, 0.0005, 0.012, sine, fixed(1750))
		tone(buf, at, 0.12, 0.45, 0.001, 0.03, sine, fixed(420))
		at += gap
		gap = math.Max(0.055, gap*0.9)
	}
	return buf
}

// A wood block clack, then a low brass thunk as the wheel catches.
func workshopEnd() []float64 {
	buf := samples(0.7)
	n := noise(0x5eed2)
	burst(buf, &n, 0, 0.02, 0.6, 0.003, 0.4)
	tone(buf, 0, 0.2, 0.6, 0.0005, 0.05, sine, fixed(700))
	tone(buf, 0, 0.2, 0.3, 0.0005, 0.035, sine, fixed(1150))
	for _, p := range []struct{ hz, amp float64 }{{196, 0.5}, {392, 0.25}, {588, 0.12}} {
		tone(buf, 0.04, 0.6, p.amp, 0.004, 0.18, sine, fixed(p.hz))
	}
	return buf
}

// Three brass bells, rising: E5, G5, C6.
func workshopCheer() []float64 {
	buf := samples(2.0)
	for i, st := range []float64{7, 10, 15} {
		bell(buf, float64(i)*0.16, note(st), 0.5, 0.6)
	}
	return buf
}

// --- Neon arcade: chiptune --------------------------------------------------

// A square-wave power-up: a climb in small steps, like an old console.
func neonStart() []float64 {
	buf := samples(0.7)
	climb := func(t float64) float64 {
		step := math.Floor(math.Min(t, 0.5) / 0.5 * 16)
		return note(-12 + step*1.5)
	}
	tone(buf, 0, 0.62, 0.45, 0.005, 0.5, square(0.25), climb)
	tone(buf, 0, 0.62, 0.25, 0.005, 0.5, triangle, func(t float64) float64 { return climb(t) / 2 })
	return buf
}

// Two quick blips: the wheel lands.
func neonEnd() []float64 {
	buf := samples(0.45)
	tone(buf, 0, 0.06, 0.45, 0.002, 1, square(0.5), fixed(note(14)))
	tone(buf, 0.07, 0.3, 0.45, 0.002, 0.12, square(0.5), fixed(note(19)))
	tone(buf, 0.07, 0.3, 0.35, 0.002, 0.12, triangle, fixed(note(-17)))
	return buf
}

// A fanfare: C, E, G, C up the scale, then a held chord with vibrato.
func neonCheer() []float64 {
	buf := samples(1.7)
	for i, st := range []float64{3, 7, 10} {
		tone(buf, float64(i)*0.09, 0.085, 0.4, 0.002, 1, square(0.25), fixed(note(st)))
	}
	vib := func(hz float64) func(float64) float64 {
		return func(t float64) float64 { return hz * (1 + 0.012*math.Sin(2*math.Pi*6*t)*math.Min(1, t/0.2)) }
	}
	tone(buf, 0.27, 1.4, 0.38, 0.004, 0.6, square(0.25), vib(note(15)))
	tone(buf, 0.27, 1.4, 0.26, 0.004, 0.6, square(0.5), vib(note(19)))
	for i, st := range []float64{-21, -26, -21} {
		tone(buf, float64(i)*0.09, 0.09, 0.4, 0.002, 1, triangle, fixed(note(st)))
	}
	tone(buf, 0.27, 1.4, 0.45, 0.004, 0.6, triangle, fixed(note(-21)))
	return buf
}

// --- Selector board: relays and chimes --------------------------------------

// A bank of relays clattering as the board starts to roll, over a motor.
func boardStart() []float64 {
	buf := samples(0.85)
	n := noise(0x5eed3)
	tone(buf, 0, 0.85, 0.12, 0.25, 2, square(0.5), func(t float64) float64 { return 90 + 40*t })
	tone(buf, 0, 0.85, 0.08, 0.25, 2, sine, func(t float64) float64 { return 180 + 80*t })
	at := 0.0
	for at < 0.75 {
		burst(buf, &n, at, 0.012, 0.45, 0.002, 0.2)
		tone(buf, at, 0.02, 0.2, 0.0003, 0.004, sine, fixed(3200))
		at += 0.04 + 0.012*(n.next()+1)/2
	}
	return buf
}

// Ka-chunk: the board locks on its row.
func boardEnd() []float64 {
	buf := samples(0.5)
	n := noise(0x5eed4)
	burst(buf, &n, 0, 0.015, 0.55, 0.003, 0.3)
	tone(buf, 0, 0.3, 0.6, 0.001, 0.07, sine, func(t float64) float64 { return 110 - 40*math.Min(1, t/0.1) })
	burst(buf, &n, 0.07, 0.03, 0.6, 0.006, 0.6)
	tone(buf, 0.07, 0.35, 0.55, 0.001, 0.09, sine, fixed(75))
	return buf
}

// A three-tone station chime, rising: C6, E6, G6.
func boardCheer() []float64 {
	buf := samples(1.6)
	for i, st := range []float64{15, 19, 22} {
		at := float64(i) * 0.22
		hz := note(st)
		tone(buf, at, 1.0, 0.45, 0.006, 0.45, sine, fixed(hz))
		tone(buf, at, 1.0, 0.12, 0.006, 0.25, sine, fixed(hz*2))
		tone(buf, at, 1.0, 0.05, 0.006, 0.15, sine, fixed(hz*3))
	}
	return buf
}
