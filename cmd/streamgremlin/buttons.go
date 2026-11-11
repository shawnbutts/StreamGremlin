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

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/buttons"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

const buttonsUsage = `usage:
  streamgremlin buttons [-json]          list your buttons and the workflows each starts
  streamgremlin buttons press ID         press one: its workflows run for real
  streamgremlin buttons set ID [flags]   create or change one:
      -label TEXT     what it says (required for a new button)
      -color NAME     red, orange, yellow, green, teal, blue, purple, pink, or none
  streamgremlin buttons move ID...       put them in this order (name every button)
  streamgremlin buttons delete ID        delete one

The Live screen shows your buttons. A workflow starts from one with the
trigger "You press a button" (buttons.button.pressed, where button_id is
the button's ID).`

func buttonsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, buttonsUsage)
		return 0
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := func(id string) string { return "/v1/buttons/" + url.PathEscape(id) }
	if len(args) >= 2 {
		id := args[1]
		switch args[0] {
		case "press":
			var out api.PressResult
			if code := apiExit(c.Do(ctx, "POST", path(id)+"/press", map[string]string{"from": buttons.FromCLI}, &out), stderr); code != 0 {
				return code
			}
			if len(out.Started) == 0 {
				fmt.Fprintf(stdout, "Pressed %s. No workflow that's on starts from it.\n", out.Button.Label)
				return 0
			}
			fmt.Fprintf(stdout, "Pressed %s. Started: %s.\n", out.Button.Label, workflowNames(out.Started))
			return 0
		case "delete":
			if code := apiExit(c.Do(ctx, "DELETE", path(id), nil, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Deleted %s.\n", id)
			return 0
		case "move":
			if code := apiExit(c.Do(ctx, "PUT", "/v1/button-order", map[string][]string{"order": args[1:]}, nil), stderr); code != 0 {
				return code
			}
			fmt.Fprintf(stdout, "Moved: %s.\n", strings.Join(args[1:], ", "))
			return 0
		case "set":
			return buttonSet(ctx, c.Do, id, args[2:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("buttons", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, buttonsUsage)
		return 2
	}
	var out api.ButtonList
	if code := apiExit(c.Do(ctx, "GET", "/v1/buttons", nil, &out), stderr); code != 0 {
		return code
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}
	if len(out.Buttons) == 0 {
		fmt.Fprintln(stdout, "No buttons. Make one on the Live screen, or with: streamgremlin buttons set ID -label TEXT")
		return 0
	}
	for _, b := range out.Buttons {
		starts := "starts nothing that's on"
		if len(b.Starts) > 0 {
			starts = "starts " + workflowNames(b.Starts)
		}
		fmt.Fprintf(stdout, "%-20s %-24s %s\n", b.ID, b.Label, starts)
	}
	return 0
}

func buttonSet(ctx context.Context, do func(context.Context, string, string, any, any) error, id string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("buttons set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	label := fs.String("label", "", "")
	color := fs.String("color", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, buttonsUsage)
		return 2
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var list api.ButtonList
	if code := apiExit(do(ctx, "GET", "/v1/buttons", nil, &list), stderr); code != 0 {
		return code
	}
	b := buttons.Button{ID: id}
	for _, have := range list.Buttons {
		if have.ID == id {
			b = have.Button
		}
	}
	if set["label"] {
		b.Label = *label
	}
	if set["color"] {
		b.Color = *color
		if b.Color == "none" {
			b.Color = ""
		}
	}
	var saved buttons.Button
	if code := apiExit(do(ctx, "PUT", "/v1/buttons/"+url.PathEscape(id), b, &saved), stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "Saved %s (%s).\n", saved.Label, saved.ID)
	return 0
}

func workflowNames(ws []workflow.Summary) string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Name
	}
	return strings.Join(out, ", ")
}
