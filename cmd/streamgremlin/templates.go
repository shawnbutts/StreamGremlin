package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/templates"
)

const templatesUsage = `usage:
  streamgremlin templates          list the ready-made workflows
  streamgremlin templates show ID  print one, to save with: workflows save FILE

Blanks (shown as "") must be filled in before saving.`

func templatesCmd(dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 2 || (len(args) >= 1 && args[0] != "show") || (len(args) == 1) {
		fmt.Fprintln(stderr, templatesUsage)
		return 2
	}
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out struct{ Templates []templates.Template }
	if code := apiExit(c.Do(ctx, "GET", "/v1/templates", nil, &out), stderr); code != 0 {
		return code
	}
	if len(args) == 0 {
		for _, t := range out.Templates {
			blanks := ""
			if len(t.Blanks) > 0 {
				blanks = fmt.Sprintf(" (%d to fill in)", len(t.Blanks))
			}
			fmt.Fprintf(stdout, "%-10s %-14s %s: %s%s\n", t.ID, t.Category, t.Title, t.Description, blanks)
		}
		return 0
	}
	for _, t := range out.Templates {
		if t.ID != args[1] {
			continue
		}
		var b bytes.Buffer
		_ = json.Indent(&b, t.Workflow, "", "  ")
		fmt.Fprintln(stdout, b.String())
		for _, bl := range t.Blanks {
			fmt.Fprintf(stderr, "Fill in %s: %s\n", bl.Path, bl.Hint)
		}
		return 0
	}
	fmt.Fprintf(stderr, "no template %q; see streamgremlin templates\n", args[1])
	return 1
}
