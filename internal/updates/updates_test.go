package updates_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/datadir"
	"code.streamgremlin.com/streamgremlin/internal/mockgithub"
	"code.streamgremlin.com/streamgremlin/internal/settings"
	"code.streamgremlin.com/streamgremlin/internal/storage"
	"code.streamgremlin.com/streamgremlin/internal/updates"
)

func TestVersionOrder(t *testing.T) {
	// Each is older than the next (SemVer 2.0.0's own example, and ours).
	order := []string{"0.0.9", "0.1.0-alpha", "0.1.0-alpha.1", "0.1.0-alpha.beta", "0.1.0-beta", "0.1.0-beta.2",
		"0.1.0-beta.9", "0.1.0-beta.10", "0.1.0-rc.1", "0.1.0", "0.1.1", "0.2.0", "1.0.0"}
	for i := range order {
		a, ok := updates.ParseVersion(order[i])
		if !ok {
			t.Fatalf("%s didn't parse", order[i])
		}
		if a.Compare(a) != 0 {
			t.Errorf("%s isn't equal to itself", a)
		}
		for j := i + 1; j < len(order); j++ {
			b, _ := updates.ParseVersion(order[j])
			if a.Compare(b) != -1 || b.Compare(a) != 1 {
				t.Errorf("%s should be older than %s", a, b)
			}
		}
	}
	for _, bad := range []string{"", "dev", "v0.1.0", "0.1", "0.1.0-", "0.1.0-beta..1", "01.0.0", "0.1.0-beta.01", "0.1.0+build", "0.1.0-béta"} {
		if _, ok := updates.ParseVersion(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSignatures(t *testing.T) {
	pub, priv := newKey(t)
	other, otherPriv := newKey(t)
	sums := []byte("ab  StreamGremlin-0.1.0-macos.dmg\n")
	sig := updates.Sign(priv, sums)
	if err := updates.Verify([]ed25519.PublicKey{other, pub}, sums, sig); err != nil {
		t.Fatalf("a good signature failed: %v", err)
	}
	bad := map[string]struct {
		keys      []ed25519.PublicKey
		sums, sig []byte
	}{
		"changed sums":   {[]ed25519.PublicKey{pub}, []byte("ac  StreamGremlin-0.1.0-macos.dmg\n"), sig},
		"untrusted key":  {[]ed25519.PublicKey{other}, sums, sig},
		"no keys":        {nil, sums, sig},
		"empty":          {[]ed25519.PublicKey{pub}, sums, nil},
		"other key's":    {[]ed25519.PublicKey{pub}, sums, updates.Sign(otherPriv, sums)},
		"not base64":     {[]ed25519.PublicKey{pub}, sums, []byte("streamgremlin-sig-v1 " + updates.KeyID(pub) + " !!!\n")},
		"wrong prefix":   {[]ed25519.PublicKey{pub}, sums, bytes.Replace(sig, []byte("streamgremlin-sig-v1"), []byte("minisign"), 1)},
		"too long":       {[]ed25519.PublicKey{pub}, sums, append(bytes.Repeat([]byte(" "), updates.MaxSigBytes), sig...)},
		"signs raw sums": {[]ed25519.PublicKey{pub}, sums, []byte("streamgremlin-sig-v1 " + updates.KeyID(pub) + " " + b64(ed25519.Sign(priv, sums)))},
	}
	for name, c := range bad {
		if err := updates.Verify(c.keys, c.sums, c.sig); !errors.Is(err, updates.ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestKeyFiles(t *testing.T) {
	text, pubText, err := updates.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := updates.ParsePrivateKey(text)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := updates.ParsePublicKey(pubText)
	if err != nil {
		t.Fatal(err)
	}
	if err := updates.Verify([]ed25519.PublicKey{pub}, []byte("x"), updates.Sign(priv, []byte("x"))); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "streamgremlin-release-key-v1", "streamgremlin-release-key-v1 AAAA", "ssh-ed25519 " + pubText} {
		if _, err := updates.ParsePrivateKey([]byte(bad)); err == nil {
			t.Errorf("%q read as a key", bad)
		}
	}
	if len(updates.TrustedKeys()) == 0 {
		t.Error("the app trusts no keys, so it could never install an update")
	}
}

func TestParseSums(t *testing.T) {
	h := strings.Repeat("ab", 32)
	got, err := updates.ParseSums([]byte(h + "  a.dmg\n" + strings.ToUpper(h) + " *b.exe\r\n\n"))
	if err != nil || got["a.dmg"] != h || got["b.exe"] != h || len(got) != 2 {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"xyz  a.dmg\n", h + "\n", h + "  a\n" + h + "  a\n", "ab  a\n"} {
		if _, err := updates.ParseSums([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

type rig struct {
	svc     *updates.Service
	gh      *mockgithub.Server
	dir     string
	priv    ed25519.PrivateKey
	mu      sync.Mutex
	applied []string
	backups int
	live    bool
	opts    updates.Options
}

func (r *rig) appliedFiles() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.applied...)
}

func newRig(t *testing.T, version string, edit ...func(*updates.Options)) *rig {
	t.Helper()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "data")
	if err := datadir.Ensure(dir); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, datadir.DatabasePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx, datadir.BackupDir(dir)); err != nil {
		t.Fatal(err)
	}
	gh := mockgithub.New()
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	gh.Base = srv.URL
	pub, priv := newKey(t)
	r := &rig{gh: gh, dir: dir, priv: priv}
	r.opts = updates.Options{
		Settings: settings.NewStore(db), DataDir: dir, Version: version, API: srv.URL, TestServer: true,
		Repo: "shawnbutts/StreamGremlin", Keys: []ed25519.PublicKey{pub}, GOOS: "darwin", GOARCH: "arm64",
		Live: func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.live },
		Backup: func(context.Context) (string, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.backups++
			return "streamgremlin-pre-update.zip", nil
		},
		Apply: func(file string) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.applied = append(r.applied, file)
			return nil
		},
		FirstCheck: time.Hour,
	}
	for _, e := range edit {
		e(&r.opts)
	}
	r.svc = updates.New(r.opts)
	sctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	r.svc.Start(sctx)
	return r
}

// release adds a signed release with a macOS, Windows and Linux download.
func (r *rig) release(version string, pre bool) {
	files := map[string][]byte{
		"StreamGremlin-" + version + "-macos.dmg":             []byte("dmg " + version),
		"StreamGremlin-" + version + "-windows-x64-setup.exe": []byte("exe " + version),
		"StreamGremlin-" + version + "-linux-x64.deb":         []byte("deb " + version),
	}
	r.gh.Add(mockgithub.Release{Tag: "v" + version, Prerelease: pre, Notes: "What's new in " + version, Files: mockgithub.Signed(files, r.priv)})
}

func TestFindsTheNewestRelease(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "0.1.0-beta.5")
	if st := r.svc.Status(ctx); st.State != updates.StateNotChecked || !st.Settings.Check {
		t.Fatalf("before checking: %+v", st)
	}
	r.release("0.1.0-beta.4", true)
	r.release("0.1.0-beta.5", true)
	st, err := r.svc.Check(ctx)
	if err != nil || st.State != updates.StateUpToDate || st.Latest != nil {
		t.Fatalf("up to date: %+v %v", st, err)
	}

	r = newRig(t, "0.1.0-beta.5")
	r.release("0.1.0-beta.10", true)
	r.release("0.1.0-beta.6", true) // listed first, but older
	r.gh.Add(mockgithub.Release{Tag: "v0.1.0-beta.11", Prerelease: true, Draft: true})
	r.gh.Add(mockgithub.Release{Tag: "nightly", Prerelease: true})
	st, err = r.svc.Check(ctx)
	if err != nil || st.State != updates.StateAvailable || st.Latest == nil {
		t.Fatalf("%+v %v", st, err)
	}
	l := st.Latest
	if l.Version != "0.1.0-beta.10" || !l.Prerelease || !l.Installable || l.File != "StreamGremlin-0.1.0-beta.10-macos.dmg" ||
		l.Notes != "What's new in 0.1.0-beta.10" || l.URL != "https://github.com/shawnbutts/StreamGremlin/releases/tag/v0.1.0-beta.10" {
		t.Fatalf("%+v", l)
	}
}

func TestFullReleasesOnlyMoveToFullReleases(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "0.1.0")
	r.release("0.2.0-beta.1", true)
	if st, _ := r.svc.Check(ctx); st.State != updates.StateUpToDate {
		t.Fatalf("a full release was offered a beta: %+v", st)
	}
	// A beta moves on to the full release.
	r = newRig(t, "0.1.0-beta.9")
	r.release("0.1.0", false)
	if st, _ := r.svc.Check(ctx); st.Latest == nil || st.Latest.Version != "0.1.0" || st.Latest.Prerelease {
		t.Fatalf("%+v", st)
	}
}

func TestWhenTheAppCantInstallIt(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "0.1.0-beta.5", func(o *updates.Options) { o.GOOS, o.GOARCH = "linux", "amd64" })
	r.release("0.1.0-beta.6", true)
	st, _ := r.svc.Check(ctx)
	if st.Latest == nil || st.Latest.Installable || !strings.Contains(st.Latest.Why, "Linux") {
		t.Fatalf("Linux: %+v", st.Latest)
	}
	if err := r.svc.Download(); !errors.Is(err, updates.ErrNotInstalling) {
		t.Fatal(err)
	}

	r = newRig(t, "0.1.0-beta.5", func(o *updates.Options) { o.GOOS, o.GOARCH = "windows", "arm64" })
	r.release("0.1.0-beta.6", true)
	if st, _ := r.svc.Check(ctx); st.Latest == nil || st.Latest.Installable || !strings.Contains(st.Latest.Why, "no download for this computer") {
		t.Fatalf("Windows on Arm: %+v", st.Latest)
	}

	// beta.5 and earlier have no signature: shown, not installed.
	r = newRig(t, "0.1.0-beta.4")
	r.gh.Add(mockgithub.Release{Tag: "v0.1.0-beta.5", Prerelease: true, Files: mockgithub.Signed(map[string][]byte{
		"StreamGremlin-0.1.0-beta.5-macos.dmg": []byte("dmg")}, nil)})
	if st, _ := r.svc.Check(ctx); st.Latest == nil || st.Latest.Installable || !strings.Contains(st.Latest.Why, "isn't signed") {
		t.Fatalf("unsigned: %+v", st.Latest)
	}
}

func TestDevBuildsNeverCheck(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "dev", func(o *updates.Options) { o.FirstCheck = time.Millisecond })
	r.release("9.9.9", false)
	if _, err := r.svc.Check(ctx); !errors.Is(err, updates.ErrDev) {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if st := r.svc.Status(ctx); st.State != updates.StateDev || r.gh.Lists() != 0 {
		t.Fatalf("%+v, %d lists", st, r.gh.Lists())
	}
}

func TestChecksOnlyWhileTheSwitchIsOn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "0.1.0-beta.5", func(o *updates.Options) { o.FirstCheck, o.Every = time.Millisecond, 20*time.Millisecond })
	r.release("0.1.0-beta.6", true)
	waitFor(t, func() bool { return r.svc.Status(ctx).State == updates.StateAvailable })

	r = newRig(t, "0.1.0-beta.5", func(o *updates.Options) { o.FirstCheck = time.Hour })
	if _, err := r.svc.SetSettings(ctx, updates.Settings{Check: false}); err != nil {
		t.Fatal(err)
	}
	// As if restarted with the switch off.
	r.opts.FirstCheck, r.opts.Every = time.Millisecond, 5*time.Millisecond
	r2 := updates.New(r.opts)
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r2.Start(sctx)
	time.Sleep(60 * time.Millisecond)
	if st := r2.Status(ctx); st.State != updates.StateOff || r.gh.Lists() != 0 {
		t.Fatalf("switched off: %+v, %d lists", st, r.gh.Lists())
	}
	// Check now still works with the switch off.
	r.release("0.1.0-beta.6", true)
	if st, err := r2.Check(ctx); err != nil || st.State != updates.StateAvailable {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestRateLimitsAreRespected(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "0.1.0-beta.5")
	r.gh.RateLimit(time.Now().Add(time.Hour))
	st, err := r.svc.Check(ctx)
	if err == nil || st.State != updates.StateFailed || !strings.Contains(st.Error, "wait until") {
		t.Fatalf("%+v %v", st, err)
	}
	// Asking again, even after the minute between checks, doesn't call GitHub
	// before the time it gave.
	if _, err := r.svc.Check(ctx); err != nil || r.gh.Lists() != 1 {
		t.Fatalf("%v, %d lists", err, r.gh.Lists())
	}

	// Check now twice in a minute: GitHub is asked once.
	r = newRig(t, "0.1.0-beta.5")
	_, _ = r.svc.Check(ctx)
	_, _ = r.svc.Check(ctx)
	if r.gh.Lists() != 1 {
		t.Fatalf("%d lists", r.gh.Lists())
	}
}

func TestDownloadAndInstall(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, "0.1.0-beta.5")
	r.release("0.1.0-beta.6", true)
	if err := r.svc.Install(ctx); !errors.Is(err, updates.ErrNotReady) {
		t.Fatalf("installed before downloading: %v", err)
	}
	if err := r.svc.Download(); !errors.Is(err, updates.ErrNothing) {
		t.Fatalf("downloaded before checking: %v", err)
	}
	if _, err := r.svc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Download(); err != nil {
		t.Fatal(err)
	}
	st := waitDownload(t, r.svc)
	if st.Download.State != updates.DownloadReady || st.Download.Version != "0.1.0-beta.6" || st.Download.Done != st.Download.Total {
		t.Fatalf("%+v", st.Download)
	}
	// Asking again downloads nothing more.
	if err := r.svc.Download(); err != nil || r.gh.Downloads("StreamGremlin-0.1.0-beta.6-macos.dmg") != 1 {
		t.Fatal(err, r.gh.Downloads("StreamGremlin-0.1.0-beta.6-macos.dmg"))
	}

	r.mu.Lock()
	r.live = true
	r.mu.Unlock()
	if err := r.svc.Install(ctx); !errors.Is(err, updates.ErrLive) || len(r.appliedFiles()) != 0 {
		t.Fatalf("installed while live: %v", err)
	}
	r.mu.Lock()
	r.live = false
	r.mu.Unlock()
	if err := r.svc.Install(ctx); err != nil {
		t.Fatal(err)
	}
	applied := r.appliedFiles()
	if len(applied) != 1 || r.backups != 1 {
		t.Fatalf("applied %v, %d backups", applied, r.backups)
	}
	if b, err := os.ReadFile(applied[0]); err != nil || string(b) != "dmg 0.1.0-beta.6" {
		t.Fatalf("%q %v", b, err)
	}
	if filepath.Dir(applied[0]) != updates.Dir(r.dir) {
		t.Fatalf("downloaded to %s", applied[0])
	}
	if st := r.svc.Status(ctx); st.Download.State != updates.DownloadInstalling {
		t.Fatalf("%+v", st.Download)
	}
	// Windows gets the installer.
	r = newRig(t, "0.1.0-beta.5", func(o *updates.Options) { o.GOOS, o.GOARCH = "windows", "amd64" })
	r.release("0.1.0-beta.6", true)
	_, _ = r.svc.Check(ctx)
	_ = r.svc.Download()
	waitDownload(t, r.svc)
	if err := r.svc.Install(ctx); err != nil || filepath.Base(r.appliedFiles()[0]) != "StreamGremlin-0.1.0-beta.6-windows-x64-setup.exe" {
		t.Fatal(err, r.appliedFiles())
	}
}

func TestBadDownloadsAreNeverInstalled(t *testing.T) {
	ctx := context.Background()
	_, otherPriv := newKey(t)
	cases := map[string]func(r *rig){
		"damaged file": func(r *rig) {
			r.release("0.1.0-beta.6", true)
			r.gh.Corrupt("StreamGremlin-0.1.0-beta.6-macos.dmg")
		},
		"signed by another key": func(r *rig) {
			r.priv = otherPriv
			r.release("0.1.0-beta.6", true)
		},
		"changed sums": func(r *rig) {
			r.release("0.1.0-beta.6", true)
			r.gh.Corrupt("SHA256SUMS.txt")
		},
		"sums without the file": func(r *rig) {
			files := mockgithub.Signed(map[string][]byte{"StreamGremlin-0.1.0-beta.6-windows-x64-setup.exe": []byte("exe")}, r.priv)
			files["StreamGremlin-0.1.0-beta.6-macos.dmg"] = []byte("dmg")
			r.gh.Add(mockgithub.Release{Tag: "v0.1.0-beta.6", Prerelease: true, Files: files})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, "0.1.0-beta.5")
			setup(r)
			if _, err := r.svc.Check(ctx); err != nil {
				t.Fatal(err)
			}
			if err := r.svc.Download(); err != nil {
				t.Fatal(err)
			}
			st := waitDownload(t, r.svc)
			if st.Download.State != updates.DownloadFailed || st.Download.Error == "" {
				t.Fatalf("%+v", st.Download)
			}
			if err := r.svc.Install(ctx); !errors.Is(err, updates.ErrNotReady) || len(r.appliedFiles()) != 0 {
				t.Fatalf("installed: %v", err)
			}
			entries, _ := os.ReadDir(updates.Dir(r.dir))
			if len(entries) != 0 {
				t.Fatalf("left behind: %v", entries)
			}
		})
	}

	// A file changed in the data folder after it was checked.
	r := newRig(t, "0.1.0-beta.5")
	r.release("0.1.0-beta.6", true)
	_, _ = r.svc.Check(ctx)
	_ = r.svc.Download()
	waitDownload(t, r.svc)
	p := filepath.Join(updates.Dir(r.dir), "StreamGremlin-0.1.0-beta.6-macos.dmg")
	if err := os.WriteFile(p, []byte("something else"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Install(ctx); err == nil || len(r.appliedFiles()) != 0 || r.backups != 0 {
		t.Fatalf("installed a changed file: %v", err)
	}
}

func TestOldDownloadsAreClearedAtStart(t *testing.T) {
	r := newRig(t, "0.1.0-beta.5")
	p := filepath.Join(updates.Dir(r.dir), "StreamGremlin-0.1.0-beta.4-macos.dmg")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := updates.New(r.opts)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func waitDownload(t *testing.T, s *updates.Service) updates.Status {
	t.Helper()
	var st updates.Status
	waitFor(t, func() bool {
		st = s.Status(context.Background())
		return st.Download.State != updates.DownloadRunning
	})
	return st
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestManifestIsComplete(t *testing.T) {
	m := updates.GitHubManifest()
	if m.VerifiedAt == "" || len(m.Docs) == 0 || len(m.Capabilities) == 0 {
		t.Fatalf("%+v", m)
	}
	for _, c := range m.Capabilities {
		switch c.Support {
		case "supported", "partial", "unsupported", "unknown":
		default:
			t.Errorf("%s: support %q", c.Name, c.Support)
		}
		if c.Notes == "" {
			t.Errorf("%s has no notes", c.Name)
		}
	}
}
