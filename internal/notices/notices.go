// Package notices collects the licences of the Go modules built into
// StreamGremlin's programs, for THIRD-PARTY-LICENSES.txt, and checks that
// THIRD-PARTY-NOTICES.md names each of them. cmd/notices runs it.
package notices

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// GoLicence is Go's own licence, for the standard library and runtime in
// every Go program. It's kept here because some installs (Homebrew's) move
// it out of GOROOT.
//
//go:embed go-LICENSE
var GoLicence string

// Module is one module version built into a program.
type Module struct {
	Path, Version, Dir string
}

// Parse reads `go list -deps` lines of "path<TAB>version<TAB>dir". Lines
// for the standard library (empty) and for modules whose path starts with
// own are left out; each module version is kept once, sorted.
func Parse(out []byte, own string) []Module {
	seen := map[Module]bool{}
	var mods []Module
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[0] == "" || strings.HasPrefix(f[0], own) {
			continue
		}
		m := Module{Path: f[0], Version: f[1], Dir: f[2]}
		if !seen[m] {
			seen[m] = true
			mods = append(mods, m)
		}
	}
	sortModules(mods)
	return mods
}

func sortModules(mods []Module) {
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Path != mods[j].Path {
			return mods[i].Path < mods[j].Path
		}
		return mods[i].Version < mods[j].Version
	})
}

var licenceFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|unlicense)`)

// Render writes Go's own licence, then each module's licence files whole.
// The output carries no date, so it changes only when the modules do.
func Render(goLicence string, mods []Module) ([]byte, error) {
	mods = append([]Module(nil), mods...)
	sortModules(mods)
	var b bytes.Buffer
	b.WriteString("Licences of the Go code built into StreamGremlin's app and command line.\n")
	b.WriteString("Written by `make notices` (cmd/notices); don't edit it by hand. THIRD-PARTY-NOTICES.md lists them.\n\n")
	section(&b, "Go (the standard library and runtime)", "LICENSE", goLicence)
	for _, m := range mods {
		entries, err := os.ReadDir(m.Dir)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", m.Path, m.Version, err)
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && licenceFile.MatchString(e.Name()) {
				names = append(names, e.Name())
			}
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("%s %s has no licence file", m.Path, m.Version)
		}
		sort.Strings(names)
		for _, n := range names {
			text, err := os.ReadFile(filepath.Join(m.Dir, n))
			if err != nil {
				return nil, err
			}
			section(&b, m.Path+" "+m.Version, n, string(text))
		}
	}
	return b.Bytes(), nil
}

func section(b *bytes.Buffer, what, file, text string) {
	fmt.Fprintf(b, "================================================================\n%s\n== %s\n\n%s", what, file, text)
	if !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// Missing lists the module paths the notices text doesn't name in
// backticks, each once.
func Missing(notices string, mods []Module) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mods {
		if seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		if !strings.Contains(notices, "`"+m.Path+"`") {
			out = append(out, m.Path)
		}
	}
	sort.Strings(out)
	return out
}
