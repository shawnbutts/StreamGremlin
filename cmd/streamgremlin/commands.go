package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/commands"
)

// commandsCmd lists chat commands and what answers each (one answer per
// command, docs/adr/0011-command-ownership.md).
func commandsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: streamgremlin commands")
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out struct{ Commands []commands.Entry }
	if code := apiExit(c.Do(ctx, "GET", "/v1/commands", nil, &out), stderr); code != 0 {
		return code
	}
	if len(out.Commands) == 0 {
		fmt.Fprintln(stdout, "No workflow answers a chat command yet.")
	}
	for _, e := range out.Commands {
		var who []string
		for _, o := range e.Owners {
			s := o.Name + " (" + o.Owner + ")"
			if len(o.Roles) > 0 {
				s += " for " + strings.Join(o.Roles, ", ")
			}
			if len(o.Sources) > 0 {
				s += " on " + strings.Join(o.Sources, ", ")
			}
			if !o.Enabled {
				s += " [off]"
			}
			who = append(who, s)
		}
		line := fmt.Sprintf("!%-20s %s", e.Command, strings.Join(who, "; "))
		if e.Conflict {
			line += "\n  ! More than one that's on answers this: turn one off."
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}
