// Command notices writes THIRD-PARTY-LICENSES.txt: the licence of every Go
// module built into the app or the command line, on macOS, Windows or
// Linux. It fails when THIRD-PARTY-NOTICES.md doesn't name one of them.
// Run from the repository's root:
//
//	go run ./cmd/notices          # write the file
//	go run ./cmd/notices -check   # fail if it's out of date (make verify)
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"code.streamgremlin.com/streamgremlin/internal/notices"
)

// What's shipped: each program, on each platform, built as the Makefile
// builds it (the app with cgo except on Windows; the CLI without).
type target struct{ dir, pkg, goos, cgo string }

var targets = []target{
	{".", "./cmd/streamgremlin", "darwin", "0"},
	{".", "./cmd/streamgremlin", "windows", "0"},
	{".", "./cmd/streamgremlin", "linux", "0"},
	{"cmd/desktop", ".", "darwin", "1"},
	{"cmd/desktop", ".", "windows", "0"},
	{"cmd/desktop", ".", "linux", "1"},
}

func main() {
	check := flag.Bool("check", false, "fail if the file is out of date instead of writing it")
	out := flag.String("out", "THIRD-PARTY-LICENSES.txt", "the licences file")
	list := flag.String("notices", "THIRD-PARTY-NOTICES.md", "the notices that must name every module")
	flag.Parse()
	if err := run(*check, *out, *list); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run(check bool, out, list string) error {
	var all []byte
	for _, t := range targets {
		cmd := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}", t.pkg) //nolint:gosec // Arguments from the fixed list above.
		cmd.Dir = t.dir
		cmd.Env = append(os.Environ(), "GOOS="+t.goos, "CGO_ENABLED="+t.cgo)
		cmd.Stderr = os.Stderr
		b, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("go list %s for %s: %w", filepath.Join(t.dir, t.pkg), t.goos, err)
		}
		all = append(all, b...)
	}
	mods := notices.Parse(all, "code.streamgremlin.com/")

	text, err := notices.Render(notices.GoLicence, mods)
	if err != nil {
		return err
	}

	named, err := os.ReadFile(list)
	if err != nil {
		return err
	}
	if missing := notices.Missing(string(named), mods); len(missing) > 0 {
		return fmt.Errorf("%s doesn't name %s; add each to its Go table", list, strings.Join(missing, ", "))
	}

	if check {
		have, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(have, text) {
			return fmt.Errorf("%s is out of date: run `make notices`", out)
		}
		return nil
	}
	return os.WriteFile(out, text, 0o644) //nolint:gosec // A public licence file in the repository.
}
