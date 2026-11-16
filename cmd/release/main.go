// Command release makes the pieces of StreamGremlin's installers that need
// code, for `make release` (D-084):
//
//	release winversion VERSION     # print 0.1.0-beta.1 as 0.1.0.1
//	release ico OUT PNG...         # a Windows .ico from square PNGs
//	release plist VERSION OUT      # the macOS app's Info.plist
//	release tarball OUT DIR PREFIX UNIXTIME  # a reproducible .tar.gz of DIR
//	release stamp DIR UNIXTIME     # date everything under DIR, for packages
//	release sbom OUT DOWNLOAD VERSION SYSTEM UNIXTIME PART...
//	                               # a CycloneDX SBOM of DOWNLOAD (D-117)
//	release keygen KEYFILE         # a new signing key for updates (D-120)
//	release sign KEYFILE SUMS OUT  # sign SHA256SUMS.txt for updates
//	release verify SUMS SIG        # check a signature with the app's keys
//
// Each PART of an SBOM is go:NAME=FILE (a Go program at NAME in the
// download), file:NAME=FILE (any other file) or npm:NAME=LIST (npm
// packages bundled into NAME, listed by packaging/sbom/bundled.mjs).
// SYSTEM is windows, macos, deb, rpm or linux.
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/release"
	"code.streamgremlin.com/streamgremlin/internal/updates"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: release winversion|ico|plist|tarball")
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
	case cmd == "stamp" && len(args) == 2:
		sec, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return err
		}
		return release.Stamp(args[0], time.Unix(sec, 0))
	case cmd == "tarball" && len(args) == 4:
		sec, err := strconv.ParseInt(args[3], 10, 64)
		if err != nil {
			return err
		}
		return release.WriteTarball(args[0], args[1], args[2], time.Unix(sec, 0))
	case cmd == "keygen" && len(args) == 1:
		priv, pub, err := updates.NewKey()
		if err != nil {
			return err
		}
		// O_EXCL: never overwrite a key; losing it means testers update by hand.
		f, err := os.OpenFile(args[0], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // The path the user names.
		if err != nil {
			return err
		}
		if _, err := f.Write(priv); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Println("Public key, for internal/updates/keys.go:", pub)
		return nil
	case cmd == "sign" && len(args) == 3:
		return sign(args[0], args[1], args[2])
	case cmd == "verify" && len(args) == 2:
		sums, err := os.ReadFile(args[0]) //nolint:gosec // The path the user names.
		if err != nil {
			return err
		}
		sig, err := os.ReadFile(args[1]) //nolint:gosec // The path the user names.
		if err != nil {
			return err
		}
		if err := updates.Verify(updates.TrustedKeys(), sums, sig); err != nil {
			return err
		}
		fmt.Println("signature OK")
		return nil
	case cmd == "sbom" && len(args) >= 6:
		return sbom(args)
	}
	return errors.New("usage: release winversion VERSION | ico OUT PNG... | plist VERSION OUT | tarball OUT DIR PREFIX UNIXTIME | stamp DIR UNIXTIME | sbom OUT DOWNLOAD VERSION SYSTEM UNIXTIME PART [PART ...] | keygen KEYFILE | sign KEYFILE SUMS OUT | verify SUMS SIG")
}

func sbom(args []string) error {
	sec, err := strconv.ParseInt(args[4], 10, 64)
	if err != nil {
		return err
	}
	spec := release.SBOMSpec{Download: args[1], Version: args[2], System: args[3], Time: time.Unix(sec, 0)}
	index := map[string]int{}
	var lists [][2]string
	for _, a := range args[5:] {
		kind, rest, ok1 := strings.Cut(a, ":")
		name, file, ok2 := strings.Cut(rest, "=")
		if !ok1 || !ok2 || name == "" || file == "" {
			return fmt.Errorf("part %q isn't go:NAME=FILE, file:NAME=FILE or npm:NAME=LIST", a)
		}
		switch kind {
		case "go", "file":
			if _, dup := index[name]; dup {
				return fmt.Errorf("%s is listed twice", name)
			}
			index[name] = len(spec.Parts)
			spec.Parts = append(spec.Parts, release.Part{Name: name, File: file, Go: kind == "go"})
		case "npm":
			lists = append(lists, [2]string{name, file})
		default:
			return fmt.Errorf("part %q should start with go, file or npm", a)
		}
	}
	for _, l := range lists {
		i, ok := index[l[0]]
		if !ok {
			return fmt.Errorf("npm:%s names no part", l[0])
		}
		spec.Parts[i].NPM = append(spec.Parts[i].NPM, l[1])
	}
	b, err := release.SBOM(spec)
	if err != nil {
		return err
	}
	return os.WriteFile(args[0], b, 0o644) //nolint:gosec // Published beside the download.
}

// sign signs sums with the key in keyFile, refusing a key file others can
// read, and checks the signature with the app's own keys before writing.
func sign(keyFile, sumsFile, out string) error {
	st, err := os.Stat(keyFile) //nolint:gosec // The path the user names.
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s can be read by others; chmod 600 it", keyFile)
	}
	text, err := os.ReadFile(keyFile) //nolint:gosec // The path the user names.
	if err != nil {
		return err
	}
	priv, err := updates.ParsePrivateKey(text)
	if err != nil {
		return err
	}
	sums, err := os.ReadFile(sumsFile) //nolint:gosec // The path the user names.
	if err != nil {
		return err
	}
	if _, err := updates.ParseSums(sums); err != nil {
		return err
	}
	sig := updates.Sign(priv, sums)
	if err := updates.Verify(updates.TrustedKeys(), sums, sig); err != nil {
		return errors.New("that key isn't one the app trusts (internal/updates/keys.go)")
	}
	return os.WriteFile(out, sig, 0o644) //nolint:gosec // Published with the release.
}
