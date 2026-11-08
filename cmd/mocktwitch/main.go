// Command mocktwitch runs a stand-in for Twitch's sign-in server, to try
// StreamGremlin without a registered Twitch app:
//
//	mocktwitch -addr 127.0.0.1:7441
//	STREAMGREMLIN_TWITCH_AUTH_URL=http://127.0.0.1:7441 ./bin/app/StreamGremlin
//
// Sign-in links open its /activate page, where any login can be approved.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mocktwitch"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7441", "address to listen on")
	approve := flag.String("auto-approve", "", "approve every sign-in code as this login after a second")
	flag.Parse()
	m := mocktwitch.New()
	m.SetBase("http://" + *addr)
	if *approve != "" {
		go func() {
			for range time.Tick(time.Second) {
				for _, c := range m.Pending() {
					_ = m.Approve(c, *approve)
				}
			}
		}()
	}
	fmt.Fprintf(os.Stderr, "mocktwitch on http://%s (activate page: http://%s/activate)\n", *addr, *addr)
	srv := &http.Server{Addr: *addr, Handler: m, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
