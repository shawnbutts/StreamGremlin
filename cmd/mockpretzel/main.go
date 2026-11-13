// Command mockpretzel runs a stand-in for Pretzel's now-playing address,
// to try StreamGremlin's Now playing overlay without Pretzel:
//
//	mockpretzel -addr 127.0.0.1:7700 -id 12345
//
// Start the engine with -pretzel-api-url http://127.0.0.1:7700. Its
// /panel page changes the song.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockpretzel"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7700", "address to listen on")
	id := flag.String("id", "", "a Twitch user id to start playing a song for (your account's, or mocktwitch's)")
	flag.Parse()
	m := mockpretzel.New()
	if *id != "" {
		m.Set(*id, mockpretzel.Line("Caffeine Part 2", "Pretzel Artist", "https://prtzl.io/mock"))
	}
	fmt.Fprintf(os.Stderr, "mockpretzel on http://%s (panel: http://%s/panel)\n", *addr, *addr)
	srv := &http.Server{Addr: *addr, Handler: m, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
