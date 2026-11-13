// Command mockdiscord runs a stand-in for Discord's webhooks, to try
// StreamGremlin's Discord posts without Discord:
//
//	mockdiscord -addr 127.0.0.1:7600
//
// It prints a webhook link to paste on Connections (start the engine with
// -discord-api-url http://127.0.0.1:7600). Its /panel page lists the posts.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/mockdiscord"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7600", "address to listen on")
	flag.Parse()
	m := mockdiscord.New()
	id, token := m.AddWebhook("Stream announcements")
	fmt.Fprintf(os.Stderr, "mockdiscord on http://%s (panel: http://%s/panel)\nwebhook link: https://discord.com/api/webhooks/%s/%s\n", *addr, *addr, id, token)
	srv := &http.Server{Addr: *addr, Handler: m, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
