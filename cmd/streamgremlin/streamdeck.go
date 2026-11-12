package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/clients"
)

const streamdeckUsage = `usage:
  streamgremlin streamdeck [-json]        list paired Stream Decks
  streamgremlin streamdeck pair [-wait=false] [-json]
                                          show a code to type into the
                                          StreamGremlin plugin; waits until
                                          it's used (Ctrl+C stops pairing)
  streamgremlin streamdeck forget ID      unpair one: its token stops working

A paired Stream Deck can only see and press your buttons.`

func streamdeckCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, streamdeckUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch {
	case len(args) == 2 && args[0] == "forget":
		if code := apiExit(c.Do(ctx, "DELETE", "/v1/clients/"+url.PathEscape(args[1]), nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "Forgotten. That Stream Deck can't reach StreamGremlin until it's paired again.")
		return 0
	case len(args) >= 1 && args[0] == "pair":
		return streamdeckPair(ctx, c.Do, args[1:], stdout, stderr)
	case len(args) <= 1:
		fs := flag.NewFlagSet("streamdeck", flag.ContinueOnError)
		fs.SetOutput(stderr)
		asJSON := fs.Bool("json", false, "")
		if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
			fmt.Fprintln(stderr, streamdeckUsage)
			return 2
		}
		var out struct {
			Clients []clients.Client `json:"clients"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/clients", nil, &out), stderr); code != 0 {
			return code
		}
		if *asJSON {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)
			return 0
		}
		if len(out.Clients) == 0 {
			fmt.Fprintln(stdout, "No Stream Deck is paired. Pair one with: streamgremlin streamdeck pair")
			return 0
		}
		for _, cl := range out.Clients {
			used := "never used"
			if cl.LastUsedAt != nil {
				used = "last used " + cl.LastUsedAt.Local().Format("2 Jan 15:04")
			}
			fmt.Fprintf(stdout, "%-20s %-30s paired %s, %s\n", cl.ID, cl.Name, cl.CreatedAt.Local().Format("2 Jan 2006"), used)
		}
		return 0
	}
	fmt.Fprintln(stderr, streamdeckUsage)
	return 2
}

func streamdeckPair(ctx context.Context, do func(context.Context, string, string, any, any) error, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("streamdeck pair", flag.ContinueOnError)
	fs.SetOutput(stderr)
	wait := fs.Bool("wait", true, "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, streamdeckUsage)
		return 2
	}
	var p clients.Pairing
	if code := apiExit(do(ctx, "POST", "/v1/clients/pairing", map[string]string{"kind": clients.StreamDeck}, &p), stderr); code != 0 {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(p)
	} else {
		fmt.Fprintf(stdout, "Type this code in the StreamGremlin plugin's settings on your Stream Deck:\n\n    %s\n\nIt works once, until %s.\n",
			p.Code, p.ExpiresAt.Local().Format("15:04"))
	}
	if !*wait {
		return 0
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// Stopped by hand: the code shouldn't stay usable.
			_ = do(context.Background(), "DELETE", "/v1/clients/pairing", nil, nil)
			fmt.Fprintln(stdout, "Stopped pairing; that code no longer works.")
			return 1
		case <-tick.C:
		}
		var st clients.State
		if code := apiExit(do(ctx, "GET", "/v1/clients/pairing", nil, &st), stderr); code != 0 {
			return code
		}
		switch st.State {
		case clients.StateWaiting:
			continue
		case clients.StatePaired:
			fmt.Fprintf(stdout, "Paired %q. It can see and press your buttons.\n", st.Client.Name)
			return 0
		case clients.StateExpired:
			fmt.Fprintln(stderr, "The code ran out before it was used. Pair again for a new one.")
		case clients.StateLocked:
			fmt.Fprintln(stderr, "Too many wrong codes were tried, so this one stopped working. Pair again for a new one.")
		default:
			fmt.Fprintln(stderr, "Pairing was stopped, here or in the app.")
		}
		return 1
	}
}
