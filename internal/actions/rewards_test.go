package actions

import (
	"context"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/workflow"
)

// The redemption step acts only on the redemption that started the run.
func TestRedemptionStepNeedsARedemption(t *testing.T) {
	reg := workflow.NewRegistry()
	Register(reg, Services{})
	a, _ := reg.Get("rewards.redemption.set")
	if !a.External || a.Preview {
		t.Fatal("it must reach Twitch only for real events")
	}
	for _, ev := range []map[string]any{nil, {"type": "chat.command.received", "payload": map[string]any{}},
		{"type": "rewards.channel_points.redeemed", "payload": map[string]any{"reward": map[string]any{}}}} {
		if _, err := a.Run(context.Background(), workflow.Call{Event: ev, Args: map[string]any{"action": "refund"}}); err == nil ||
			!strings.Contains(err.Error(), "only when a viewer redeems channel points") {
			t.Errorf("%v: %v", ev, err)
		}
	}
	reward, id, ok := redemptionOf(workflow.Call{Event: map[string]any{"type": "rewards.channel_points.redeemed",
		"payload": map[string]any{"redemption_id": "d1", "reward": map[string]any{"id": "r1"}}}})
	if !ok || reward != "r1" || id != "d1" {
		t.Fatal(reward, id, ok)
	}
}
