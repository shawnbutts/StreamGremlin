// Command mockyoutube runs a stand-in for Google's sign-in and YouTube's
// Data API, to try StreamGremlin's YouTube sign-in without Google:
//
//	mockyoutube -addr 127.0.0.1:7700
//
// Start the engine with -youtube-url http://127.0.0.1:7700 (or the app
// with STREAMGREMLIN_YOUTUBE_URL), and paste any client ID shaped like
// Google's on Connections. Signing in shows a page to pick a channel.
// http://127.0.0.1:7700/mock goes live, chats, ends a broadcast and drops
// chat streams.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockyoutube"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7700", "address to listen on")
	flag.Parse()
	m := mockyoutube.New()
	fmt.Fprintf(os.Stderr, "%s on http://%s\nclient ID to paste: 123456789012-mockyoutubeclient0000000000.apps.googleusercontent.com\n"+
		"go live and chat on http://%s/mock\n", m, *addr, *addr)
	srv := &http.Server{Addr: *addr, Handler: m, ReadHeaderTimeout: 10 * time.Second, Protocols: mockyoutube.Protocols()}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
