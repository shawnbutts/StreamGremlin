package actions

import (
	"regexp"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/variables"
	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// Every action the app offers must be complete enough for the plain-words
// editor: a label, a group, a description, and parameters it can show.
func TestEveryActionCanBeShownInTheEditor(t *testing.T) {
	reg := workflow.NewRegistry()
	Register(reg, Services{})
	e := workflow.New(workflow.Options{Actions: reg, Variables: variables.New(nil)})
	p := e.Palette()
	if len(p.Actions) < 15 || len(p.Triggers) == 0 {
		t.Fatalf("%d actions, %d triggers", len(p.Actions), len(p.Triggers))
	}
	groups := map[string]bool{"General": true, "Twitch": true, "OBS": true, "Variables": true, "Workflows": true, "Timers": true, "Quotes": true, "Sounds": true, "Overlays": true, "Giveaways": true, "Wheels": true, "Points": true, "Lua": true}
	for _, a := range p.Actions {
		if a.Label == "" || a.Description == "" || !groups[a.Group] {
			t.Errorf("%s: label %q, group %q, description %q", a.Name, a.Label, a.Group, a.Description)
		}
		for _, prm := range a.Params {
			switch prm.Editor {
			case "", "message", "scene", "source", "workflow", "variable", "value", "timer", "sound", "overlay", "reward", "code":
			default:
				t.Errorf("%s.%s: editor %q", a.Name, prm.Name, prm.Editor)
			}
			switch prm.Type {
			case "text", "number", "bool", "any":
			case "choice":
				if len(prm.Choices) == 0 {
					t.Errorf("%s.%s: a choice with no choices", a.Name, prm.Name)
				}
				// The editor offers "leave it unset" as an option, so it must
				// say what that does.
				if !prm.Required && prm.DefaultHint == "" {
					t.Errorf("%s.%s: an optional choice needs a DefaultHint", a.Name, prm.Name)
				}
			default:
				t.Errorf("%s.%s: type %q", a.Name, prm.Name, prm.Type)
			}
			if prm.Label == "" || (prm.Literal && prm.Type != "text") {
				t.Errorf("%s.%s: %+v", a.Name, prm.Name, prm)
			}
			if prm.Pattern != "" {
				if _, err := regexp.Compile(prm.Pattern); err != nil || prm.PatternHint == "" {
					t.Errorf("%s.%s: pattern %q: %v", a.Name, prm.Name, prm.Pattern, err)
				}
			}
		}
	}
}
