package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/api"
	"code.streamgremlin.com/streamgremlin/internal/client"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

const workflowsUsage = `usage:
  streamgremlin workflows                 list them
  streamgremlin workflows show ID         print the definition
  streamgremlin workflows save FILE       add or update one (new ones start on)
  streamgremlin workflows on|off ID       turn one on or off
  streamgremlin workflows groups          list groups, with their switches
  streamgremlin workflows group NAME on|off   switch a group (each workflow keeps its own switch)
  streamgremlin workflows group NAME rename NEW
  streamgremlin workflows delete ID       delete one (it can be restored)
  streamgremlin workflows deleted         list deleted ones
  streamgremlin workflows restore ID      bring one back, turned off
  streamgremlin runs [-workflow ID]       the newest runs
  streamgremlin runs show RUN             one run's trace
  streamgremlin runs cancel RUN           stop a waiting or running run`

func workflowsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch {
	case sub == "list" && len(args) == 0:
		var out struct{ Workflows []workflow.Summary }
		if code := apiExit(c.Do(ctx, "GET", "/v1/workflows", nil, &out), stderr); code != 0 {
			return code
		}
		if len(out.Workflows) == 0 {
			fmt.Fprintln(stdout, "No workflows yet. Add one with `streamgremlin workflows save FILE`.")
		}
		for _, w := range out.Workflows {
			state := "off"
			switch {
			case w.Running:
				state = "on "
			case w.Enabled:
				state = "on, group off"
			}
			if w.Group != "" {
				state += "  [" + w.Group + "]"
			}
			var types []string
			for _, t := range w.Triggers {
				types = append(types, t.Type)
			}
			last := ""
			if w.LastJob != nil {
				last = "  last run: " + w.LastJob.Status
			}
			fmt.Fprintf(stdout, "%s  %-28s %s (v%d) when %s%s\n", state, w.ID, w.Name, w.Version, strings.Join(types, ", "), last)
		}
		return 0
	case sub == "show" && len(args) == 1:
		var out api.WorkflowDetail
		if code := apiExit(c.Do(ctx, "GET", "/v1/workflows/"+url.PathEscape(args[0]), nil, &out), stderr); code != 0 {
			return code
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out.Definition)
		return 0
	case sub == "save" && len(args) == 1:
		raw, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		var probe struct{ ID string }
		if json.Unmarshal(raw, &probe) != nil || probe.ID == "" {
			fmt.Fprintln(stderr, "that file isn't a workflow definition (no id)")
			return 2
		}
		var out struct {
			Workflow   workflow.Summary
			Changed    bool
			OffBecause string `json:"off_because"`
		}
		err = c.DoRaw(ctx, "PUT", "/v1/workflows/"+url.PathEscape(probe.ID), raw, &out)
		var pe *client.APIError
		if errors.As(err, &pe) && len(pe.Problems) > 0 {
			fmt.Fprintln(stderr, "The workflow has problems:")
			for _, p := range pe.Problems {
				fmt.Fprintf(stderr, "  %s: %s\n", p.Path, p.Message)
			}
			return 2
		}
		if code := apiExit(err, stderr); code != 0 {
			return code
		}
		switch {
		case !out.Changed:
			fmt.Fprintf(stdout, "%s is unchanged (v%d).\n", out.Workflow.ID, out.Workflow.Version)
		case out.Workflow.Enabled:
			fmt.Fprintf(stdout, "Saved %s as v%d. It's on.\n", out.Workflow.ID, out.Workflow.Version)
		case out.OffBecause != "":
			fmt.Fprintf(stdout, "Saved %s as v%d. It's off: %s\n", out.Workflow.ID, out.Workflow.Version, out.OffBecause)
		default:
			fmt.Fprintf(stdout, "Saved %s as v%d. It's off: turn it on with `streamgremlin workflows on %s`.\n", out.Workflow.ID, out.Workflow.Version, out.Workflow.ID)
		}
		return 0
	case sub == "groups" && len(args) == 0:
		var out struct{ Groups []workflow.GroupSummary }
		if code := apiExit(c.Do(ctx, "GET", "/v1/workflow-groups", nil, &out), stderr); code != 0 {
			return code
		}
		if len(out.Groups) == 0 {
			fmt.Fprintln(stdout, "No groups yet. A workflow's \"group\" puts it in one.")
		}
		for _, g := range out.Groups {
			state := "on "
			if !g.Enabled {
				state = "off"
			}
			fmt.Fprintf(stdout, "%s  %-24s %d workflows, %d switched on\n", state, g.Name, g.Workflows, g.On)
		}
		return 0
	case sub == "group" && len(args) == 2 && (args[1] == "on" || args[1] == "off"):
		path := "/v1/workflow-groups/" + url.PathEscape(args[0]) + "/enabled"
		if code := apiExit(c.Do(ctx, "PUT", path, map[string]bool{"enabled": args[1] == "on"}, nil), stderr); code != 0 {
			return code
		}
		if args[1] == "on" {
			fmt.Fprintf(stdout, "%s is on: its workflows that are switched on run.\n", args[0])
		} else {
			fmt.Fprintf(stdout, "%s is off: none of its workflows run. Each keeps its own switch.\n", args[0])
		}
		return 0
	case sub == "group" && len(args) == 3 && args[1] == "rename":
		var out struct{ Name string }
		if code := apiExit(c.Do(ctx, "PUT", "/v1/workflow-groups/"+url.PathEscape(args[0]), map[string]string{"name": args[2]}, &out), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Renamed %s to %s.\n", args[0], out.Name)
		return 0
	case sub == "deleted" && len(args) == 0:
		var out struct{ Workflows []workflow.Summary }
		if code := apiExit(c.Do(ctx, "GET", "/v1/workflows?deleted=true", nil, &out), stderr); code != 0 {
			return code
		}
		if len(out.Workflows) == 0 {
			fmt.Fprintln(stdout, "No deleted workflows.")
		}
		for _, w := range out.Workflows {
			when := ""
			if w.DeletedAt != nil {
				when = "  deleted " + w.DeletedAt.Local().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(stdout, "%-28s %s (v%d)%s\n", w.ID, w.Name, w.Version, when)
		}
		return 0
	case sub == "delete" && len(args) == 1:
		if code := apiExit(c.Do(ctx, "DELETE", "/v1/workflows/"+url.PathEscape(args[0]), nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Deleted %s. Bring it back with `streamgremlin workflows restore %s`.\n", args[0], args[0])
		return 0
	case sub == "restore" && len(args) == 1:
		if code := apiExit(c.Do(ctx, "POST", "/v1/workflows/"+url.PathEscape(args[0])+"/restore", nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Restored %s. It's off: turn it on with `streamgremlin workflows on %s`.\n", args[0], args[0])
		return 0
	case (sub == "on" || sub == "off") && len(args) == 1:
		if code := apiExit(c.Do(ctx, "PUT", "/v1/workflows/"+url.PathEscape(args[0])+"/enabled", map[string]bool{"enabled": sub == "on"}, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "%s is %s.\n", args[0], sub)
		return 0
	}
	fmt.Fprintln(stderr, workflowsUsage)
	return 2
}

func runsCmd(dir string, args []string, stdout, stderr io.Writer) int {
	c, ok := connect(dir, stderr)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch {
	case len(args) == 0 || (len(args) == 2 && args[0] == "-workflow"):
		path := "/v1/jobs?limit=30"
		if len(args) == 2 {
			path += "&workflow=" + url.QueryEscape(args[1])
		}
		var out struct{ Jobs []workflow.Job }
		if code := apiExit(c.Do(ctx, "GET", path, nil, &out), stderr); code != 0 {
			return code
		}
		for _, j := range out.Jobs {
			test := ""
			if j.DryRun {
				test = " (test)"
			}
			why := j.Error
			if why == "" {
				why = j.Reason
			}
			if why != "" {
				why = ": " + why
			}
			fmt.Fprintf(stdout, "%s  %s  %-24s %s%s%s\n", j.ID[:8], j.CreatedAt.Local().Format("15:04:05"), j.WorkflowID, j.Status, test, why)
		}
		return 0
	case len(args) == 2 && args[0] == "show":
		var out api.JobDetail
		if code := apiExit(c.Do(ctx, "GET", "/v1/jobs/"+url.PathEscape(fullJobID(ctx, c, args[1])), nil, &out), stderr); code != 0 {
			return code
		}
		j := out.Job
		fmt.Fprintf(stdout, "%s (%s v%d): %s", j.ID, j.WorkflowID, j.Version, j.Status)
		if j.DryRun {
			fmt.Fprint(stdout, ", a test run: nothing reached Twitch or OBS")
		}
		fmt.Fprintln(stdout)
		if j.Error != "" || j.Reason != "" {
			fmt.Fprintln(stdout, "  "+j.Error+j.Reason)
		}
		for _, s := range out.Steps {
			name := s.Kind
			if s.Action != "" {
				name = s.Action
			}
			line := fmt.Sprintf("  %-7s %-34s %s", s.Status, s.Path, name)
			if s.Reason != "" {
				line += " (" + s.Reason + ")"
			}
			if s.Error != "" {
				line += ": " + s.Error
			}
			fmt.Fprintln(stdout, line)
			if s.Input != nil {
				b, _ := json.Marshal(s.Input)
				fmt.Fprintf(stdout, "          in:  %s\n", b)
			}
			if s.Output != nil {
				b, _ := json.Marshal(s.Output)
				fmt.Fprintf(stdout, "          out: %s\n", b)
			}
		}
		return 0
	case len(args) == 2 && args[0] == "cancel":
		if code := apiExit(c.Do(ctx, "POST", "/v1/jobs/"+url.PathEscape(fullJobID(ctx, c, args[1]))+"/cancel", nil, nil), stderr); code != 0 {
			return code
		}
		fmt.Fprintln(stdout, "Cancelling.")
		return 0
	}
	fmt.Fprintln(stderr, workflowsUsage)
	return 2
}

// fullJobID expands the 8-character prefix `runs` prints.
func fullJobID(ctx context.Context, c *client.Client, prefix string) string {
	if len(prefix) >= 36 {
		return prefix
	}
	var out struct{ Jobs []workflow.Job }
	if c.Do(ctx, "GET", "/v1/jobs?limit=200", nil, &out) == nil {
		for _, j := range out.Jobs {
			if strings.HasPrefix(j.ID, prefix) {
				return j.ID
			}
		}
	}
	return prefix
}
