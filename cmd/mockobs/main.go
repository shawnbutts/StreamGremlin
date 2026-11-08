// Command mockobs runs a stand-in for OBS's WebSocket server, to try
// StreamGremlin without OBS:
//
//	mockobs -addr 127.0.0.1:4456 -password secret
//
// Point StreamGremlin's OBS connection at ws://127.0.0.1:4456. Its /panel
// page switches scenes by hand, as a streamer would in OBS.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockobs"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4456", "address to listen on (OBS itself uses 4455)")
	password := flag.String("password", "", "require this password, as OBS's server can")
	flag.Parse()
	m := mockobs.New()
	m.SetPassword(*password)
	fmt.Fprintf(os.Stderr, "mockobs on ws://%s (panel: http://%s/panel)\n", *addr, *addr)
	srv := &http.Server{Addr: *addr, Handler: m, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
