package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
)

const adsUsage = `usage:
  streamgremlin ads                 the ad schedule, and recent requests and breaks
  streamgremlin ads run SECONDS     ask Twitch for an ad break now (1 to 180 seconds)
  streamgremlin ads snooze          push the next automatic ad back 5 minutes

Ads need an Affiliate or Partner channel, live. A request is sent once and
never retried. A break is shown only when Twitch says it began; its end is
an estimate, because Twitch doesn't say when an ad ends.`

func adsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, adsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	usage := func() int { fmt.Fprintln(stderr, adsUsage); return 2 }
	// One key per command: if the connection drops and it's run through
	// again by the client, Twitch is still asked once.
	key := make([]byte, 12)
	_, _ = rand.Read(key)
	op := "cli-" + hex.EncodeToString(key)
	when := func(t *time.Time) string {
		if t == nil {
			return "unknown"
		}
		return t.Local().Format("15:04:05")
	}
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch {
	case sub == "" && len(args) == 0:
		var v api.AdsView
		if code := apiExit(c.Do(ctx, "GET", "/v1/ads", nil, &v), stderr); code != 0 {
			return code
		}
		if v.Schedule == nil {
			fmt.Fprintln(stdout, "Schedule: unknown:", v.Detail)
		} else {
			sc := v.Schedule
			fmt.Fprintf(stdout, "Next ad: %s (%d seconds). Snoozes left: %d. Last ad: %s.\n", when(sc.NextAdAt), sc.Duration, sc.SnoozeCount,
				when(sc.LastAdAt))
		}
		for _, b := range v.Breaks {
			kind := "you ran it"
			if b.Automatic {
				kind = "automatic"
			}
			fmt.Fprintf(stdout, "Break %s, %d seconds (%s), ends about %s (estimated)\n", b.StartedAt.Local().Format("15:04:05"),
				b.Duration, kind, b.EndEstimatedAt.Local().Format("15:04:05"))
		}
		for _, q := range v.Requests {
			what := "snooze"
			if q.Action == "commercial" {
				what = fmt.Sprintf("ad, %d seconds", q.Length)
			}
			line := fmt.Sprintf("Asked %s: %s, %s", q.CreatedAt.Local().Format("15:04:05"), what, q.Status)
			if q.Message != "" && q.Status != "accepted" {
				line += ": " + q.Message
			}
			fmt.Fprintln(stdout, line)
		}
		return 0
	case sub == "run" && len(args) == 1:
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return usage()
		}
		if code := apiExit(c.Do(ctx, "POST", "/v1/ads/commercial", map[string]any{"length": n, "op_key": op}, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "Twitch accepted it. The break shows here when Twitch says it began.")
		return 0
	case sub == "snooze" && len(args) == 0:
		var q struct {
			NextAdAt    *time.Time `json:"next_ad_at"`
			SnoozeCount *int       `json:"snooze_count"`
		}
		if code := apiExit(c.Do(ctx, "POST", "/v1/ads/snooze", map[string]any{"op_key": op}, &q), stderr); code != 0 {
			return code
		}
		left := "unknown"
		if q.SnoozeCount != nil {
			left = strconv.Itoa(*q.SnoozeCount)
		}
		fmt.Fprintf(stdout, "Snoozed: the next ad is at %s. Snoozes left: %s.\n", when(q.NextAdAt), left)
		return 0
	}
	return usage()
}
