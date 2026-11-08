//go:build acceptance

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/secrets"
	"code.streamgremlin.com/streamgremlin/internal/storage"
)

const leakCanary = "oauth:acceptance-canary-6b1f0e"

// saveSecrets writes secrets into a stopped engine's data folder, as a later
// feature (Twitch sign-in, OBS) would from inside the engine.
func saveSecrets(t *testing.T, dir string, keys ...string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, datadir.DatabasePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := secrets.Open(ctx, db, secrets.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind() != secrets.KindFile {
		t.Fatalf("store %s; acceptance tests use the file", s.Kind())
	}
	for _, k := range keys {
		if err := s.Set(ctx, k, secrets.NewValue(leakCanary)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecretsThroughTheCLI(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir).kill()
	saveSecrets(t, dir, "obs/password", "twitch/42/oauth")
	e := startEngine(t, dir, "-debug")

	var outputs []string
	run := func(wantCode int, args ...string) string {
		t.Helper()
		out, errOut, code := cli(t, dir, args...)
		outputs = append(outputs, out, errOut)
		if code != wantCode {
			t.Fatalf("%v: code %d, want %d\n%s%s", args, code, wantCode, out, errOut)
		}
		return out
	}

	out := run(0, "secrets")
	if !strings.Contains(out, "an encrypted file in your data folder") || !strings.Contains(out, "obs/password") || !strings.Contains(out, "twitch/42/oauth") {
		t.Fatalf("list:\n%s", out)
	}
	if out := run(0, "secrets", "-check"); strings.Count(out, "readable") != 2 {
		t.Fatalf("check:\n%s", out)
	}
	var list struct {
		Store   struct{ Kind string }
		Secrets []struct{ Key, State string }
	}
	if err := json.Unmarshal([]byte(run(0, "secrets", "-check", "-json")), &list); err != nil || list.Store.Kind != "file" || len(list.Secrets) != 2 {
		t.Fatalf("json: %+v %v", list, err)
	}
	run(0, "secrets", "forget", "twitch/42/oauth")
	run(0, "secrets", "forget", "twitch/42/oauth") // a retried forget is harmless
	run(2, "secrets", "forget", "../etc/passwd")
	if out := run(0, "secrets"); strings.Contains(out, "twitch/42/oauth") || !strings.Contains(out, "obs/password") {
		t.Fatalf("after forget:\n%s", out)
	}

	// Forgetting survives a crash.
	e.kill()
	e = startEngine(t, dir)
	if out := run(0, "secrets"); strings.Contains(out, "twitch/42/oauth") {
		t.Fatalf("forgotten secret came back:\n%s", out)
	}
	code, body := call(t, e, "GET", "/v1/secrets?check=true", e.token, "", nil)
	outputs = append(outputs, body, e.stderr.String())
	if code != 200 {
		t.Fatalf("api: %d %s", code, body)
	}

	// The value is nowhere but the encrypted file.
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte("acceptance-canary")) {
			t.Errorf("the secret value is readable in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outputs {
		if strings.Contains(o, "acceptance-canary") {
			t.Fatalf("the secret value was printed:\n%s", o)
		}
	}
	if runtime.GOOS != "windows" {
		for _, n := range []string{secrets.FileName, secrets.KeyFileName} {
			fi, err := os.Stat(filepath.Join(dir, n))
			if err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v %v", n, fi.Mode().Perm(), err)
			}
		}
	}
}

func TestSecretStoreStaysFixed(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir).kill()
	cmd := exec.Command(bin, "-data-dir", dir, "engine", "-addr", "127.0.0.1:0", "-secret-store", "keyring")
	cmd.Env = cleanEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("the engine switched secret stores")
	}
	if !strings.Contains(string(out), "already uses another secret store") {
		t.Fatalf("unhelpful message: %s", out)
	}
	startEngine(t, dir) // and the folder still works with the store it has
}

func TestDamagedSecretsFileDoesNotStopTheEngine(t *testing.T) {
	dir := t.TempDir()
	startEngine(t, dir).kill()
	saveSecrets(t, dir, "obs/password")
	if err := os.WriteFile(filepath.Join(dir, secrets.KeyFileName), bytes.Repeat([]byte{1}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	startEngine(t, dir)
	out, _, code := cli(t, dir, "secrets", "-check")
	if code != 1 || !strings.Contains(out, "can't be read") || !strings.Contains(out, "obs/password") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if _, _, code := cli(t, dir, "status"); code != 0 {
		t.Fatal("engine not running")
	}
}
