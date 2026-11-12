package release

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestCheckVersion(t *testing.T) {
	for _, v := range []string{"0.1.0", "0.1.0-beta.1", "1.20.3-rc.12", "2.0.0-beta"} {
		if err := CheckVersion(v); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	// Anything that could break out of a plist or an NSIS define is refused.
	for _, v := range []string{"", "dev", "1.0", "v1.0.0", "1.0.0-beta 1", "1.0.0-<x>", "1.0.0\"", "1.0.0-beta.1;x", "01.0.0"} {
		if err := CheckVersion(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

// Windows wants four numbers; a pre-release's number becomes the fourth.
func TestWindowsVersion(t *testing.T) {
	for v, want := range map[string]string{
		"0.1.0":        "0.1.0.0",
		"0.1.0-beta.3": "0.1.0.3",
		"1.2.3-rc.12":  "1.2.3.12",
		"2.0.0-beta":   "2.0.0.0",
	} {
		got, err := WindowsVersion(v)
		if err != nil || got != want {
			t.Errorf("%s: %q %v", v, got, err)
		}
	}
	if _, err := WindowsVersion("1.0.0-beta.70000"); err == nil {
		t.Error("a part over 65535 accepted")
	}
	if _, err := WindowsVersion("dev"); err == nil {
		t.Error("dev accepted")
	}
}

func square(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.Set(x, y, color.NRGBA{R: 200, G: 10, B: 20, A: 255})
		}
	}
	// The top-left pixel is see-through blue, to check orientation and alpha.
	img.Set(0, 0, color.NRGBA{B: 255, A: 128})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// An .ico holds each size as a 32-bit bitmap, bottom row first, with its
// alpha, as every Windows version and NSIS read it.
func TestICO(t *testing.T) {
	ico, err := ICO([][]byte{square(t, 16), square(t, 32), square(t, 256)})
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if le.Uint16(ico[0:]) != 0 || le.Uint16(ico[2:]) != 1 || le.Uint16(ico[4:]) != 3 {
		t.Fatalf("header % x", ico[:6])
	}
	for i, size := range []int{16, 32, 256} {
		e := ico[6+16*i:]
		w := int(e[0])
		if w == 0 {
			w = 256
		}
		if w != size || le.Uint16(e[6:]) != 32 {
			t.Fatalf("entry %d: % x", i, e[:16])
		}
		n, off := int(le.Uint32(e[8:])), int(le.Uint32(e[12:]))
		dib := ico[off : off+n]
		if int(le.Uint32(dib[4:])) != size || int(le.Uint32(dib[8:])) != 2*size || le.Uint16(dib[14:]) != 32 {
			t.Fatalf("entry %d bitmap header % x", i, dib[:16])
		}
		rowBytes := size * 4
		maskRow := ((size + 31) / 32) * 4
		if n != 40+rowBytes*size+maskRow*size {
			t.Fatalf("entry %d is %d bytes", i, n)
		}
		// The image's top row is stored last.
		top := dib[40+rowBytes*(size-1):]
		if top[0] != 255 || top[1] != 0 || top[2] != 0 || top[3] != 128 {
			t.Fatalf("entry %d top-left pixel BGRA % x", i, top[:4])
		}
		if top[4] != 20 || top[5] != 10 || top[6] != 200 || top[7] != 255 {
			t.Fatalf("entry %d second pixel BGRA % x", i, top[4:8])
		}
	}

	if _, err := ICO([][]byte{square(t, 300)}); err == nil {
		t.Error("an icon over 256 accepted")
	}
	if _, err := ICO([][]byte{[]byte("not a png")}); err == nil {
		t.Error("not a PNG accepted")
	}
}

// The app's Info.plist parses, names the executable and icon, and carries
// the version and the minimum macOS (D-008).
func TestInfoPlist(t *testing.T) {
	b, err := InfoPlist("0.1.0-beta.2")
	if err != nil {
		t.Fatal(err)
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	keys := map[string]string{}
	var last, text string
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch tok := tok.(type) {
		case xml.CharData:
			text = strings.TrimSpace(string(tok))
		case xml.EndElement:
			switch tok.Name.Local {
			case "key":
				last = text
			case "string":
				keys[last] = text
			case "true":
				keys[last] = "true"
			}
		case xml.StartElement:
			text = ""
			if tok.Name.Local == "true" {
				keys[last] = "true"
			}
		}
	}
	for k, want := range map[string]string{
		"CFBundleExecutable":         "StreamGremlin",
		"CFBundleIdentifier":         "com.streamgremlin.desktop",
		"CFBundleName":               "StreamGremlin",
		"CFBundleIconFile":           "StreamGremlin",
		"CFBundlePackageType":        "APPL",
		"CFBundleShortVersionString": "0.1.0-beta.2",
		"CFBundleVersion":            "0.1.0.2",
		"LSMinimumSystemVersion":     "13.0",
		"NSHighResolutionCapable":    "true",
	} {
		if keys[k] != want {
			t.Errorf("%s = %q, want %q", k, keys[k], want)
		}
	}
	if _, err := InfoPlist("1.0.0</string>"); err == nil {
		t.Error("a bad version accepted")
	}
}
