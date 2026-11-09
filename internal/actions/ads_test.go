package actions

import (
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

func TestOnlyTheStreamersChatRunsAds(t *testing.T) {
	chat := func(typ string, broadcaster any) workflow.Call {
		return workflow.Call{Event: map[string]any{"type": typ, "payload": map[string]any{"chatter": map[string]any{"is_broadcaster": broadcaster}}}}
	}
	for _, c := range []struct {
		call    workflow.Call
		refused bool
	}{
		{chat("chat.command.received", false), true},
		{chat("chat.message.received", nil), true},
		{chat("chat.command.received", true), false},
		{workflow.Call{Event: map[string]any{"type": "timers.timer.fired"}}, false},
		{workflow.Call{}, false},
	} {
		if got := chatNotFromStreamer(c.call); got != c.refused {
			t.Errorf("%v: refused %v, want %v", c.call.Event, got, c.refused)
		}
	}
}
