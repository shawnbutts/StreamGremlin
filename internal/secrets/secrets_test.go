package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"code.streamgremlin.com/streamgremlin/internal/storage"
)

const secretText = "oauth:very-secret-token-0123456789"

// fakeKeyring stands in for the OS keyring; tests never touch the real one.
type fakeKeyring struct {
	mu    sync.Mutex
	items map[string]string
	err   error         // returned by every call when set
	delay time.Duration // makes every call slow
	calls int
}

func newFake() *fakeKeyring { return &fakeKeyring{items: map[string]string{}} }

func (f *fakeKeyring) do() error {
	f.mu.Lock()
	f.calls++
	d, err := f.delay, f.err
	f.mu.Unlock()
	time.Sleep(d)
	return err
}

func (f *fakeKeyring) Get(s, u string) (string, error) {
	if err := f.do(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.items[s+"|"+u]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Set(s, u, p string) error {
	if err := f.do(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[s+"|"+u] = p
	return nil
}

func (f *fakeKeyring) Delete(s, u string) error {
	if err := f.do(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[s+"|"+u]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.items, s+"|"+u)
	return nil
}

func newDB(t *testing.T, dir string) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return db
}

func boolp(b bool) *bool { return &b }

// stores returns a fresh store of each kind.
func stores(t *testing.T) map[Kind]*Store {
	t.Helper()
	out := map[Kind]*Store{}
	for _, k := range []Kind{KindKeyring, KindFile} {
		dir := t.TempDir()
		s, err := Open(context.Background(), newDB(t, dir), Options{DataDir: dir, Want: k, provider: newFake()})
		if err != nil {
			t.Fatal(err)
		}
		out[k] = s
	}
	return out
}

func TestValueNeverPrints(t *testing.T) {
	v := NewValue(secretText)
	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("x", "v", v, "group", slog.GroupValue(slog.Any("v", v)))
	slog.New(slog.NewTextHandler(&logBuf, nil)).Info("x", "v", v)
	j, _ := json.Marshal(map[string]any{"v": v, "p": &v})
	txt, _ := v.MarshalText()
	outputs := []string{
		fmt.Sprint(v), fmt.Sprintf("%v %+v %#v %s %q %x %X %d", v, v, v, v, v, v, v, v),
		fmt.Sprintf("%v", &v), fmt.Sprintf("%v", struct{ V Value }{v}),
		string(j), string(txt), logBuf.String(), errors.New(fmt.Sprint(v)).Error(),
	}
	for _, out := range outputs {
		if strings.Contains(out, "very-secret") || strings.Contains(out, fmt.Sprintf("%x", secretText)[:12]) {
			t.Errorf("secret leaked: %s", out)
		}
	}
	if v.Reveal() != secretText {
		t.Fatal("Reveal lost the value")
	}
	var back Value
	if json.Unmarshal([]byte(`"x"`), &back) == nil {
		t.Fatal("a Value decoded from JSON")
	}
}

func TestValidKey(t *testing.T) {
	for _, k := range []string{"obs/password", "twitch/123/oauth", "a", "x.y_z-1/b"} {
		if !ValidKey(k) {
			t.Errorf("%q should be valid", k)
		}
	}
	for _, k := range []string{"", "/a", "a/", "a//b", "A", "a b", "../x", "a/../b", ".x", strings.Repeat("a", 129)} {
		if ValidKey(k) {
			t.Errorf("%q should be invalid", k)
		}
	}
}

func TestSetGetListDelete(t *testing.T) {
	for kind, s := range stores(t) {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			if s.Kind() != kind {
				t.Fatalf("kind %s", s.Kind())
			}
			if _, err := s.Get(ctx, "obs/password"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("get missing: %v", err)
			}
			for i := 0; i < 2; i++ { // a retried save is harmless
				if err := s.Set(ctx, "obs/password", NewValue(secretText)); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Set(ctx, "twitch/1/oauth", NewValue("b")); err != nil {
				t.Fatal(err)
			}
			v, err := s.Get(ctx, "obs/password")
			if err != nil || v.Reveal() != secretText {
				t.Fatalf("get: %v", err)
			}
			list, err := s.List(ctx)
			if err != nil || len(list) != 2 || list[0].Key != "obs/password" || list[1].Key != "twitch/1/oauth" {
				t.Fatalf("list: %+v %v", list, err)
			}
			checks, err := s.Check(ctx)
			if err != nil || len(checks) != 2 || checks[0].State != CheckOK || checks[1].State != CheckOK {
				t.Fatalf("check: %+v %v", checks, err)
			}
			for i := 0; i < 2; i++ { // forgetting twice is harmless
				if err := s.Delete(ctx, "obs/password"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Get(ctx, "obs/password"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("after delete: %v", err)
			}
			if list, _ := s.List(ctx); len(list) != 1 {
				t.Fatalf("list after delete: %+v", list)
			}
		})
	}
}

func TestRejectsBadInput(t *testing.T) {
	s := stores(t)[KindFile]
	ctx := context.Background()
	cases := map[string]error{
		"bad key":   s.Set(ctx, "Bad Key", NewValue("x")),
		"empty":     s.Set(ctx, "a", NewValue("")),
		"too large": s.Set(ctx, "a", NewValue(strings.Repeat("x", MaxValueBytes+1))),
		"get bad":   func() error { _, err := s.Get(ctx, "../x"); return err }(),
		"del bad":   s.Delete(ctx, "../x"),
	}
	for name, err := range cases {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if err := s.Set(ctx, "a", NewValue(strings.Repeat("x", MaxValueBytes))); err != nil {
		t.Fatalf("largest value: %v", err)
	}
}

func TestChoiceIsRecordedAndFixed(t *testing.T) {
	dir := t.TempDir()
	db := newDB(t, dir)
	ctx := context.Background()
	fake := newFake()
	s, err := Open(ctx, db, Options{DataDir: dir, provider: fake, goos: "darwin"})
	if err != nil || s.Kind() != KindKeyring {
		t.Fatalf("auto with an answering keyring: %v, %v", s, err)
	}
	// Later starts don't probe again, even if the keyring is now locked.
	fake.err = errors.New("locked")
	before := fake.calls
	s, err = Open(ctx, db, Options{DataDir: dir, provider: fake})
	if err != nil || s.Kind() != KindKeyring || fake.calls != before {
		t.Fatalf("restart: %v %v, %d calls", s, err, fake.calls-before)
	}
	if _, err := Open(ctx, db, Options{DataDir: dir, Want: KindFile, provider: fake}); !errors.Is(err, ErrStoreMismatch) {
		t.Fatalf("asking for another store: %v", err)
	}
}

func TestAutoFallsBackToTheFile(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		fake *fakeKeyring
		goos string
		env  map[string]string
	}{
		"keyring errors":         {fake: &fakeKeyring{items: map[string]string{}, err: errors.New("no service")}, goos: "darwin"},
		"linux without D-Bus":    {fake: newFake(), goos: "linux"},
		"keyring doesn't answer": {fake: &fakeKeyring{items: map[string]string{}, delay: probeTimeout + time.Second}, goos: "windows"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.fake.delay > 0 && testing.Short() {
				t.Skip("slow")
			}
			dir := t.TempDir()
			getenv := func(k string) string { return c.env[k] }
			s, err := Open(ctx, newDB(t, dir), Options{DataDir: dir, provider: c.fake, goos: c.goos, getenv: getenv})
			if err != nil || s.Kind() != KindFile {
				t.Fatalf("got %v, %v", s, err)
			}
			if name == "linux without D-Bus" && c.fake.calls != 0 {
				t.Fatal("probed the keyring without a session bus")
			}
		})
	}
	dir := t.TempDir()
	s, err := Open(ctx, newDB(t, dir), Options{DataDir: dir, provider: newFake(), goos: "linux",
		getenv: func(k string) string { return map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/x"}[k] }})
	if err != nil || s.Kind() != KindKeyring {
		t.Fatalf("linux with D-Bus: %v, %v", s, err)
	}
}

func TestTestsNeverUseTheRealKeyring(t *testing.T) {
	t.Setenv("STREAMGREMLIN_TEST_OS_KEYRING", "")
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, newDB(t, dir), Options{DataDir: dir})
	if err != nil || s.Kind() != KindFile {
		t.Fatalf("auto in a test: %v, %v", s, err)
	}
	dir = t.TempDir()
	if _, err := Open(ctx, newDB(t, dir), Options{DataDir: dir, Want: KindKeyring}); err == nil {
		t.Fatal("a test opened the real keyring")
	}
	// The same code outside a test binary is allowed to.
	dir = t.TempDir()
	if _, err := Open(ctx, newDB(t, dir), Options{DataDir: dir, Want: KindKeyring, inTest: boolp(false), provider: newFake()}); err != nil {
		t.Fatal(err)
	}
}

func TestKeyringTimeout(t *testing.T) {
	k := newKeyringBackend(&fakeKeyring{items: map[string]string{}, delay: 200 * time.Millisecond}, "0123")
	k.timeout = 20 * time.Millisecond
	if _, err := k.get(context.Background(), "a"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	if err := (&keyringBackend{p: &fakeKeyring{err: keyring.ErrSetDataTooBig}, timeout: time.Second}).set(context.Background(), "a", "b"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too big: %v", err)
	}
}

func TestKeyringItemsAreNamespaced(t *testing.T) {
	fake := newFake()
	ctx := context.Background()
	var keys []string
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		s, err := Open(ctx, newDB(t, dir), Options{DataDir: dir, Want: KindKeyring, provider: fake})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set(ctx, "obs/password", NewValue(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	for k := range fake.items {
		keys = append(keys, k)
		if !strings.HasPrefix(k, "StreamGremlin (") {
			t.Errorf("item service %q", k)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("two data folders shared an item: %v", keys)
	}
}

func TestFileIsEncryptedAndPrivate(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db := newDB(t, dir)
	s, err := Open(ctx, db, Options{DataDir: dir, Want: KindFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "obs/password", NewValue(secretText)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{FileName, KeyFileName} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("very-secret")) || bytes.Contains(b, []byte("obs/password")) {
			t.Errorf("%s holds plain text", name)
		}
		if runtime.GOOS != "windows" {
			fi, _ := os.Stat(filepath.Join(dir, name))
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %v", name, fi.Mode().Perm())
			}
		}
	}
	// The database holds the key name, never the value.
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "s.db"))
	if bytes.Contains(b, []byte("very-secret")) {
		t.Error("the database holds the value")
	}
	if !bytes.Contains(b, []byte("obs/password")) {
		t.Error("the database doesn't index the key")
	}
}

func TestFileSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db := newDB(t, dir)
	s, _ := Open(ctx, db, Options{DataDir: dir, Want: KindFile})
	if err := s.Set(ctx, "obs/password", NewValue(secretText)); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, db, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := s2.Get(ctx, "obs/password"); err != nil || v.Reveal() != secretText {
		t.Fatalf("after reopen: %v", err)
	}
}

func TestDamagedFile(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*Store, string) {
		dir := t.TempDir()
		s, err := Open(ctx, newDB(t, dir), Options{DataDir: dir, Want: KindFile})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set(ctx, "obs/password", NewValue(secretText)); err != nil {
			t.Fatal(err)
		}
		return s, dir
	}
	cases := map[string]func(t *testing.T, dir string){
		"flipped byte": func(t *testing.T, dir string) {
			p := filepath.Join(dir, FileName)
			var env envelope
			b, _ := os.ReadFile(p)
			_ = json.Unmarshal(b, &env)
			env.Data[0] ^= 1
			b, _ = json.Marshal(env)
			_ = os.WriteFile(p, b, 0o600)
		},
		"missing key": func(t *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, KeyFileName)) },
		"other key": func(t *testing.T, dir string) {
			_ = os.WriteFile(filepath.Join(dir, KeyFileName), bytes.Repeat([]byte{7}, keyBytes), 0o600)
		},
		"not json": func(t *testing.T, dir string) {
			_ = os.WriteFile(filepath.Join(dir, FileName), []byte("garbage"), 0o600)
		},
		"another copy": nil, // checked below
	}
	for name, damage := range cases {
		if damage == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			s, dir := setup(t)
			damage(t, dir)
			if _, err := s.Get(ctx, "obs/password"); !errors.Is(err, ErrDamaged) {
				t.Fatalf("get: %v", err)
			}
			checks, err := s.Check(ctx)
			if err != nil || len(checks) != 1 || checks[0].State != CheckUnreadable || checks[0].Detail == "" {
				t.Fatalf("check: %+v %v", checks, err)
			}
			if strings.Contains(checks[0].Detail, "very-secret") {
				t.Fatal("detail leaks the value")
			}
			// Saving refuses rather than replacing secrets it can't read.
			if err := s.Set(ctx, "other", NewValue("x")); !errors.Is(err, ErrDamaged) {
				t.Fatalf("set over damage: %v", err)
			}
		})
	}
	t.Run("file from another data folder", func(t *testing.T) {
		_, dirA := setup(t)
		sB, dirB := setup(t)
		for _, n := range []string{FileName, KeyFileName} {
			b, _ := os.ReadFile(filepath.Join(dirA, n))
			_ = os.WriteFile(filepath.Join(dirB, n), b, 0o600)
		}
		if _, err := sB.Get(ctx, "obs/password"); !errors.Is(err, ErrDamaged) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestFileRefusesLooseModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, _ := Open(ctx, newDB(t, dir), Options{DataDir: dir, Want: KindFile})
	if err := s.Set(ctx, "a", NewValue("x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, KeyFileName), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "a"); err == nil || !strings.Contains(err.Error(), "other users") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckReportsMissingValues(t *testing.T) {
	fake := newFake()
	dir := t.TempDir()
	ctx := context.Background()
	s, _ := Open(ctx, newDB(t, dir), Options{DataDir: dir, Want: KindKeyring, provider: fake})
	if err := s.Set(ctx, "obs/password", NewValue("x")); err != nil {
		t.Fatal(err)
	}
	fake.items = map[string]string{} // removed in Keychain Access by hand
	checks, err := s.Check(ctx)
	if err != nil || checks[0].State != CheckMissing {
		t.Fatalf("got %+v %v", checks, err)
	}
	// A failed save leaves the key listed, and checking says it's missing.
	fake.err = errors.New("denied")
	if err := s.Set(ctx, "twitch/1/oauth", NewValue("x")); err == nil {
		t.Fatal("save should fail")
	}
	fake.err = nil
	checks, _ = s.Check(ctx)
	if len(checks) != 2 || checks[1].Key != "twitch/1/oauth" || checks[1].State != CheckMissing {
		t.Fatalf("got %+v", checks)
	}
}

func TestConcurrentSaves(t *testing.T) {
	for kind, s := range stores(t) {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if err := s.Set(ctx, fmt.Sprintf("k/%d", i), NewValue(fmt.Sprint(i))); err != nil {
						t.Error(err)
					}
				}(i)
			}
			wg.Wait()
			for i := 0; i < 20; i++ {
				v, err := s.Get(ctx, fmt.Sprintf("k/%d", i))
				if err != nil || v.Reveal() != fmt.Sprint(i) {
					t.Fatalf("k/%d: %v", i, err)
				}
			}
		})
	}
}

// TestRealKeyring writes, reads and deletes one item in the real OS keyring.
// It runs only with STREAMGREMLIN_TEST_OS_KEYRING=1, and cleans up after itself.
func TestRealKeyring(t *testing.T) {
	if os.Getenv("STREAMGREMLIN_TEST_OS_KEYRING") != "1" {
		t.Skip("set STREAMGREMLIN_TEST_OS_KEYRING=1 to test the real OS keyring")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, newDB(t, dir), Options{DataDir: dir, Want: KindKeyring})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Delete(ctx, "test/real-keyring") })
	if err := s.Set(ctx, "test/real-keyring", NewValue(secretText+"\nwith a second line ✓")); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(ctx, "test/real-keyring"); err != nil || v.Reveal() != secretText+"\nwith a second line ✓" {
		t.Fatalf("read back: %v", err)
	}
	if err := s.Delete(ctx, "test/real-keyring"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "test/real-keyring"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	t.Logf("checked %s", s.Label())
}
