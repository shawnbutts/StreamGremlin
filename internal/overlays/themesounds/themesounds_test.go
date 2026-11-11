package themesounds

import (
	"bytes"
	"encoding/binary"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the looks' sound files")

const themes = "../themes"

// The looks' sound files are this package's output, byte for byte. After
// changing a sound: go test ./internal/overlays/themesounds -update
func TestTheLooksSoundsAreMadeHere(t *testing.T) {
	files := Files()
	if len(files) != len(Looks)*len(Cues) {
		t.Fatalf("%d files", len(files))
	}
	for _, look := range Looks {
		for _, cue := range Cues {
			name := look + "/" + cue + ".wav"
			want, ok := files[name]
			if !ok {
				t.Fatalf("no %s", name)
			}
			path := filepath.Join(themes, filepath.FromSlash(name))
			if *update {
				if err := os.WriteFile(path, want, 0o644); err != nil { //nolint:gosec // a file in the repository, readable like the rest
					t.Fatal(err)
				}
				continue
			}
			got, err := os.ReadFile(path) //nolint:gosec // the repository's own files
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s doesn't match what this package makes (%v): run go test ./internal/overlays/themesounds -update", name, err)
			}
		}
	}
}

// Each is a short, valid, 16-bit mono WAV that isn't silent and doesn't clip.
func TestTheLooksSoundsArePlayable(t *testing.T) {
	again := Files()
	for name, b := range Files() {
		if !bytes.Equal(b, again[name]) {
			t.Errorf("%s: not the same twice", name)
		}
		if len(b) < 44 || string(b[:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" {
			t.Fatalf("%s: not a WAV file", name)
		}
		le := binary.LittleEndian
		if le.Uint16(b[20:]) != 1 || le.Uint16(b[22:]) != 1 || le.Uint32(b[24:]) != Rate || le.Uint16(b[34:]) != 16 ||
			int(le.Uint32(b[40:])) != len(b)-44 || int(le.Uint32(b[4:])) != len(b)-8 {
			t.Fatalf("%s: header", name)
		}
		seconds := float64(len(b)-44) / 2 / Rate
		if seconds < 0.3 || seconds > 2.5 || len(b) > 120<<10 {
			t.Errorf("%s: %.2fs, %d bytes", name, seconds, len(b))
		}
		peak := 0
		for i := 44; i+1 < len(b); i += 2 {
			v := sample(b[i:])
			peak = max(peak, v, -v)
		}
		if peak < 20000 || peak > 23500 { // about -3 dBFS
			t.Errorf("%s: peak %d", name, peak)
		}
		if last := sample(b[len(b)-2:]); last > 50 || last < -50 {
			t.Errorf("%s: ends at %d, a click", name, last)
		}
	}
}

// sample reads a signed 16-bit little-endian sample.
func sample(b []byte) int {
	v := int(binary.LittleEndian.Uint16(b))
	if v >= 1<<15 {
		v -= 1 << 16
	}
	return v
}
