package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSBOMCommand(t *testing.T) {
	dir := t.TempDir()
	download := filepath.Join(dir, "StreamGremlin-0.1.0-beta.5-linux-x64.deb")
	list := filepath.Join(dir, "ui.json")
	for f, body := range map[string]string{download: "a package", list: `{"packages":[{"name":"svelte","version":"5.57.2","license":"MIT"}]}`} {
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "sbom.json")
	err = run([]string{"sbom", out, download, "0.1.0-beta.5", "deb", "1800000000",
		"go:usr/bin/streamgremlin-desktop=" + self, "npm:usr/bin/streamgremlin-desktop=" + list, "file:usr/share/doc/streamgremlin/LICENSE=" + list})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out) //nolint:gosec // The test's own file.
	if err != nil {
		t.Fatal(err)
	}
	var bom struct {
		Components []struct {
			Ref string `json:"bom-ref"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &bom); err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, c := range bom.Components {
		refs = append(refs, c.Ref)
	}
	all := strings.Join(refs, " ")
	for _, want := range []string{"part:usr/bin/streamgremlin-desktop", "part:usr/share/doc/streamgremlin/LICENSE", "pkg:npm/svelte@5.57.2", "system:libgtk-4-1"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %s in %s", want, all)
		}
	}

	for _, bad := range [][]string{
		{"sbom", out, download, "0.1.0-beta.5", "deb", "1800000000", "npm:nothing=" + list, "go:app=" + self},
		{"sbom", out, download, "0.1.0-beta.5", "deb", "1800000000", "exe:app=" + self},
		{"sbom", out, download, "0.1.0-beta.5", "deb", "1800000000", "go:app"},
		{"sbom", out, download, "0.1.0-beta.5", "deb", "1800000000", "go:app=" + self, "file:app=" + list},
		{"sbom", out, download, "0.1.0-beta.5", "deb", "soon", "go:app=" + self},
	} {
		if err := run(bad); err == nil {
			t.Errorf("%v: no error", bad[6:])
		}
	}
}
