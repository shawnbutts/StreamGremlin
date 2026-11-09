package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/twitch"
)

const pollsUsage = `usage:
  streamgremlin polls                                 the newest poll and prediction
  streamgremlin polls start [-seconds 60] [-points N] QUESTION CHOICE CHOICE [CHOICE…]
  streamgremlin polls end [-remove]                   end the running poll
  streamgremlin polls predict [-seconds 120] QUESTION OUTCOME OUTCOME [OUTCOME…]
  streamgremlin polls lock                            stop taking bets
  streamgremlin polls resolve WINNER                  an outcome's words or number
  streamgremlin polls cancel                          cancel the prediction and refund everyone

Polls and predictions need an Affiliate or Partner channel. Twitch runs one
of each at a time.`

func pollsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, pollsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	usage := func() int { fmt.Fprintln(stderr, pollsUsage); return 2 }
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "":
		var v struct {
			State      string             `json:"state"`
			Detail     string             `json:"detail"`
			Poll       *twitch.Poll       `json:"poll"`
			Prediction *twitch.Prediction `json:"prediction"`
		}
		if code := apiExit(c.Do(ctx, "GET", "/v1/polls", nil, &v), stderr); code != 0 {
			return code
		}
		if v.State != "ok" {
			fmt.Fprintln(stdout, v.Detail)
			return 0
		}
		if v.Poll == nil {
			fmt.Fprintln(stdout, "No polls yet.")
		} else {
			fmt.Fprintf(stdout, "Poll (%s): %s\n", v.Poll.Status, v.Poll.Title)
			for i, ch := range v.Poll.Choices {
				fmt.Fprintf(stdout, "  %d. %-25s %d votes\n", i+1, ch.Title, ch.Votes)
			}
		}
		if v.Prediction == nil {
			fmt.Fprintln(stdout, "No predictions yet.")
		} else {
			fmt.Fprintf(stdout, "Prediction (%s): %s\n", v.Prediction.Status, v.Prediction.Title)
			for i, o := range v.Prediction.Outcomes {
				won := ""
				if o.ID == v.Prediction.WinningOutcomeID {
					won = "  ← won"
				}
				fmt.Fprintf(stdout, "  %d. %-25s %d viewers, %d points%s\n", i+1, o.Title, o.Users, o.ChannelPoints, won)
			}
		}
		return 0
	case "start":
		fs := flag.NewFlagSet("polls start", flag.ContinueOnError)
		fs.SetOutput(stderr)
		secs := fs.Int("seconds", 60, "how long it runs, 15 to 1800")
		points := fs.Int("points", 0, "channel points for each extra vote (0 for none)")
		if err := fs.Parse(args); err != nil || fs.NArg() < 3 {
			return usage()
		}
		var p twitch.Poll
		in := twitch.NewPoll{Title: fs.Arg(0), Choices: fs.Args()[1:], Seconds: *secs, ChannelPointsPerVote: *points}
		if code := apiExit(c.Do(ctx, "POST", "/v1/polls", in, &p), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Poll started: %s (%d choices, %d seconds).\n", p.Title, len(p.Choices), p.Seconds)
		return 0
	case "end":
		fs := flag.NewFlagSet("polls end", flag.ContinueOnError)
		fs.SetOutput(stderr)
		remove := fs.Bool("remove", false, "remove it from chat at once, rather than showing the results")
		if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
			return usage()
		}
		if code := apiExit(c.Do(ctx, "POST", "/v1/polls/end", map[string]bool{"archive": *remove}, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "The poll ended.")
		return 0
	case "predict":
		fs := flag.NewFlagSet("polls predict", flag.ContinueOnError)
		fs.SetOutput(stderr)
		secs := fs.Int("seconds", 120, "how long it takes bets, 30 to 1800")
		if err := fs.Parse(args); err != nil || fs.NArg() < 3 {
			return usage()
		}
		var p twitch.Prediction
		in := twitch.NewPrediction{Title: fs.Arg(0), Outcomes: fs.Args()[1:], Seconds: *secs}
		if code := apiExit(c.Do(ctx, "POST", "/v1/predictions", in, &p), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Prediction started: %s (%d outcomes, bets for %d seconds).\n", p.Title, len(p.Outcomes), p.Seconds)
		return 0
	case "lock", "resolve", "cancel":
		if (sub == "resolve") != (len(args) == 1) || len(args) > 1 {
			return usage()
		}
		winner := strings.Join(args, " ")
		var out struct {
			Prediction twitch.Prediction `json:"prediction"`
		}
		if code := apiExit(c.Do(ctx, "POST", "/v1/predictions/end", map[string]string{"how": sub, "winner": winner}, &out), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "The prediction is %s.\n", out.Prediction.Status)
		return 0
	}
	return usage()
}
