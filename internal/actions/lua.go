package actions

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/lua"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// LuaOutput is what a Lua step gives the steps after it, and what trying a
// script answers (docs/adr/0037-lua-scripts.md).
func LuaOutput(res lua.Result) map[string]any {
	out := map[string]any{"result": res.Value, "log": orNoLines(res.Log)}
	if res.LogDropped > 0 {
		out["log_dropped"] = res.LogDropped
	}
	return out
}

func orNoLines(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// LuaTimeLimit reads a time limit in seconds (a number, or text a number):
// absent is lua.DefaultTimeLimit.
func LuaTimeLimit(v any) (time.Duration, error) {
	var secs float64
	switch x := v.(type) {
	case nil:
		return lua.DefaultTimeLimit, nil
	case int64:
		secs = float64(x)
	case int:
		secs = float64(x)
	case float64:
		secs = x
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, errors.New("the time limit is a number of seconds")
		}
		secs = f
	default:
		return 0, errors.New("the time limit is a number of seconds")
	}
	d := time.Duration(secs * float64(time.Second))
	if err := lua.CheckTimeLimit(d); err != nil {
		return 0, err
	}
	return d, nil
}

// registerLua adds "Run a Lua script". A script changes nothing outside
// itself, so it runs for test events too, and a retried run is harmless.
func registerLua(reg *workflow.Registry, runner *lua.Runner) {
	reg.Add(workflow.Action{
		Name: "lua.run", Label: "Run a Lua script", Group: "Lua", More: true,
		Description: "Runs Lua you write here, in a separate process with a time limit. It reads sg.args and sg.event, " +
			"writes lines with sg.log or print, and what it returns is the step's result. It can't reach chat, OBS, files or the internet.",
		Params: []workflow.Param{
			{Name: "script", Label: "Script", Type: "text", Required: true, Literal: true, Editor: "code", MaxLength: lua.MaxScriptBytes},
			{Name: "args", Label: "Values to pass", Type: "any"},
			{Name: "time_limit", Label: "Time limit (seconds)", Type: "number", Default: int64(1), DefaultHint: "1 second; at most 30"},
		},
		Run: func(ctx context.Context, c workflow.Call) (any, error) {
			if runner == nil {
				return nil, errors.New("this build can't run Lua scripts")
			}
			script, _ := c.String("script", "")
			limit, err := LuaTimeLimit(c.Args["time_limit"])
			if err != nil {
				return nil, err
			}
			var args map[string]any
			switch a := c.Args["args"].(type) {
			case nil:
			case map[string]any:
				args = a
			default:
				return nil, errors.New("values to pass are names and values")
			}
			res, err := runner.Run(ctx, lua.Job{Script: script, Args: args, Event: c.Event, TimeLimit: limit,
				Who: []any{"workflow", c.WorkflowID, "run", c.JobID, "step", c.IdempotencyKey}})
			var le *lua.Error
			if errors.As(err, &le) {
				if len(le.Log) > 0 {
					return nil, fmt.Errorf("%s (its last line: %q)", le.Message, le.Log[len(le.Log)-1])
				}
				return nil, le
			}
			if err != nil {
				return nil, err
			}
			return LuaOutput(res), nil
		},
	})
}
