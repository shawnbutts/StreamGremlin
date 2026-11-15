package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// sbomFixture is a download holding this test's own program, which Go
// built with the jsonschema module, and a bundle listing npm packages.
func sbomFixture(t *testing.T) SBOMSpec {
	t.Helper()
	dir := t.TempDir()
	download := filepath.Join(dir, "StreamGremlin-1.2.3-beta.4-windows-x64-setup.exe")
	if err := os.WriteFile(download, []byte("an installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(dir, "ui.json")
	if err := os.WriteFile(list, []byte(`{"packages":[{"name":"@tabler/icons","version":"3.50.0","license":"MIT"},{"name":"svelte","version":"5.57.2","license":"MIT"}],
		"vendored":[{"name":"Nerd Fonts Symbols Only","version":"3.5.1","license":"MIT","purl":"pkg:github/ryanoasis/nerd-fonts@v3.5.1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(dir, "plugin")
	if err := os.WriteFile(plugin, []byte("a Stream Deck plugin"), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return SBOMSpec{
		Download: download, Version: "1.2.3-beta.4", System: "windows", Time: time.Unix(1_800_000_000, 0),
		Parts: []Part{
			{Name: "StreamGremlin.exe", File: self, Go: true, NPM: []string{list}},
			{Name: "Stream Deck/com.streamgremlin.streamdeck.streamDeckPlugin", File: plugin},
		},
	}
}

func TestSBOM(t *testing.T) {
	spec := sbomFixture(t)
	b, err := SBOM(spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := SBOM(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, again) {
		t.Error("the same download gave two different SBOMs")
	}
	validate(t, b)

	var bom cdxBOM
	if err := json.Unmarshal(b, &bom); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bom.Serial, "urn:uuid:") || bom.Metadata.Timestamp != "2027-01-15T08:00:00Z" {
		t.Errorf("serial %q, timestamp %q", bom.Serial, bom.Metadata.Timestamp)
	}
	sum := sha256.Sum256([]byte("an installer"))
	root := bom.Metadata.Component
	if root.Version != "1.2.3-beta.4" || len(root.Hashes) != 1 || root.Hashes[0].Content != hex.EncodeToString(sum[:]) {
		t.Errorf("the download is %+v", root)
	}
	if !slices.Contains(root.Refs, cdxExtRef{"distribution", "https://github.com/shawnbutts/StreamGremlin/releases/download/v1.2.3-beta.4/StreamGremlin-1.2.3-beta.4-windows-x64-setup.exe"}) {
		t.Errorf("no link to the download on GitHub: %+v", root.Refs)
	}

	byRef := map[string]cdxComponent{}
	for _, c := range bom.Components {
		if _, dup := byRef[c.Ref]; dup {
			t.Errorf("%s is listed twice", c.Ref)
		}
		byRef[c.Ref] = c
		if strings.Contains(c.Name, "code.streamgremlin.com") {
			t.Errorf("StreamGremlin's own module %s is listed as a library", c.Name)
		}
	}
	var schemaMod string
	for ref := range byRef {
		if strings.HasPrefix(ref, "pkg:golang/github.com/santhosh-tekuri/jsonschema/v6@v") {
			schemaMod = ref
		}
	}
	if schemaMod == "" {
		t.Error("the Go modules the program was built with aren't listed")
	}
	for _, ref := range []string{"pkg:npm/%40tabler/icons@3.50.0", "pkg:npm/svelte@5.57.2", "pkg:github/ryanoasis/nerd-fonts@v3.5.1"} {
		if _, ok := byRef[ref]; !ok {
			t.Errorf("%s isn't listed", ref)
		}
	}
	if c := byRef["part:Stream Deck/com.streamgremlin.streamdeck.streamDeckPlugin"]; c.Type != "file" || len(c.Hashes) != 1 {
		t.Errorf("the plugin is %+v", c)
	}
	app := byRef["part:StreamGremlin.exe"]
	if app.Type != "application" || !slices.ContainsFunc(app.Properties, func(p cdxProperty) bool { return p.Name == "go:GOOS" }) {
		t.Errorf("the app is %+v", app)
	}
	var stdlib bool
	for ref := range byRef {
		stdlib = stdlib || strings.HasPrefix(ref, "pkg:golang/stdlib@")
	}
	if !stdlib {
		t.Error("Go's standard library isn't listed")
	}
	webview := byRef["system:Microsoft Edge WebView2 Runtime"]
	if webview.Scope != "required" || !slices.Contains(webview.Properties, cdxProperty{"streamgremlin:shipped", "false"}) {
		t.Errorf("WebView2 should be required and not shipped: %+v", webview)
	}

	deps := map[string][]string{}
	for _, d := range bom.Dependencies {
		deps[d.Ref] = d.DependsOn
	}
	for _, want := range []string{"part:StreamGremlin.exe", "system:Microsoft Edge WebView2 Runtime"} {
		if !slices.Contains(deps["download"], want) {
			t.Errorf("the download doesn't depend on %s", want)
		}
	}
	if !slices.Contains(deps["part:StreamGremlin.exe"], schemaMod) || !slices.Contains(deps["part:StreamGremlin.exe"], "pkg:npm/svelte@5.57.2") {
		t.Errorf("the app's dependencies are %v", deps["part:StreamGremlin.exe"])
	}

	// Another download is another serial number.
	spec.Version = "1.2.3-beta.5"
	other, err := SBOM(spec)
	if err != nil {
		t.Fatal(err)
	}
	var bom2 cdxBOM
	if err := json.Unmarshal(other, &bom2); err != nil {
		t.Fatal(err)
	}
	if bom2.Serial == bom.Serial {
		t.Error("two versions share a serial number")
	}
}

func TestSBOMRefuses(t *testing.T) {
	for name, change := range map[string]func(*SBOMSpec){
		"a bad version":        func(s *SBOMSpec) { s.Version = "1.2" },
		"an unknown system":    func(s *SBOMSpec) { s.System = "beos" },
		"no parts":             func(s *SBOMSpec) { s.Parts = nil },
		"a part twice":         func(s *SBOMSpec) { s.Parts = append(s.Parts, s.Parts[0]) },
		"a missing download":   func(s *SBOMSpec) { s.Download += ".gone" },
		"a missing npm list":   func(s *SBOMSpec) { s.Parts[0].NPM = []string{"gone.json"} },
		"a Go part that isn't": func(s *SBOMSpec) { s.Parts[1].Go = true },
		"an empty npm list": func(s *SBOMSpec) {
			empty := filepath.Join(filepath.Dir(s.Download), "empty.json")
			if err := os.WriteFile(empty, []byte(`{"packages":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			s.Parts[0].NPM = []string{empty}
		},
	} {
		spec := sbomFixture(t)
		change(&spec)
		if _, err := SBOM(spec); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// Every kind of download's SBOM is valid CycloneDX 1.6.
func TestSBOMEverySystem(t *testing.T) {
	for system := range systemNeeds {
		spec := sbomFixture(t)
		spec.System = system
		b, err := SBOM(spec)
		if err != nil {
			t.Fatal(err)
		}
		validate(t, b)
	}
}

// What the Linux SBOMs say a package needs is what the package says.
func TestSBOMMatchesLinuxPackages(t *testing.T) {
	b, err := os.ReadFile("../../packaging/linux/nfpm.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var conf struct {
		Overrides map[string]struct {
			Depends    []string `yaml:"depends"`
			Recommends []string `yaml:"recommends"`
		} `yaml:"overrides"`
	}
	if err := yaml.Unmarshal(b, &conf); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"deb", "rpm"} {
		var required, optional []string
		for _, c := range systemNeeds[kind] {
			if c.Scope == "required" {
				required = append(required, c.Name)
			} else {
				optional = append(optional, c.Name)
			}
		}
		o := conf.Overrides[kind]
		if !slices.Equal(required, o.Depends) || !slices.Equal(optional, o.Recommends) {
			t.Errorf("%s: the SBOM needs %v and %v; nfpm.yaml says %v and %v", kind, required, optional, o.Depends, o.Recommends)
		}
	}
}

func TestPURLs(t *testing.T) {
	for got, want := range map[string]string{
		goPURL("github.com/a/b", "v1.2.3+incompatible"): "pkg:golang/github.com/a/b@v1.2.3%2Bincompatible",
		npmPURL("@tabler/icons", "3.50.0"):              "pkg:npm/%40tabler/icons@3.50.0",
		npmPURL("ws", "8.22.2"):                         "pkg:npm/ws@8.22.2",
	} {
		if got != want {
			t.Errorf("%s, want %s", got, want)
		}
	}
}

// The schema check would catch a mistake.
func TestSBOMSchemaCatches(t *testing.T) {
	b, err := SBOM(sbomFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	bad := bytes.Replace(b, []byte(`"scope": "required"`), []byte(`"scope": "sometimes"`), 1)
	if bytes.Equal(bad, b) {
		t.Fatal("no scope to break")
	}
	if checkSchema(t, bad) == nil {
		t.Error("an invalid scope passed the schema")
	}
}

// validate checks b against CycloneDX's own schema, kept in testdata.
func validate(t *testing.T, b []byte) {
	t.Helper()
	if err := checkSchema(t, b); err != nil {
		t.Errorf("not valid CycloneDX 1.6: %v", err)
	}
}

func checkSchema(t *testing.T, b []byte) error {
	t.Helper()
	c := jsonschema.NewCompiler()
	for _, f := range []string{"bom-1.6.schema.json", "spdx.schema.json", "jsf-0.82.schema.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "cyclonedx", f)) //nolint:gosec // The test's own files.
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource("http://cyclonedx.org/schema/"+f, doc); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := c.Compile(cdxSchema)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return schema.Validate(doc)
}
