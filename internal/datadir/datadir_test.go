package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultRefusesRealFolderInTests(t *testing.T) {
	t.Setenv(EnvVar, "")
	if _, err := Default(); err == nil {
		t.Fatal("Default must refuse to pick the real data folder in a test binary")
	}
}

func TestDefaultUsesEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvVar, dir)
	got, err := Default()
	if err != nil || got != dir {
		t.Fatalf("Default() = %q, %v; want %q", got, err, dir)
	}
}

func TestOSDefaults(t *testing.T) {
	home := func() (string, error) { return "/home/sam", nil }
	env := map[string]string{"LocalAppData": `C:\Users\sam\AppData\Local`}
	getenv := func(k string) string { return env[k] }
	cases := map[string]string{
		"darwin":  filepath.Join("/home/sam", "Library", "Application Support", "StreamGremlin"),
		"linux":   filepath.Join("/home/sam", ".local", "share", "streamgremlin"),
		"windows": filepath.Join(`C:\Users\sam\AppData\Local`, "StreamGremlin"),
	}
	for goos, want := range cases {
		got, err := osDefault(goos, getenv, home)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", goos, got, err, want)
		}
	}
	env["XDG_DATA_HOME"] = "/xdg"
	if got, _ := osDefault("linux", getenv, home); got != filepath.Join("/xdg", "streamgremlin") {
		t.Errorf("XDG_DATA_HOME ignored: %q", got)
	}
}

func TestEnsureIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := Ensure(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v, want 0700", fi.Mode().Perm())
	}
}

func TestLockIsExclusiveAndReleasable(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire: got %v, want ErrLocked", err)
	}
	var le *LockedError
	if errors.As(err, &le) && le.PID != os.Getpid() {
		t.Errorf("lock holder pid %d, want %d", le.PID, os.Getpid())
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	_ = l2.Release()
}
