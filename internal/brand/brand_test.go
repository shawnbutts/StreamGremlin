package brand

import "testing"

func TestDerivedNames(t *testing.T) {
	cases := map[string]string{
		Env("DATA_DIR"):    "STREAMGREMLIN_DATA_DIR",
		Format("workflow"): "streamgremlin.workflow/v1",
		APIVersionHeader:   "StreamGremlin-API-Version",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
