//go:build release

// Package release checks the installers `make release` built (D-084):
// what each holds, that the programs in them are the version asked for,
// for the right systems, with StreamGremlin's Twitch app built in, and that
// the macOS app is whole and signed. Run by `make release` with
// RELEASE_DIR and RELEASE_VERSION set; needs macOS, 7z and hdiutil.
package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sgrelease "code.streamgremlin.com/streamgremlin/internal/release"
)

func env(t *testing.T) (dir, version string) {
	t.Helper()
	dir, version = os.Getenv("RELEASE_DIR"), os.Getenv("RELEASE_VERSION")
	if dir == "" || version == "" {
		t.Fatal("set RELEASE_DIR and RELEASE_VERSION (make release does)")
	}
	return dir, version
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput() //nolint:gosec // Fixed tools on paths this test makes.
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// checkBuild checks a program is for the right system, and holds the
// version and StreamGremlin's Twitch app ID it was built with. -trimpath
// leaves -ldflags out of Go's build record, so the values are looked for
// in the program itself. The ID comes from TWITCH_CLIENT_ID, which make
// passes on; it's never printed (it isn't committed anywhere, AGENTS.md).
func checkBuild(t *testing.T, path, version, goos string) {
	t.Helper()
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, s := range info.Settings {
		if s.Key == "GOOS" && s.Value != goos {
			t.Errorf("%s is for %q", filepath.Base(path), s.Value)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(version)) {
		t.Errorf("%s isn't version %s", filepath.Base(path), version)
	}
	id := os.Getenv("TWITCH_CLIENT_ID")
	if id == "" {
		t.Fatal("set TWITCH_CLIENT_ID (make release passes it on)")
	}
	if !bytes.Contains(b, []byte(id)) {
		t.Errorf("%s doesn't have StreamGremlin's Twitch app built in", filepath.Base(path))
	}
}

// downloads are the files a release offers, each with an SBOM beside it.
func downloads(version string) []string {
	return []string{"StreamGremlin-" + version + "-windows-x64-setup.exe", "StreamGremlin-" + version + "-macos.dmg",
		"StreamGremlin-" + version + "-linux-x64.deb", "StreamGremlin-" + version + "-linux-x64.rpm", "StreamGremlin-" + version + "-linux-x64.tar.gz",
		"StreamGremlin-" + version + "-streamdeck.streamDeckPlugin"}
}

// Each download has a CycloneDX SBOM beside it (D-117) that names it by
// its SHA-256 and lists its programs, the Go modules and npm packages
// built into them, and what it needs from the system.
func TestSBOMs(t *testing.T) {
	dir, version := env(t)
	parts := map[string][]string{
		"windows-x64-setup.exe":       {"StreamGremlin.exe", "cli/streamgremlin.exe", "Stream Deck/com.streamgremlin.streamdeck.streamDeckPlugin"},
		"macos.dmg":                   {"StreamGremlin.app/Contents/MacOS/StreamGremlin", "StreamGremlin.app/Contents/Helpers/streamgremlin", "com.streamgremlin.streamdeck.streamDeckPlugin"},
		"linux-x64.deb":               {"usr/bin/streamgremlin-desktop", "usr/bin/streamgremlin"},
		"linux-x64.rpm":               {"usr/bin/streamgremlin-desktop", "usr/bin/streamgremlin"},
		"linux-x64.tar.gz":            {"StreamGremlin-" + version + "-linux-x64/streamgremlin-desktop", "StreamGremlin-" + version + "-linux-x64/streamgremlin"},
		"streamdeck.streamDeckPlugin": {deckPlugin},
	}
	// The plugin's SHA-256 in each download that carries it.
	deckSums := map[string]string{}
	for _, f := range downloads(version) {
		b, err := os.ReadFile(filepath.Join(dir, f+".cdx.json")) //nolint:gosec // The release folder under test.
		if err != nil {
			t.Error(err)
			continue
		}
		var bom struct {
			Format   string `json:"bomFormat"`
			Spec     string `json:"specVersion"`
			Metadata struct {
				Component struct {
					Version string `json:"version"`
					Hashes  []struct {
						Content string `json:"content"`
					} `json:"hashes"`
				} `json:"component"`
			} `json:"metadata"`
			Components []struct {
				Ref    string `json:"bom-ref"`
				Hashes []struct {
					Content string `json:"content"`
				} `json:"hashes"`
			} `json:"components"`
		}
		if err := json.Unmarshal(b, &bom); err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, f)) //nolint:gosec // As above.
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		root := bom.Metadata.Component
		if bom.Format != "CycloneDX" || bom.Spec != "1.6" || root.Version != version || len(root.Hashes) != 1 || root.Hashes[0].Content != hex.EncodeToString(sum[:]) {
			t.Errorf("%s's SBOM doesn't describe it: %s %s %+v", f, bom.Format, bom.Spec, root)
		}
		var refs []string
		for _, c := range bom.Components {
			refs = append(refs, c.Ref)
			if strings.HasSuffix(c.Ref, "/"+deckPlugin) || c.Ref == "part:"+deckPlugin {
				if len(c.Hashes) == 1 {
					deckSums[f] = c.Hashes[0].Content
				}
			}
		}
		all := strings.Join(refs, "\n")
		kind := strings.TrimPrefix(f, "StreamGremlin-"+version+"-")
		want := []string{"pkg:golang/stdlib@", "pkg:golang/github.com/wailsapp/wails/v3@", "pkg:npm/svelte@", "system:"}
		if kind == "streamdeck.streamDeckPlugin" {
			want = []string{"system:StreamGremlin", "system:Elgato Stream Deck"}
		}
		for _, p := range parts[kind] {
			want = append(want, "part:"+p+"\n")
		}
		if strings.HasSuffix(kind, ".exe") || strings.HasSuffix(kind, ".dmg") || strings.HasSuffix(kind, ".streamDeckPlugin") {
			want = append(want, "pkg:npm/%40elgato/streamdeck@")
		}
		for _, w := range want {
			if !strings.Contains(all+"\n", w) {
				t.Errorf("%s's SBOM has no %q", f, strings.TrimSpace(w))
			}
		}
	} // The plugin on its own is the one the installers carry (D-119).
	alone := deckSums["StreamGremlin-"+version+"-streamdeck.streamDeckPlugin"]
	for _, f := range []string{"StreamGremlin-" + version + "-windows-x64-setup.exe", "StreamGremlin-" + version + "-macos.dmg"} {
		if alone == "" || deckSums[f] != alone {
			t.Errorf("%s carries a Stream Deck plugin %q, the download alone is %q", f, deckSums[f], alone)
		}
	}
}

const deckPlugin = "com.streamgremlin.streamdeck.streamDeckPlugin"

func TestChecksums(t *testing.T) {
	dir, version := env(t)
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range downloads(version) {
		for _, f := range []string{f, f + ".cdx.json"} {
			if !strings.Contains(string(sums), f+"\n") {
				t.Errorf("SHA256SUMS.txt misses %s", f)
			}
		}
	}
	cmd := exec.Command("shasum", "-a", "256", "-c", "SHA256SUMS.txt")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checksums: %v\n%s", err, out)
	}
}

// The Linux archive holds what the .deb and .rpm do (D-114): the app and
// command line for linux, the launcher, icons and the documents. The
// packages themselves are installed and started by make linux-smoke.
func TestLinuxArchive(t *testing.T) {
	dir, version := env(t)
	prefix := "StreamGremlin-" + version + "-linux-x64/"
	f, err := os.Open(filepath.Join(dir, prefix[:len(prefix)-1]+".tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := t.TempDir()
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name, ok := strings.CutPrefix(h.Name, prefix)
		if !ok {
			t.Fatalf("%s is outside %s", h.Name, prefix)
		}
		names = append(names, name)
		if name == "streamgremlin-desktop" || name == "streamgremlin" {
			if h.Mode != 0o755 {
				t.Errorf("%s has mode %o", name, h.Mode)
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(out, name)
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			checkBuild(t, p, version, "linux")
		}
	}
	for _, want := range []string{"streamgremlin-desktop", "streamgremlin", "com.streamgremlin.StreamGremlin.desktop",
		"icons/256x256/apps/com.streamgremlin.StreamGremlin.png", "doc/LICENSE", "doc/README.md", "doc/THIRD-PARTY-LICENSES.txt"} {
		if !slices.Contains(names, want) {
			t.Errorf("the archive misses %s: %q", want, names)
		}
	}
	for _, ext := range []string{"deb", "rpm"} {
		if st, err := os.Stat(filepath.Join(dir, "StreamGremlin-"+version+"-linux-x64."+ext)); err != nil || st.Size() < 1<<20 {
			t.Errorf("no .%s: %v", ext, err)
		}
	}
}

func TestWindowsInstaller(t *testing.T) {
	dir, version := env(t)
	setup := filepath.Join(dir, "StreamGremlin-"+version+"-windows-x64-setup.exe")
	listing := run(t, "7z", "l", "-ba", "-slt", setup)
	var files []string
	for _, line := range strings.Split(listing, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "Path = "); ok {
			files = append(files, strings.ReplaceAll(p, `\`, "/"))
		}
	}
	for _, want := range []string{
		"StreamGremlin.exe", "cli/streamgremlin.exe", "Stream Deck/com.streamgremlin.streamdeck.streamDeckPlugin",
		"StreamGremlin.ico", "Read me first.txt", "LICENSE.txt", "THIRD-PARTY-NOTICES.md", "THIRD-PARTY-LICENSES.txt",
	} {
		if !slices.Contains(files, want) {
			t.Errorf("the installer misses %s; it has %q", want, files)
		}
	}

	out := t.TempDir()
	run(t, "7z", "x", "-y", "-o"+out, setup, "StreamGremlin.exe", "cli/streamgremlin.exe")
	for _, exe := range []string{"StreamGremlin.exe", "cli/streamgremlin.exe"} {
		path := filepath.Join(out, exe)
		f, err := pe.Open(path)
		if err != nil {
			t.Fatalf("%s: %v", exe, err)
		}
		if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			t.Errorf("%s is for machine %#x", exe, f.Machine)
		}
		sub := f.OptionalHeader.(*pe.OptionalHeader64).Subsystem
		_ = f.Close()
		// The app is a window, with no console; the command line is a console program.
		if want := map[string]uint16{"StreamGremlin.exe": pe.IMAGE_SUBSYSTEM_WINDOWS_GUI, "cli/streamgremlin.exe": pe.IMAGE_SUBSYSTEM_WINDOWS_CUI}[exe]; sub != want {
			t.Errorf("%s subsystem %d, want %d", exe, sub, want)
		}
		checkBuild(t, path, version, "windows")
	}
}

func TestMacDMG(t *testing.T) {
	dir, version := env(t)
	dmg := filepath.Join(dir, "StreamGremlin-"+version+"-macos.dmg")
	mnt := t.TempDir()
	run(t, "hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mnt, dmg)
	t.Cleanup(func() { _ = exec.Command("hdiutil", "detach", "-force", mnt).Run() }) //nolint:gosec // The test's own mount point.

	if target, err := os.Readlink(filepath.Join(mnt, "Applications")); err != nil || target != "/Applications" {
		t.Errorf("no Applications link: %q %v", target, err)
	}
	for _, f := range []string{"com.streamgremlin.streamdeck.streamDeckPlugin", "Read me first.txt"} {
		if _, err := os.Stat(filepath.Join(mnt, f)); err != nil {
			t.Error(err)
		}
	}

	app := filepath.Join(mnt, "StreamGremlin.app")
	exe := filepath.Join(app, "Contents", "MacOS", "StreamGremlin")
	cli := filepath.Join(app, "Contents", "Helpers", "streamgremlin")
	for _, f := range []string{"Resources/StreamGremlin.icns", "Resources/LICENSE", "Resources/THIRD-PARTY-NOTICES.md", "Resources/THIRD-PARTY-LICENSES.txt"} {
		if _, err := os.Stat(filepath.Join(app, "Contents", f)); err != nil {
			t.Error(err)
		}
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	for key, want := range map[string]string{"CFBundleExecutable": "StreamGremlin", "CFBundleShortVersionString": version, "LSMinimumSystemVersion": "13.0", "CFBundleIconFile": "StreamGremlin"} {
		if got := strings.TrimSpace(run(t, "plutil", "-extract", key, "raw", plist)); got != want {
			t.Errorf("Info.plist %s = %q, want %q", key, got, want)
		}
	}

	// Apple silicon and Intel, both.
	for _, bin := range []string{exe, cli} {
		archs := strings.Fields(run(t, "lipo", "-archs", bin))
		slices.Sort(archs)
		if !slices.Equal(archs, []string{"arm64", "x86_64"}) {
			t.Errorf("%s is for %v", filepath.Base(bin), archs)
		}
		checkBuild(t, bin, version, "darwin")
	}
	// A whole, valid (ad hoc) signature: without one, macOS calls a
	// downloaded app damaged rather than offering Open Anyway.
	run(t, "codesign", "--verify", "--strict", "--deep", app)
	if out := run(t, "codesign", "-dv", app); !strings.Contains(out, "Signature=adhoc") {
		t.Errorf("not signed ad hoc:\n%s", out)
	}

	// The command line runs, and says its version.
	cmd := exec.Command(cli, "version") //nolint:gosec // The CLI from the dmg under test.
	cmd.Env = append(os.Environ(), "STREAMGREMLIN_DATA_DIR="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), version) {
		t.Errorf("streamgremlin version: %v\n%s", err, out)
	}
}

// The plugin says the release's version, in Stream Deck's four parts, the
// same as the Windows installer's: 0.1.0-beta.4 is 0.1.0.4 (D-119).
func TestStreamDeckVersion(t *testing.T) {
	dir, version := env(t)
	want, err := sgrelease.WindowsVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(filepath.Join(dir, "StreamGremlin-"+version+"-streamdeck.streamDeckPlugin"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()
	for _, f := range z.File {
		if f.Name != deckDir+"/manifest.json" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var m struct{ Version, UUID string }
		err = json.NewDecoder(r).Decode(&m)
		_ = r.Close()
		if err != nil || m.Version != want || m.UUID != "com.streamgremlin.streamdeck" {
			t.Fatalf("the plugin's manifest: %+v %v, want version %s", m, err, want)
		}
		return
	}
	t.Fatal("the plugin has no " + deckDir + "/manifest.json")
}

const deckDir = "com.streamgremlin.streamdeck.sdPlugin"
