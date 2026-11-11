package api

import (
	"errors"
	"net/http"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/actions"
	"code.streamgremlin.com/streamgremlin/internal/lua"
)

// LuaTry is the body of POST /v1/lua/try.
type LuaTry struct {
	Script string         `json:"script"`
	Args   map[string]any `json:"args,omitempty"`
	// Event replaces the made-up test event the script sees as sg.event.
	Event     map[string]any `json:"event,omitempty"`
	TimeLimit any            `json:"time_limit,omitempty"`
}

// LuaTried is what POST /v1/lua/try answers: the step's output when it ran
// (result, log, log_dropped), or what went wrong.
type LuaTried struct {
	OK         bool     `json:"ok"`
	Result     any      `json:"result"`
	Log        []string `json:"log"`
	LogDropped int      `json:"log_dropped,omitempty"`
	Error      string   `json:"error,omitempty"`
	// Kind is script, timeout, memory, output or crashed.
	Kind   string `json:"kind,omitempty"`
	TookMS int64  `json:"took_ms"`
}

// TestLuaEvent is the event a tried script sees unless one is given: a
// test chat message from a made-up viewer.
func TestLuaEvent(now time.Time) map[string]any {
	return map[string]any{
		"id": "test", "seq": 0, "type": "chat.message.received", "source": "test", "source_event_id": "",
		"channel_id": "", "delivery_quality": "synthetic", "occurred_at": now.UTC().Format(time.RFC3339Nano),
		"payload": map[string]any{"text": "!roll 20", "chatter": map[string]any{"role": "viewer"}},
		"actor":   map[string]any{"platform": "twitch", "id": "0", "login": "testviewer", "display_name": "TestViewer"},
	}
}

func luaRoutes(d Deps) []Route {
	return []Route{
		{
			Method: "POST", Path: "/v1/lua/try",
			Summary: "Run a Lua script once, as a \"Run a Lua script\" step would (docs/adr/0037-lua-scripts.md), with values, " +
				"a made-up test event (or the one given), and a time limit. A script that fails is still 200, with ok false, the error and its kind.",
			Permission: PermWorkflowsWrite, // it runs code
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var body LuaTry
				if !decodeJSON(w, r, &body) {
					return
				}
				if body.Script == "" {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "script is required")
					return
				}
				if len(body.Script) > lua.MaxScriptBytes {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, "the script is over 64 KiB")
					return
				}
				limit, err := actions.LuaTimeLimit(body.TimeLimit)
				if err != nil {
					writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
					return
				}
				if d.Lua == nil {
					writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "Lua scripts aren't available")
					return
				}
				event := body.Event
				if event == nil {
					event = TestLuaEvent(time.Now())
				}
				res, err := d.Lua.Run(r.Context(), lua.Job{Script: body.Script, Args: body.Args, Event: event, TimeLimit: limit, Who: []any{"tried", true}})
				var le *lua.Error
				switch {
				case err == nil:
					out := actions.LuaOutput(res)
					writeJSON(w, http.StatusOK, LuaTried{OK: true, Result: out["result"], Log: out["log"].([]string), LogDropped: res.LogDropped, TookMS: res.Took.Milliseconds()})
				case errors.As(err, &le):
					log := le.Log
					if log == nil {
						log = []string{}
					}
					writeJSON(w, http.StatusOK, LuaTried{Error: le.Message, Kind: le.Kind, Log: log, LogDropped: le.LogDropped, TookMS: res.Took.Milliseconds()})
				default:
					internalError(w, r, d.Logger, err)
				}
			},
		},
	}
}
