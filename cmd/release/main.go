// Command release makes the pieces of StreamGremlin's installers that need
// code, for `make release` (D-084):
//
//	release winversion VERSION     # print 0.1.0-beta.1 as 0.1.0.1
//	release ico OUT PNG...         # a Windows .ico from square PNGs
//	release plist VERSION OUT      # the macOS app's Info.plist
package main

import (
	"errors"
	"fmt"
	"os"

	"code.streamgremlin.com/streamgremlin/internal/release"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: release winversion|ico|plist")
	}
	switch cmd, args := args[0], args[1:]; {
	case cmd == "winversion" && len(args) == 1:
		v, err := release.WindowsVersion(args[0])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	case cmd == "ico" && len(args) >= 2:
		var pngs [][]byte
		for _, p := range args[1:] {
			b, err := os.ReadFile(p) //nolint:gosec // Paths the Makefile names, in this repository.
			if err != nil {
				return err
			}
			pngs = append(pngs, b)
		}
		ico, err := release.ICO(pngs)
		if err != nil {
			return err
		}
		return os.WriteFile(args[0], ico, 0o644) //nolint:gosec // Part of a public installer.
	case cmd == "plist" && len(args) == 2:
		b, err := release.InfoPlist(args[0])
		if err != nil {
			return err
		}
		return os.WriteFile(args[1], b, 0o644) //nolint:gosec // Part of a public app.
	}
	return errors.New("usage: release winversion VERSION | ico OUT PNG... | plist VERSION OUT")
}
