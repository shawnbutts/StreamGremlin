package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/datadir"
)

func TestIconMatchesBrandKit(t *testing.T) {
	want, err := os.ReadFile("../../brand-kit/streamgremlin-icon-1024.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(icon, want) {
		t.Fatal("build/appicon.png differs from brand-kit/streamgremlin-icon-1024.png; copy it again")
	}
}

func TestExplainLockedFolder(t *testing.T) {
	msg := explain(&datadir.LockedError{Dir: "/d", PID: 1}, "/d")
	if !strings.Contains(msg, "already using your data folder") {
		t.Fatal(msg)
	}
	if !strings.Contains(explain(errors.New("boom"), "/d"), "boom") {
		t.Fatal("unknown errors must keep their detail")
	}
}

func TestUIIsEmbedded(t *testing.T) {
	if _, err := dist.ReadFile("frontend/dist/index.html"); err != nil {
		t.Skip("frontend not built; run `make desktop-ui` first")
	}
}
