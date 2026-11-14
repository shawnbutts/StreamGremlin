package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/events"
)

func eventsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list", "watch":
		return listEvents(dir, sub == "watch", args, stdout, stderr)
	case "simulate":
		return simulateEvent(dir, args, stdout, stderr)
	case "types":
		return eventTypes(dir, stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown events command %q; use list, watch, simulate or types\n", sub)
	return 2
}

func listEvents(dir string, watch bool, args []string, stdout, stderr io.Writer) int {
	name := "events"
	if watch {
		name = "events watch"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 20, "how many of the newest events (1-200)")
	after := fs.Int64("after", -1, "start after this seq instead of at the newest")
	types := fs.String("types", "", "only these types, comma-separated; a prefix like chat.* matches a group")
	asJSON := fs.Bool("json", false, "print one JSON event per line")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(*limit))
	if *types != "" {
		q.Set("types", *types)
	}
	show := func(r events.Record) {
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(r)
			return
		}
		fmt.Fprintln(stdout, describe(r))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cursor := *after
	if cursor < 0 {
		var page api.EventPage
		if err := c.Do(ctx, "GET", "/v1/events?"+q.Encode(), nil, &page); err != nil {
			reportErr(err, stderr)
			return 1
		}
		for _, r := range page.Events {
			show(r)
		}
		cursor = page.Head
		if !watch {
			return 0
		}
	}
	q.Set("limit", "200")
	for {
		q.Set("after", strconv.FormatInt(cursor, 10))
		if watch {
			q.Set("wait", "20")
		}
		var page api.EventPage
		err := c.Do(ctx, "GET", "/v1/events?"+q.Encode(), nil, &page)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			reportErr(err, stderr)
			return 1
		}
		for _, r := range page.Events {
			show(r)
			cursor = r.Seq
		}
		if page.Head < cursor { // the engine's data was replaced; start again
			cursor = page.Head
		}
		if !watch && len(page.Events) == 0 {
			return 0
		}
	}
}

// describe is one line for people: seq, time, what happened, who, and a short summary.
func describe(r events.Record) string {
	var p map[string]any
	_ = json.Unmarshal(r.Payload, &p)
	var b strings.Builder
	fmt.Fprintf(&b, "#%d  %s  %s  (%s", r.Seq, r.OccurredAt.Local().Format("15:04:05"), r.Type, r.Source)
	if r.DeliveryQuality != events.Confirmed {
		b.WriteString(", " + string(r.DeliveryQuality))
	}
	b.WriteString(")")
	if r.Actor != nil {
		who := r.Actor.DisplayName
		if who == "" {
			who = r.Actor.Login
		}
		fmt.Fprintf(&b, "  %s", who)
	}
	for _, k := range []string{"text", "scene_name", "title", "names"} {
		if v, ok := p[k].(string); ok && v != "" {
			fmt.Fprintf(&b, ": %s", v)
			break
		}
	}
	return b.String()
}

func simulateEvent(dir string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("events simulate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the stored event as JSON")
	source := fs.String("source", "", "the platform it comes from, such as youtube (chat events)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: streamgremlin events simulate [-json] [-source youtube] TYPE [field=value ...]\n\nSee `streamgremlin events types` for types and fields.")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}
	sim := events.Simulation{Type: fs.Arg(0), Fields: map[string]string{}, RequestID: requestID(), Source: *source}
	for _, kv := range fs.Args()[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			fmt.Fprintf(stderr, "%q isn't field=value\n", kv)
			return 2
		}
		sim.Fields[k] = v
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var res api.SimulateResult
	err := c.Do(ctx, "POST", "/v1/events/simulate", sim, &res)
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Code == api.CodeInvalidRequest {
		fmt.Fprintln(stderr, apiErr.Message)
		return 2
	}
	if err != nil {
		reportErr(err, stderr)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(res.Event)
		return 0
	}
	fmt.Fprintln(stdout, "Stored test event "+describe(res.Event))
	return 0
}

func eventTypes(dir string, stdout, stderr io.Writer) int {
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cat api.EventCatalog
	if err := c.Do(ctx, "GET", "/v1/events/catalog", nil, &cat); err != nil {
		reportErr(err, stderr)
		return 1
	}
	for _, k := range cat.Kinds {
		fmt.Fprintf(stdout, "%s  (%s, from %s)\n  %s\n", k.Type, k.Label, strings.Join(k.Sources, ", "), k.Description)
		for _, f := range k.Fields {
			fmt.Fprintf(stdout, "    %s=%q  %s\n", f.Name, f.Default, f.Label)
		}
	}
	return 0
}

func requestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "cli-" + hex.EncodeToString(b[:])
}
