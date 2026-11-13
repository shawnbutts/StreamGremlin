//go:build acceptance

// Package acceptance drives the real streamgremlin binary: start, stop,
// crash, restart, and the API's security checks. Run with `make test-acceptance`.
package acceptance

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var bin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "sg-acceptance-")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(tmp, "streamgremlin")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "code.streamgremlin.com/streamgremlin/cmd/streamgremlin")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		panic("build streamgremlin: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// engine is a running `streamgremlin engine` process.
type engine struct {
	cmd    *exec.Cmd
	dir    string
	url    string
	token  string
	stderr *bytes.Buffer
	exited chan error
}

func startEngine(t *testing.T, dir string, extra ...string) *engine {
	t.Helper()
	_ = os.Remove(filepath.Join(dir, "api-addr")) // wait for this run's file
	e := &engine{dir: dir, stderr: new(bytes.Buffer), exited: make(chan error, 1)}
	args := append([]string{"-data-dir", dir, "engine", "-addr", "127.0.0.1:0", "-overlay-addr", "127.0.0.1:0"}, extra...)
	e.cmd = exec.Command(bin, args...)
	e.cmd.Env = cleanEnv()
	e.cmd.Stderr = e.stderr
	if err := e.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { e.exited <- e.cmd.Wait() }()
	t.Cleanup(func() { e.kill() })

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-e.exited:
			t.Fatalf("engine exited early: %v\n%s", err, e.stderr)
		default:
		}
		if b, err := os.ReadFile(filepath.Join(dir, "api-addr")); err == nil {
			e.url = strings.TrimSpace(string(b))
			tok, err := os.ReadFile(filepath.Join(dir, "api-token"))
			if err != nil {
				t.Fatal(err)
			}
			e.token = strings.TrimSpace(string(tok))
			return e
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("engine didn't start\n%s", e.stderr)
	return nil
}

func (e *engine) kill() {
	if e.cmd.ProcessState == nil {
		_ = e.cmd.Process.Kill()
		select {
		case <-e.exited:
		case <-time.After(5 * time.Second):
		}
	}
}

// cleanEnv keeps the test away from the user's real data folder.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "STREAMGREMLIN_") {
			env = append(env, kv)
		}
	}
	// Never the real keyring (AGENTS.md); a test asks for another store by flag.
	// Nor the real Twitch or Discord: port 9 (discard) answers nothing; tests
	// pass -twitch-auth-url and -discord-api-url for their own stand-ins.
	return append(env, "STREAMGREMLIN_DATA_DIR=/nonexistent/do-not-use", "STREAMGREMLIN_SECRET_STORE=file",
		"STREAMGREMLIN_TWITCH_AUTH_URL=http://127.0.0.1:9", "STREAMGREMLIN_DISCORD_API_URL=http://127.0.0.1:9")
}

func cli(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"-data-dir", dir}, args...)...)
	cmd.Env = cleanEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

func call(t *testing.T, e *engine, method, path, token, body string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, e.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestStatusThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	out, errOut, code := cli(t, dir, "status")
	if code != 0 || !strings.Contains(out, "StreamGremlin is running.") || !strings.Contains(out, "Schema:       ") {
		t.Fatalf("code %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	// A fresh install: setup to do, ticked from real state, and nothing wrong.
	if !strings.Contains(out, "Get set up") || !strings.Contains(out, "[ ] Sign in with your Twitch account") ||
		!strings.Contains(out, "[ ] Connect OBS (optional)") || strings.Contains(out, "Needs you") {
		t.Fatalf("status:\n%s", out)
	}
	out, _, code = cli(t, dir, "status", "-json")
	var st map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st["status"] != "running" {
		t.Fatalf("json status: %d %s", code, out)
	}
}

func TestStatusWhenNotRunning(t *testing.T) {
	_, errOut, code := cli(t, t.TempDir(), "status")
	if code != 1 || !strings.Contains(errOut, "isn't running") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func TestOnlyOneEnginePerDataFolder(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir)
	cmd := exec.Command(bin, "-data-dir", dir, "engine", "-addr", "127.0.0.1:0")
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("second engine started on the same data folder")
	}
	if !strings.Contains(string(out), "another StreamGremlin process is using this data folder") {
		t.Fatalf("unhelpful message: %s", out)
	}
}

func TestAPISecurityChecks(t *testing.T) {
	e := startEngine(t, t.TempDir())
	if code, _ := call(t, e, "GET", "/v1/health", "", "", nil); code != 200 {
		t.Errorf("health: %d", code)
	}
	if code, _ := call(t, e, "GET", "/v1/status", "", "", nil); code != 401 {
		t.Errorf("no token: %d", code)
	}
	if code, _ := call(t, e, "GET", "/v1/status", "wrong", "", nil); code != 401 {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := call(t, e, "GET", "/v1/status", e.token, "", map[string]string{"Host": "evil.example"}); code != 403 {
		t.Errorf("rebinding Host: %d", code)
	}
	if code, _ := call(t, e, "GET", "/v1/status", e.token, "", map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Errorf("foreign Origin: %d", code)
	}
	if code, _ := call(t, e, "GET", "/v1/status", e.token, "", nil); code != 200 {
		t.Errorf("good request: %d", code)
	}
}

func TestSettingsSurviveACrash(t *testing.T) {
	dir := t.TempDir()
	e := startEngine(t, dir)
	for i := 0; i < 2; i++ { // a retried write is harmless
		if code, body := call(t, e, "PUT", "/v1/settings/ui", e.token, `{"theme":"midnight"}`, nil); code != 200 {
			t.Fatalf("put: %d %s", code, body)
		}
	}
	e.kill() // like a crash: no clean shutdown, api-addr left behind

	if _, errOut, code := cli(t, dir, "status"); code != 1 || !strings.Contains(errOut, "isn't running") {
		t.Fatalf("status after crash: %d %q", code, errOut)
	}
	e2 := startEngine(t, dir)
	code, body := call(t, e2, "GET", "/v1/settings/ui", e2.token, "", nil)
	if code != 200 || !strings.Contains(body, `"theme":"midnight"`) {
		t.Fatalf("after restart: %d %s", code, body)
	}
	if e2.token != e.token {
		t.Error("API token changed across a restart")
	}
}

func TestCleanStopRemovesAddressFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("can't send an interrupt to another process on Windows")
	}
	dir := t.TempDir()
	e := startEngine(t, dir)
	if err := e.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-e.exited:
		if err != nil {
			t.Fatalf("exit: %v\n%s", err, e.stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("engine didn't stop")
	}
	if _, err := os.Stat(filepath.Join(dir, "api-addr")); !os.IsNotExist(err) {
		t.Fatal("api-addr left after a clean stop")
	}
}

func TestTokenNeverLogged(t *testing.T) {
	dir := t.TempDir()
	e := startEngine(t, dir, "-debug")
	call(t, e, "GET", "/v1/status", e.token, "", nil)
	call(t, e, "GET", "/v1/status", "wrong-"+e.token, "", nil)
	logFile, err := os.ReadFile(filepath.Join(dir, "logs", "engine.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logFile), "api request") {
		t.Fatalf("debug log has no request lines:\n%s", logFile)
	}
	for name, text := range map[string]string{"engine.log": string(logFile), "stderr": e.stderr.String()} {
		if strings.Contains(text, e.token) {
			t.Errorf("the API token appears in %s", name)
		}
	}
}

func TestDataFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "data")
	startEngine(t, dir)
	want := map[string]os.FileMode{
		"":                 0o700,
		"api-token":        0o600,
		"api-addr":         0o600,
		"streamgremlin.db": 0o600,
		"logs/engine.log":  0o600,
	}
	for name, mode := range want {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if fi.Mode().Perm() != mode {
			t.Errorf("%s: mode %v, want %v", name, fi.Mode().Perm(), mode)
		}
	}
}
