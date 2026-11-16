// Command mockgithub runs a stand-in for GitHub's releases, to try
// StreamGremlin's updates (D-120) without publishing anything:
//
//	mockgithub -addr 127.0.0.1:7460 -dir bin/release -version 0.1.0-beta.99
//
// It serves the files in -dir as release v-version, with SHA256SUMS.txt
// made from them and signed by a key it makes for the run. It prints how to
// start the app or engine against it:
//
//	STREAMGREMLIN_UPDATES_URL=http://127.0.0.1:7460 STREAMGREMLIN_UPDATES_KEY=... STREAMGREMLIN_UPDATES_VERSION=0.1.0-beta.5
//
// Only files whose names start with StreamGremlin-VERSION- are served.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockgithub"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7460", "address to listen on")
	dir := flag.String("dir", "bin/release", "folder with the release's files")
	version := flag.String("version", "", "the release's version, like 0.1.0-beta.99")
	notes := flag.String("notes", "A test release from mockgithub.", "the release's notes")
	flag.Parse()
	if *version == "" {
		fmt.Fprintln(os.Stderr, "mockgithub: say the release's version with -version")
		os.Exit(2)
	}
	files := map[string][]byte{}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mockgithub:", err)
		os.Exit(1)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "StreamGremlin-"+*version+"-") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(*dir, e.Name())) //nolint:gosec // the folder named on the command line
		if err != nil {
			fmt.Fprintln(os.Stderr, "mockgithub:", err)
			os.Exit(1)
		}
		files[e.Name()] = b
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "mockgithub: no StreamGremlin-%s-* files in %s\n", *version, *dir)
		os.Exit(1)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mockgithub:", err)
		os.Exit(1)
	}
	m := mockgithub.New()
	m.Base = "http://" + *addr
	m.Add(mockgithub.Release{Tag: "v" + *version, Prerelease: strings.Contains(*version, "-"), Notes: *notes, Files: mockgithub.Signed(files, priv)})
	fmt.Fprintf(os.Stderr, "mockgithub on http://%s: release v%s with %d files, signed for this run.\nStart StreamGremlin with:\n"+
		"  STREAMGREMLIN_UPDATES_URL=http://%s STREAMGREMLIN_UPDATES_KEY=%s STREAMGREMLIN_UPDATES_VERSION=<an older version>\n",
		*addr, *version, len(files), *addr, base64.StdEncoding.EncodeToString(pub))
	srv := &http.Server{Addr: *addr, Handler: m, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
