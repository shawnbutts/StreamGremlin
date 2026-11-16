// Package release makes the pieces of StreamGremlin's installers that
// need code: the Windows icon file, the macOS app's Info.plist, and the
// version in the forms each system wants (D-084). cmd/release runs it for
// `make release`.
package release

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"regexp"
	"strconv"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/brand"
)

// A release version: X.Y.Z, optionally with a pre-release such as beta.1.
// Nothing else is allowed, so it's safe in a plist or an NSIS define.
var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[a-z]+(\.[0-9]+)?)?$`)

// CheckVersion refuses anything but a plain release version.
func CheckVersion(v string) error {
	if !versionRE.MatchString(v) {
		return fmt.Errorf("version %q isn't like 0.1.0 or 0.1.0-beta.1", v)
	}
	return nil
}

// FullBuild is a full release's fourth number: the highest, so 0.2.0 sorts
// after every 0.2.0 pre-release, as it does by Semantic Versioning (D-125).
const FullBuild = 65535

// WindowsVersion turns 0.1.0-beta.3 into the four numbers Windows, macOS's
// build number and Stream Deck want, 0.1.0.3: a pre-release's number is the
// fourth, at most 65534. A full release's fourth is FullBuild, 0.2.0.65535,
// so it's newer than its own betas there too.
func WindowsVersion(v string) (string, error) {
	if err := CheckVersion(v); err != nil {
		return "", err
	}
	core, pre, isPre := strings.Cut(v, "-")
	build := strconv.Itoa(FullBuild)
	if isPre {
		build = "0"
		if _, n, ok := strings.Cut(pre, "."); ok {
			build = n
		}
		if n, err := strconv.Atoi(build); err != nil || n >= FullBuild {
			return "", fmt.Errorf("version %q: a pre-release's number must be under %d", v, FullBuild)
		}
	}
	parts := append(strings.Split(core, "."), build)
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err != nil || n > 65535 {
			return "", fmt.Errorf("version %q: each part must be at most 65535", v)
		}
	}
	return strings.Join(parts, "."), nil
}

// ICO makes a Windows icon from square PNGs of up to 256 pixels. Each size
// is kept as a 32-bit bitmap with its alpha, which every Windows version
// and NSIS read.
func ICO(pngs [][]byte) ([]byte, error) {
	type entry struct {
		size int
		data []byte
	}
	var entries []entry
	for i, p := range pngs {
		src, err := png.Decode(bytes.NewReader(p))
		if err != nil {
			return nil, fmt.Errorf("icon %d: %w", i, err)
		}
		b := src.Bounds()
		size := b.Dx()
		if size != b.Dy() || size < 1 || size > 256 {
			return nil, fmt.Errorf("icon %d is %dx%d; it must be square, at most 256", i, b.Dx(), b.Dy())
		}
		img := image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.Draw(img, img.Bounds(), src, b.Min, draw.Src)
		entries = append(entries, entry{size, dib(img)})
	}
	if len(entries) == 0 {
		return nil, errors.New("no icons")
	}
	le := binary.LittleEndian
	var out bytes.Buffer
	head := make([]byte, 6)
	le.PutUint16(head[2:], 1)
	le.PutUint16(head[4:], uint16(len(entries))) //nolint:gosec // A handful of sizes.
	out.Write(head)
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		d := make([]byte, 16)
		d[0], d[1] = byte(e.size%256), byte(e.size%256) //nolint:gosec // 1 to 256, and 0 means 256.
		le.PutUint16(d[4:], 1)
		le.PutUint16(d[6:], 32)
		le.PutUint32(d[8:], uint32(len(e.data))) //nolint:gosec // At most a few hundred KB.
		le.PutUint32(d[12:], uint32(offset))     //nolint:gosec // As above.
		out.Write(d)
		offset += len(e.data)
	}
	for _, e := range entries {
		out.Write(e.data)
	}
	return out.Bytes(), nil
}

// dib is one icon image: a BITMAPINFOHEADER (with the height doubled, as
// icons have it), BGRA rows from the bottom up, and an empty AND mask.
func dib(img *image.NRGBA) []byte {
	le := binary.LittleEndian
	size := img.Bounds().Dx()
	maskRow := ((size + 31) / 32) * 4
	out := make([]byte, 40, 40+size*size*4+maskRow*size)
	le.PutUint32(out[0:], 40)
	le.PutUint32(out[4:], uint32(size))   //nolint:gosec // At most 256.
	le.PutUint32(out[8:], uint32(2*size)) //nolint:gosec // As above.
	le.PutUint16(out[12:], 1)
	le.PutUint16(out[14:], 32)
	for y := size - 1; y >= 0; y-- {
		for x := range size {
			c := img.NRGBAAt(x, y)
			out = append(out, c.B, c.G, c.R, c.A)
		}
	}
	return append(out, make([]byte, maskRow*size)...)
}

// InfoPlist is the macOS app's Info.plist for a version.
func InfoPlist(version string) ([]byte, error) {
	build, err := WindowsVersion(version)
	if err != nil {
		return nil, err
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>` + brand.Name + `</string>
	<key>CFBundleIdentifier</key>
	<string>com.streamgremlin.desktop</string>
	<key>CFBundleName</key>
	<string>` + brand.Name + `</string>
	<key>CFBundleDisplayName</key>
	<string>` + brand.Name + `</string>
	<key>CFBundleIconFile</key>
	<string>` + brand.Name + `</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>` + version + `</string>
	<key>CFBundleVersion</key>
	<string>` + build + `</string>
	<key>LSMinimumSystemVersion</key>
	<string>13.0</string>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.video</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSHumanReadableCopyright</key>
	<string>Copyright 2026 the StreamGremlin authors. Apache License 2.0.</string>
</dict>
</plist>
`), nil
}
