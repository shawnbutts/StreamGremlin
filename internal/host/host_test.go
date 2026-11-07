package host

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/datadir"
)

func TestStartServeAndClose(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	h, err := Start(ctx, Options{DataDir: dir, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(datadir.AddrPath(dir))
	if err != nil || strings.TrimSpace(string(b)) != h.URL() {
		t.Fatalf("api-addr %q, %v; want %s", b, err, h.URL())
	}
	resp, err := http.Get(h.URL() + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health %d", resp.StatusCode)
	}

	if _, err := Start(ctx, Options{DataDir: dir, Addr: "127.0.0.1:0"}); !errors.Is(err, datadir.ErrLocked) {
		t.Fatalf("second Start: %v, want ErrLocked", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.Close(cctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(datadir.AddrPath(dir)); !os.IsNotExist(err) {
		t.Fatal("api-addr left behind after Close")
	}
	h2, err := Start(ctx, Options{DataDir: dir, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	_ = h2.Close(cctx)
}

func TestRefusesNonLoopback(t *testing.T) {
	_, err := Start(context.Background(), Options{DataDir: t.TempDir(), Addr: "0.0.0.0:0"})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("got %v", err)
	}
}

func TestFallsBackWhenDefaultPortTaken(t *testing.T) {
	// Hold the default port, then start with no address.
	blocker, err := listen("")
	if err != nil {
		t.Skipf("can't test fallback: %v", err)
	}
	defer blocker.Close()
	h, err := Start(context.Background(), Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close(context.Background()) }()
	if h.URL() == "http://"+blocker.Addr().String() {
		t.Fatal("did not fall back")
	}
}
