package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/filters"
)

const filtersUsage = `usage:
  streamgremlin filters                          the chat filters' settings, and what they caught lately
  streamgremlin filters check [-role R] TEXT     which filter would catch TEXT (role: viewer, subscriber, vip, moderator)

Change the filters on the Chat filters screen.`

var ruleNames = map[string]string{filters.RuleWords: "Words", filters.RuleLinks: "Links", filters.RuleEmotes: "Emotes",
	filters.RuleCaps: "Capitals", filters.RuleSymbols: "Symbols", filters.RuleRepeats: "Repeats"}

func filtersCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, filtersUsage)
		return 0
	}
	if len(args) > 0 && args[0] != "check" {
		fmt.Fprintln(stderr, filtersUsage)
		return 2
	}
	var in api.FilterCheck
	if len(args) > 0 {
		fs := flag.NewFlagSet("filters check", flag.ContinueOnError)
		fs.SetOutput(stderr)
		role := fs.String("role", "viewer", "who sends it")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() == 0 {
			fmt.Fprintln(stderr, filtersUsage)
			return 2
		}
		in = api.FilterCheck{Text: strings.Join(fs.Args(), " "), Role: *role}
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if len(args) > 0 {
		var v api.FilterVerdict
		if code := apiExit(c.Do(ctx, "POST", "/v1/filters/check", in, &v), stderr); code != 0 {
			return code
		}
		if v.Rule == "" {
			fmt.Fprintln(stdout, "No filter that's on would catch it.")
		} else {
			fmt.Fprintf(stdout, "%s would catch it: %s\n", ruleNames[v.Rule], v.Reason)
		}
		return 0
	}
	var v api.FiltersView
	if code := apiExit(c.Do(ctx, "GET", "/v1/filters", nil, &v), stderr); code != 0 {
		return code
	}
	s := v.Settings
	fmt.Fprintf(stdout, "Chat filters: %s\n", map[bool]string{true: "on", false: "off"}[s.On])
	for _, name := range filters.Order {
		r, _ := s.RuleFor(name)
		state := "off"
		if r.On {
			state = r.Action
			if r.Action == filters.ActionTimeout {
				state = fmt.Sprintf("timeout %ds", r.Seconds)
			}
		}
		var alone []string
		if r.ExemptVIPs {
			alone = append(alone, "VIPs")
		}
		if r.ExemptSubscribers {
			alone = append(alone, "subscribers")
		}
		extra := ""
		switch name {
		case filters.RuleWords:
			extra = fmt.Sprintf(", %d words", len(s.Words.List))
		case filters.RuleLinks:
			extra = ", allowed: " + strings.Join(s.Links.Allowed, " ")
		}
		fmt.Fprintf(stdout, "  %-9s %-14s leaves alone: %s%s\n", ruleNames[name], state, nobodyElse(alone), extra)
	}
	if len(v.Recent) == 0 {
		fmt.Fprintln(stdout, "Nothing caught lately.")
		return 0
	}
	fmt.Fprintln(stdout, "Caught lately:")
	for _, c := range v.Recent {
		fmt.Fprintf(stdout, "  %s  %-9s %-8s %-8s %s: %s\n", c.At.Local().Format("Jan 2 15:04"), ruleNames[c.Rule], c.Action, c.State, c.Login, c.Text)
	}
	return 0
}

func nobodyElse(list []string) string {
	if len(list) == 0 {
		return "nobody else"
	}
	return strings.Join(list, ", ")
}
