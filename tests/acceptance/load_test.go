//go:build acceptance && load

package acceptance

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Load budgets (M9 slice 1, D-110): five or more times what an Apple
// silicon laptop measures (45 runs a second, 44 s, API p99 72 ms, 64 MB),
// so a slower machine passes and a failure means something got slower.
// `make test-load` prints the measurements.
const (
	loadViewers      = 200
	loadEach         = 10
	budgetPerSecond  = 8.0               // runs finished per second, at least
	budgetAllDone    = 250 * time.Second // from the first message to the last run
	budgetAPIP99     = time.Second       // API answers while the surge runs
	budgetMemoryKB   = 400 * 1024        // the engine's resident memory after
	loadReportEnvVar = "STREAMGREMLIN_LOAD_REPORT"
)

// TestLoadChatSurge: 200 viewers type !hype 10 times each, about one
// message in ten is delivered twice (as Twitch may), and four API clients
// read the whole time. The workflow gives a point and counts, globally and
// per viewer. Every message runs once: the counts are exact, the ledger
// audits clean, and the budgets hold.
func TestLoadChatSurge(t *testing.T) {
	tw, twURL := startMockTwitch(t)
	dir := t.TempDir()
	e := startEngine(t, dir, "-twitch-auth-url", twURL)
	cli(t, dir, "twitch", "client-id", "abcdefghij0123456789")
	if out, code := signInThroughCLI(t, dir, tw, "Streamer"); code != 0 {
		t.Fatal(out)
	}
	eventually(t, "chat", func() string {
		if tw.Subscribed("streamer") == 0 {
			return "not reading chat"
		}
		return ""
	})
	wf := writeFile(t, t.TempDir(), "hype.json", `{"schema":"streamgremlin.workflow/v1","id":"chat.hype","name":"Hype",
		"triggers":[{"type":"chat.command.received","where":{"command":"hype"}}],
		"queue":{"policy":"parallel","concurrency":8,"max_pending":2000},
		"steps":[{"action":"points.give","with":{"amount":1,"reason":"hype"}},
			{"action":"vars.add","with":{"scope":"global","name":"hype","amount":1}},
			{"action":"vars.add","with":{"scope":"viewer","name":"hype","amount":1}}]}`)
	if out, errOut, code := cli(t, dir, "workflows", "save", wf); code != 0 {
		t.Fatal(out, errOut)
	}
	cli(t, dir, "workflows", "on", "chat.hype")

	// Readers: status, viewers and variables, as the app polls.
	stop := make(chan struct{})
	var mu sync.Mutex
	var latencies []time.Duration
	apiErrors := 0
	var readers sync.WaitGroup
	for i := range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			paths := []string{"/v1/status", "/v1/viewers?limit=50", "/v1/variables?scope=global", "/v1/points"}
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				req, _ := http.NewRequest("GET", e.url+paths[(i+n)%len(paths)], nil)
				req.Header.Set("Authorization", "Bearer "+e.token)
				start := time.Now()
				resp, err := http.DefaultClient.Do(req)
				took := time.Since(start)
				mu.Lock()
				latencies = append(latencies, took)
				if err != nil || resp.StatusCode != 200 {
					apiErrors++
				}
				mu.Unlock()
				if err == nil {
					_ = resp.Body.Close()
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
	}

	// The surge: each viewer in its own goroutine; every tenth message is
	// delivered again a moment later.
	start := time.Now()
	var said sync.WaitGroup
	for v := range loadViewers {
		said.Add(1)
		go func() {
			defer said.Done()
			for n := range loadEach {
				id := tw.Say("streamer", fmt.Sprintf("viewer%03d", v), "!hype")
				if (v*loadEach+n)%10 == 0 {
					tw.SayAgain(id)
				}
			}
		}()
	}
	said.Wait()
	sent := time.Since(start)

	want := loadViewers * loadEach
	deadline := time.Now().Add(budgetAllDone + 30*time.Second)
	var got int
	for time.Now().Before(deadline) {
		if got = globalHype(t, e); got >= want {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	allDone := time.Since(start)
	close(stop)
	readers.Wait()

	// Exact counts: nothing lost, nothing run twice.
	time.Sleep(500 * time.Millisecond) // anything run twice would land now
	if got = globalHype(t, e); got != want {
		_, jobs := call(t, e, "GET", "/v1/jobs?workflow=chat.hype&limit=200", e.token, "", nil)
		counts := map[string]int{}
		var body struct {
			Jobs []struct {
				Status string `json:"status"`
				Reason string `json:"reason"`
				Error  string `json:"error"`
			} `json:"jobs"`
		}
		_ = json.Unmarshal([]byte(jobs), &body)
		for _, j := range body.Jobs {
			counts[j.Status+" "+j.Reason+" "+j.Error]++
		}
		var warn []string
		for _, l := range strings.Split(e.stderr.String(), "\n") {
			if (strings.Contains(l, "level=WARN") || strings.Contains(l, "level=ERROR")) && len(warn) < 30 {
				warn = append(warn, l)
			}
		}
		log := strings.Join(warn, "\n")
		t.Fatalf("the global count is %d, want %d; the newest 200 runs: %v\n%s", got, want, counts, log)
	}
	per := viewerHype(t, e)
	if len(per) != loadViewers {
		t.Errorf("%d viewers counted, want %d", len(per), loadViewers)
	}
	for who, n := range per {
		if n != loadEach {
			t.Errorf("%s counted %d, want %d", who, n, loadEach)
		}
	}
	if n := strings.Count(e.stderr.String(), "event refused"); n > 0 {
		t.Errorf("%d redeliveries were refused rather than recognised", n)
	}
	if code, out := call(t, e, "GET", "/v1/points/audit", e.token, "", nil); code != 200 || !strings.Contains(out, `"ok":true`) {
		t.Errorf("ledger audit: %d %s", code, out)
	}

	slices.Sort(latencies)
	p99 := latencies[int(math.Ceil(float64(len(latencies))*0.99))-1]
	perSecond := float64(want) / allDone.Seconds()
	rss := residentKB(t, e.cmd.Process.Pid)
	report := map[string]any{
		"messages": want, "redelivered": want / 10, "sent_ms": sent.Milliseconds(), "all_done_ms": allDone.Milliseconds(),
		"runs_per_second": math.Round(perSecond*10) / 10, "api_requests": len(latencies), "api_p99_ms": p99.Milliseconds(),
		"api_errors": apiErrors, "engine_rss_mb": rss / 1024,
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("load: %s", b)
	if p := os.Getenv(loadReportEnvVar); p != "" {
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Error(err)
		}
	}
	switch {
	case perSecond < budgetPerSecond:
		t.Errorf("%.1f runs a second, budget at least %.0f", perSecond, budgetPerSecond)
	case allDone > budgetAllDone:
		t.Errorf("every run done after %v, budget %v", allDone, budgetAllDone)
	}
	if p99 > budgetAPIP99 {
		t.Errorf("API p99 %v, budget %v", p99, budgetAPIP99)
	}
	if apiErrors > 0 {
		t.Errorf("%d API errors during the surge", apiErrors)
	}
	if rss > budgetMemoryKB {
		t.Errorf("engine memory %d MB, budget %d MB", rss/1024, budgetMemoryKB/1024)
	}
}

type loadVar struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func loadVars(t *testing.T, e *engine, scope string) []loadVar {
	t.Helper()
	code, out := call(t, e, "GET", "/v1/variables?limit=1000&scope="+scope, e.token, "", nil)
	if code != 200 {
		t.Fatalf("variables: %d %s", code, out)
	}
	var body struct {
		Variables []loadVar `json:"variables"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	return body.Variables
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return -1
}

func globalHype(t *testing.T, e *engine) int {
	for _, v := range loadVars(t, e, "global") {
		if v.Name == "hype" {
			return asInt(v.Value)
		}
	}
	return 0
}

func viewerHype(t *testing.T, e *engine) map[string]int {
	out := map[string]int{}
	for _, v := range loadVars(t, e, "viewer") {
		if v.Name == "hype" {
			out[v.Owner] = asInt(v.Value)
		}
	}
	return out
}

// residentKB is a process's resident memory, from ps.
func residentKB(t *testing.T, pid int) int {
	t.Helper()
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	kb, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("ps said %q", out)
	}
	return kb
}
