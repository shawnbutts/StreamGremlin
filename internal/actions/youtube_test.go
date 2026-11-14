package actions

import (
	"context"
	"errors"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/workflow"
	"code.streamgremlin.com/streamgremlin/internal/youtube"
)

// The YouTube steps (D-102) are External, say their quota cost, and fail in
// words without YouTube; a poll's choices are the ones filled in.
func TestYouTubeSteps(t *testing.T) {
	reg := workflow.NewRegistry()
	Register(reg, Services{})
	for _, name := range []string{"youtube.stream.set", "youtube.poll.start", "youtube.poll.end"} {
		a, ok := reg.Get(name)
		if !ok || !a.External || a.Group != "YouTube" {
			t.Fatalf("%s: %+v", name, a)
		}
		if _, err := a.Run(context.Background(), workflow.Call{Args: map[string]any{"question": "Q", "choice_1": "a", "choice_2": "b"}}); !errors.Is(err, errNoYouTube) {
			t.Fatalf("%s without YouTube: %v", name, err)
		}
	}
	a, _ := reg.Get("youtube.poll.start")
	if n := len(a.Params); n != 1+youtube.MaxPollOptions {
		t.Fatalf("%d params", n)
	}
	if got := ytPlain(errors.New("youtube: invalid: a question is 1 to 100 characters")); got.Error() != "a question is 1 to 100 characters" {
		t.Fatal(got)
	}
	if ytPlain(nil) != nil {
		t.Fatal("nil")
	}
}
