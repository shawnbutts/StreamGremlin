package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/events"
	"code.streamgremlin.com/streamgremlin/internal/quotes"
)

const quotesUsage = `usage:
  streamgremlin quotes [-q TEXT] [-json]       list quotes, newest first
  streamgremlin quotes add [-by NAME] TEXT    add one
  streamgremlin quotes show N                 show quote #N
  streamgremlin quotes edit N TEXT            change its text
  streamgremlin quotes delete N               delete it (later quotes keep their numbers)

N is a number, like 5 or #5. In chat, the templates !quote, !quoteadd and
!quotedel (the app's Workflows → New workflow → Quotes) do the same.`

type quoteList struct {
	Quotes []quotes.Quote `json:"quotes"`
	Count  int            `json:"count"`
	Next   int64          `json:"next"`
}

func showQuote(q quotes.Quote) string {
	s := fmt.Sprintf("#%d %s", q.ID, q.Text)
	var about []string
	if q.AddedBy != "" {
		about = append(about, "added by "+q.AddedBy)
	}
	if q.Category != "" {
		about = append(about, q.Category)
	}
	about = append(about, q.CreatedAt.Local().Format("2 Jan 2006"))
	return s + " (" + strings.Join(about, ", ") + ")"
}

func quotesCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, quotesUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	num := func(s string) (string, bool) {
		n, ok := quotes.ParseNumber(s)
		if !ok {
			fmt.Fprintf(stderr, "%q isn't a quote number\n", s)
		}
		return fmt.Sprint(n), ok
	}
	if len(args) > 0 {
		switch args[0] {
		case "add":
			fs := flag.NewFlagSet("quotes add", flag.ContinueOnError)
			fs.SetOutput(stderr)
			by := fs.String("by", "you", "who added it")
			if err := fs.Parse(args[1:]); err != nil || fs.NArg() == 0 {
				fmt.Fprintln(stderr, quotesUsage)
				return 2
			}
			body := map[string]string{"text": strings.Join(fs.Args(), " "), "added_by": *by, "request_id": events.NewID()}
			var q quotes.Quote
			if code := apiExit(c.Do(ctx, "POST", "/v1/quotes", body, &q), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Added quote #%d.\n", q.ID)
			return 0
		case "show", "delete", "edit":
			if len(args) < 2 || (args[0] == "edit") != (len(args) > 2) {
				fmt.Fprintln(stderr, quotesUsage)
				return 2
			}
			n, ok := num(args[1])
			if !ok {
				return 2
			}
			path := "/v1/quotes/" + url.PathEscape(n)
			switch args[0] {
			case "delete":
				if code := apiExit(c.Do(ctx, "DELETE", path, nil, nil), stderr); code != 0 {
					return code
				}
				fmt.Fprintf(stdout, "Deleted quote #%s.\n", n)
				return 0
			case "edit":
				var list quoteList
				if code := apiExit(c.Do(ctx, "GET", "/v1/quotes?q="+url.QueryEscape("#"+n)+"&limit=1000", nil, &list), stderr); code != 0 {
					return code
				}
				var old quotes.Quote
				for _, q := range list.Quotes {
					if fmt.Sprint(q.ID) == n {
						old = q
					}
				}
				body := map[string]string{"text": strings.Join(args[2:], " "), "added_by": old.AddedBy, "category": old.Category}
				var q quotes.Quote
				if code := apiExit(c.Do(ctx, "PUT", path, body, &q), stderr); code != 0 {
					return code
				}
				fmt.Fprintln(stdout, showQuote(q))
				return 0
			}
			var list quoteList
			if code := apiExit(c.Do(ctx, "GET", "/v1/quotes?q="+url.QueryEscape("#"+n), nil, &list), stderr); code != 0 {
				return code
			}
			for _, q := range list.Quotes {
				if fmt.Sprint(q.ID) == n {
					fmt.Fprintln(stdout, showQuote(q))
					return 0
				}
			}
			fmt.Fprintf(stderr, "there's no quote #%s\n", n)
			return 1
		}
	}
	fs := flag.NewFlagSet("quotes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	search := fs.String("q", "", "only quotes containing TEXT")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, quotesUsage)
		return 2
	}
	var list quoteList
	if code := apiExit(c.Do(ctx, "GET", "/v1/quotes?q="+url.QueryEscape(*search), nil, &list), stderr); code != 0 {
		return code
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return 0
	}
	if len(list.Quotes) == 0 {
		fmt.Fprintln(stdout, "No quotes. Add one with: streamgremlin quotes add I meant to do that")
		return 0
	}
	for _, q := range list.Quotes {
		fmt.Fprintln(stdout, showQuote(q))
	}
	fmt.Fprintf(stdout, "\n%d quotes.\n", list.Count)
	return 0
}
