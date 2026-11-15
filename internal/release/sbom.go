package release

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/brand"
)

// The SBOM format (D-117): CycloneDX 1.6 JSON, which scanners read most
// widely.
const (
	cdxSchema  = "http://cyclonedx.org/schema/bom-1.6.schema.json"
	cdxVersion = "1.6"
	ownModules = "code.streamgremlin.com/"
)

// Part is one program or file inside a download.
type Part struct {
	Name string   // where it is in the download: "cli/streamgremlin.exe"
	File string   // the file as shipped, to hash and, for Go, to read
	Go   bool     // a Go program: its modules come from its build record
	NPM  []string // lists of the npm packages bundled into it (bin/sbom)
}

// SBOMSpec says what one download holds.
type SBOMSpec struct {
	Download string    // the download's file
	Version  string    // StreamGremlin's version
	System   string    // windows, macos, deb, rpm or linux: what it needs from the system
	Time     time.Time // when it was built: SOURCE_DATE_EPOCH, so a rebuild is the same bytes
	Parts    []Part
}

// The JSON, with only the fields StreamGremlin fills.
type (
	cdxBOM struct {
		Schema       string          `json:"$schema"`
		Format       string          `json:"bomFormat"`
		Spec         string          `json:"specVersion"`
		Serial       string          `json:"serialNumber"`
		Version      int             `json:"version"`
		Metadata     cdxMetadata     `json:"metadata"`
		Components   []cdxComponent  `json:"components"`
		Dependencies []cdxDependency `json:"dependencies"`
	}
	cdxMetadata struct {
		Timestamp string       `json:"timestamp"`
		Tools     cdxTools     `json:"tools"`
		Component cdxComponent `json:"component"`
	}
	cdxTools struct {
		Components []cdxComponent `json:"components"`
	}
	cdxComponent struct {
		Type        string        `json:"type"`
		Ref         string        `json:"bom-ref,omitempty"`
		Supplier    *cdxOrg       `json:"supplier,omitempty"`
		Name        string        `json:"name"`
		Version     string        `json:"version,omitempty"`
		Description string        `json:"description,omitempty"`
		Scope       string        `json:"scope,omitempty"`
		Hashes      []cdxHash     `json:"hashes,omitempty"`
		Licenses    []cdxLicense  `json:"licenses,omitempty"`
		PURL        string        `json:"purl,omitempty"`
		Refs        []cdxExtRef   `json:"externalReferences,omitempty"`
		Properties  []cdxProperty `json:"properties,omitempty"`
	}
	cdxOrg struct {
		Name string   `json:"name"`
		URL  []string `json:"url,omitempty"`
	}
	cdxHash struct {
		Alg     string `json:"alg"`
		Content string `json:"content"`
	}
	cdxLicense struct {
		Expression string `json:"expression"`
	}
	cdxExtRef struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	cdxProperty struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	cdxDependency struct {
		Ref       string   `json:"ref"`
		DependsOn []string `json:"dependsOn,omitempty"`
	}
)

// bundled is what packaging/sbom/bundled.mjs writes.
type bundled struct {
	Packages []bundledPackage `json:"packages"`
	Vendored []bundledPackage `json:"vendored"`
}

type bundledPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	License string `json:"license"`
	PURL    string `json:"purl"`
}

// systemNeeds is what each kind of download needs from the computer and
// doesn't carry: listed so a scanner knows they're there, marked as not
// shipped. The Linux names match packaging/linux/nfpm.yaml.
var systemNeeds = map[string][]cdxComponent{
	"windows": {
		{Type: "operating-system", Name: "Windows", Scope: "required", Description: "Windows 10 or 11."},
		{Type: "framework", Name: "Microsoft Edge WebView2 Runtime", Scope: "required", Description: "Draws the app's window. Part of Windows 10 and 11."},
	},
	"macos": {
		{Type: "operating-system", Name: "macOS", Scope: "required", Description: "macOS 13 or newer. The app draws its window with the system's WebKit."},
	},
	"deb": {
		{Type: "library", Name: "libgtk-4-1", Scope: "required", Description: "GTK 4, 4.10 or newer, from the system's packages."},
		{Type: "library", Name: "libwebkitgtk-6.0-4", Scope: "required", Description: "WebKitGTK, from the system's packages. Draws the app's window."},
		{Type: "library", Name: "gstreamer1.0-plugins-good", Scope: "optional", Description: "Plays the app's sounds."},
		{Type: "application", Name: "gnome-keyring", Scope: "optional", Description: "Keeps sign-ins; without a keyring they go in an encrypted file."},
	},
	"rpm": {
		{Type: "library", Name: "gtk4", Scope: "required", Description: "GTK 4, 4.10 or newer, from the system's packages."},
		{Type: "library", Name: "webkitgtk6.0", Scope: "required", Description: "WebKitGTK, from the system's packages. Draws the app's window."},
		{Type: "library", Name: "gstreamer1-plugins-good", Scope: "optional", Description: "Plays the app's sounds."},
		{Type: "application", Name: "gnome-keyring", Scope: "optional", Description: "Keeps sign-ins; without a keyring they go in an encrypted file."},
	},
	"linux": {
		{Type: "library", Name: "GTK", Scope: "required", Description: "GTK 4, 4.10 or newer, from the system's packages."},
		{Type: "library", Name: "WebKitGTK", Scope: "required", Description: "WebKitGTK's 6.0 API, from the system's packages. Draws the app's window."},
		{Type: "library", Name: "GStreamer good plugins", Scope: "optional", Description: "Plays the app's sounds."},
		{Type: "application", Name: "Secret Service keyring", Scope: "optional", Description: "Keeps sign-ins, such as GNOME Keyring; without one they go in an encrypted file."},
	},
}

// SBOM is a CycloneDX software bill of materials for one download: the
// download itself, each program and file in it with its SHA-256, the Go
// modules built into each program (from Go's build record, so exactly
// what was compiled), the npm packages bundled into each, and what the
// download needs from the system. The same inputs give the same bytes.
func SBOM(spec SBOMSpec) ([]byte, error) {
	if err := CheckVersion(spec.Version); err != nil {
		return nil, err
	}
	needs, ok := systemNeeds[spec.System]
	if !ok {
		return nil, fmt.Errorf("unknown system %q", spec.System)
	}
	if len(spec.Parts) == 0 {
		return nil, errors.New("a download holds at least one part")
	}
	sum, err := fileSHA256(spec.Download)
	if err != nil {
		return nil, err
	}
	file := filepath.Base(spec.Download)
	home := strings.TrimSuffix(brand.Repo, ".git")
	licence := []cdxLicense{{Expression: "Apache-2.0"}}
	supplier := &cdxOrg{Name: brand.Name, URL: []string{"https://streamgremlin.com"}}
	root := cdxComponent{
		Type: "application", Ref: "download", Supplier: supplier, Name: brand.Name, Version: spec.Version,
		Description: file,
		Hashes:      []cdxHash{{"SHA-256", sum}},
		Licenses:    licence,
		Refs: []cdxExtRef{
			{"distribution", home + "/releases/download/v" + spec.Version + "/" + file},
			{"vcs", home},
			{"website", "https://streamgremlin.com"},
			{"issue-tracker", brand.IssuesURL},
		},
	}

	var parts []cdxComponent
	libs := map[string]cdxComponent{}
	deps := map[string][]string{}
	for _, p := range spec.Parts {
		ref := "part:" + p.Name
		if _, dup := deps[ref]; dup {
			return nil, fmt.Errorf("%s is listed twice", p.Name)
		}
		deps[ref] = nil
		h, err := fileSHA256(p.File)
		if err != nil {
			return nil, err
		}
		c := cdxComponent{Type: "application", Ref: ref, Supplier: supplier, Name: p.Name, Version: spec.Version, Hashes: []cdxHash{{"SHA-256", h}}, Licenses: licence}
		if !p.Go && len(p.NPM) == 0 {
			c.Type = "file"
		}
		if p.Go {
			info, err := buildinfo.ReadFile(p.File)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p.Name, err)
			}
			c.Properties = goProperties(info)
			for _, lib := range goLibraries(info) {
				libs[lib.Ref] = lib
				deps[ref] = append(deps[ref], lib.Ref)
			}
		}
		for _, list := range p.NPM {
			found, err := npmLibraries(list)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p.Name, err)
			}
			for _, lib := range found {
				libs[lib.Ref] = lib
				deps[ref] = append(deps[ref], lib.Ref)
			}
		}
		parts = append(parts, c)
	}

	bom := cdxBOM{Schema: cdxSchema, Format: "CycloneDX", Spec: cdxVersion, Version: 1}
	bom.Metadata = cdxMetadata{
		Timestamp: spec.Time.UTC().Format(time.RFC3339),
		Tools:     cdxTools{Components: []cdxComponent{{Type: "application", Supplier: supplier, Name: "streamgremlin release", Version: spec.Version}}},
		Component: root,
	}
	rootDeps := make([]string, 0, len(parts)+len(needs))
	bom.Components = append(bom.Components, parts...)
	for _, p := range parts {
		rootDeps = append(rootDeps, p.Ref)
	}
	refs := make([]string, 0, len(libs))
	for ref := range libs {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		bom.Components = append(bom.Components, libs[ref])
	}
	for _, n := range needs {
		n.Ref = "system:" + n.Name
		n.Properties = []cdxProperty{{"streamgremlin:shipped", "false"}}
		bom.Components = append(bom.Components, n)
		rootDeps = append(rootDeps, n.Ref)
	}
	bom.Dependencies = append(bom.Dependencies, cdxDependency{Ref: "download", DependsOn: rootDeps})
	for _, p := range parts {
		d := deps[p.Ref]
		sort.Strings(d)
		bom.Dependencies = append(bom.Dependencies, cdxDependency{Ref: p.Ref, DependsOn: d})
	}

	// The serial number is made from everything else, so it names this
	// content and stays the same when the content does.
	first, err := json.Marshal(bom)
	if err != nil {
		return nil, err
	}
	bom.Serial = "urn:uuid:" + uuidOf(first)
	out, err := json.MarshalIndent(bom, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// goLibraries are the Go standard library and every module a program was
// built with, except StreamGremlin's own.
func goLibraries(info *buildinfo.BuildInfo) []cdxComponent {
	goVersion, _, _ := strings.Cut(info.GoVersion, " ")
	out := []cdxComponent{{
		Type: "library", Ref: "pkg:golang/stdlib@" + purlEscape(strings.TrimPrefix(goVersion, "go")),
		Name: "stdlib", Version: goVersion, Description: "Go's standard library and runtime.",
		Licenses: []cdxLicense{{Expression: "BSD-3-Clause"}},
		PURL:     "pkg:golang/stdlib@" + purlEscape(strings.TrimPrefix(goVersion, "go")),
	}}
	for _, m := range info.Deps {
		if m.Replace != nil {
			if m.Replace.Version == "" { // a folder on disk: StreamGremlin's own
				continue
			}
			m = m.Replace
		}
		if strings.HasPrefix(m.Path, ownModules) {
			continue
		}
		purl := goPURL(m.Path, m.Version)
		out = append(out, cdxComponent{Type: "library", Ref: purl, Name: m.Path, Version: m.Version, PURL: purl})
	}
	return out
}

// goProperties are the build settings that say what a program is for.
func goProperties(info *buildinfo.BuildInfo) []cdxProperty {
	keep := map[string]bool{"GOOS": true, "GOARCH": true, "CGO_ENABLED": true, "vcs.revision": true, "vcs.modified": true}
	out := []cdxProperty{{"go:version", info.GoVersion}}
	for _, s := range info.Settings {
		if keep[s.Key] {
			out = append(out, cdxProperty{"go:" + s.Key, s.Value})
		}
	}
	return out
}

// npmLibraries reads a list bundled.mjs wrote.
func npmLibraries(file string) ([]cdxComponent, error) {
	b, err := os.ReadFile(file) //nolint:gosec // A list the build wrote, named by the Makefile.
	if err != nil {
		return nil, err
	}
	var list bundled
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(list.Packages) == 0 {
		return nil, fmt.Errorf("%s lists no packages; was the bundle built?", file)
	}
	var out []cdxComponent
	for _, p := range append(list.Packages, list.Vendored...) {
		if p.Name == "" || p.Version == "" {
			return nil, fmt.Errorf("%s: a package without a name or version", file)
		}
		purl := p.PURL
		if purl == "" {
			purl = npmPURL(p.Name, p.Version)
		}
		c := cdxComponent{Type: "library", Ref: purl, Name: p.Name, Version: p.Version, PURL: purl}
		if p.License != "" {
			c.Licenses = []cdxLicense{{Expression: p.License}}
		}
		out = append(out, c)
	}
	return out, nil
}

// goPURL is a Go module's package URL: pkg:golang/github.com/a/b@v1.2.3.
func goPURL(path, version string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = purlEscape(s)
	}
	return "pkg:golang/" + strings.Join(segs, "/") + "@" + purlEscape(version)
}

// npmPURL is an npm package's package URL: pkg:npm/%40scope/name@1.2.3.
func npmPURL(name, version string) string {
	scope, rest, scoped := strings.Cut(name, "/")
	if scoped {
		return "pkg:npm/" + purlEscape(scope) + "/" + purlEscape(rest) + "@" + purlEscape(version)
	}
	return "pkg:npm/" + purlEscape(name) + "@" + purlEscape(version)
}

// purlEscape percent-encodes all but the characters the package URL
// spec leaves bare in a segment.
func purlEscape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// uuidOf is a version 5 style UUID from the SHA-256 of b.
func uuidOf(b []byte) string {
	h := sha256.Sum256(b)
	u := h[:16]
	u[6] = (u[6] & 0x0f) | 0x50
	u[8] = (u[8] & 0x3f) | 0x80
	x := hex.EncodeToString(u)
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // Files the Makefile names, in the build's own folders.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
