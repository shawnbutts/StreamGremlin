// Package updates checks GitHub for a newer StreamGremlin and, when the
// streamer asks, downloads it, checks its signature and installs it
// (D-120, docs/adr/0052-updates.md).
//
// Checking is on by default, once a day, with a switch in Settings. Nothing
// is downloaded until the streamer chooses Install, and nothing installs
// while they're live. A download is installed only if SHA256SUMS.txt is
// signed by a key in TrustedKeys and the file matches it.
package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"code.streamgremlin.com/streamgremlin/internal/settings"
)

// Settings are the streamer's choices.
type Settings struct {
	// Check: look for a new version at start and once a day.
	Check bool `json:"check"`
}

// DefaultSettings: checking is on (D-120).
var DefaultSettings = Settings{Check: true}

const settingsKey = "updates"

// States of the check, for Status.State.
const (
	StateDev        = "dev"         // a development build: never checks
	StateOff        = "off"         // checking is off and nothing was checked
	StateNotChecked = "not_checked" // on, not checked yet
	StateChecking   = "checking"
	StateUpToDate   = "up_to_date"
	StateAvailable  = "available"
	StateFailed     = "failed"
)

// States of a download, for Download.State.
const (
	DownloadNone        = "none"
	DownloadRunning     = "downloading"
	DownloadReady       = "ready"
	DownloadFailed      = "failed"
	DownloadInstalling  = "installing"
	downloadsFolderName = "updates"
)

// Release is the newest release this computer could move to.
type Release struct {
	Version     string `json:"version"`
	Prerelease  bool   `json:"prerelease"`
	Notes       string `json:"notes"` // the release's notes, Markdown, shown as text
	URL         string `json:"url"`   // the release's page on GitHub
	PublishedAt string `json:"published_at,omitempty"`
	// Installable: the app can download and install it itself. When not,
	// Why says so and the streamer uses the release page.
	Installable bool   `json:"installable"`
	Why         string `json:"why,omitempty"`
	File        string `json:"file,omitempty"`
	Size        int64  `json:"size,omitempty"`

	fileURL, sumsURL, sigURL string
}

// Download is the state of downloading Release.
type Download struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Error   string `json:"error,omitempty"`
}

// Status is the body of GET /v1/updates.
type Status struct {
	Version   string   `json:"version"` // the running version
	Settings  Settings `json:"settings"`
	State     string   `json:"state"`
	CheckedAt string   `json:"checked_at,omitempty"`
	Error     string   `json:"error,omitempty"`
	Latest    *Release `json:"latest,omitempty"`
	Download  Download `json:"download"`
	// TestServer: checking a stand-in for GitHub, not GitHub.
	TestServer bool `json:"test_server,omitempty"`
}

// Errors the API answers 409 for.
var (
	ErrDev           = errors.New("development builds don't update themselves")
	ErrNothing       = errors.New("there's no newer version to install; check for updates first")
	ErrNotInstalling = errors.New("this version can't be installed from the app; use the release page")
	ErrBusy          = errors.New("the update is already downloading")
	ErrNotReady      = errors.New("the update hasn't finished downloading")
	ErrLive          = errors.New("you're live: install the update after your stream")
)

// Options configure a Service.
type Options struct {
	Settings *settings.Store
	DataDir  string
	// Version is the running version (buildinfo.Version).
	Version string
	// API is GitHub's REST API (DefaultAPI), or a stand-in on this
	// computer for tests (TestServer set).
	API        string
	TestServer bool
	// Repo is "owner/name".
	Repo string
	// Keys are the public keys trusted to sign releases (TrustedKeys).
	Keys   []ed25519.PublicKey
	Client *http.Client
	// GOOS and GOARCH choose the download; the running system's by default.
	GOOS, GOARCH string
	// Live reports whether the streamer is live; nil when that's unknown.
	Live func() bool
	// Backup makes a backup before installing, and names it.
	Backup func(ctx context.Context) (string, error)
	// Apply starts installing file once this process quits: the system's
	// own way by default (apply_*.go). Tests replace it.
	Apply func(file string) error
	// FirstCheck is how long after start the first check waits (1 minute);
	// Every is how often to check after that (a day).
	FirstCheck, Every time.Duration
	Now               func() time.Time
	Logger            *slog.Logger
}

// Service checks for, downloads and installs updates.
type Service struct {
	opts    Options
	current Version
	dev     bool
	checkMu sync.Mutex // one check at a time

	mu        sync.Mutex
	checking  bool
	checkedAt time.Time
	checkErr  string
	waitUntil time.Time // GitHub's rate limit
	latest    *Release
	dl        Download
	file      string // the downloaded file, when ready
	fileSum   string
	ctx       context.Context
}

// New returns a Service; Start begins checking.
func New(opts Options) *Service {
	if opts.API == "" {
		opts.API = DefaultAPI
	}
	opts.API = strings.TrimRight(opts.API, "/")
	if opts.Keys == nil {
		opts.Keys = TrustedKeys()
	}
	if opts.GOOS == "" {
		opts.GOOS, opts.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Transport: http.DefaultTransport}
	}
	if !opts.TestServer {
		c := *opts.Client
		// Releases are fetched over HTTPS only, redirects included.
		c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return errors.New("a download redirected away from HTTPS")
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		}
		opts.Client = &c
	}
	if opts.Apply == nil {
		opts.Apply = apply
	}
	if opts.FirstCheck == 0 {
		opts.FirstCheck = time.Minute
	}
	if opts.Every == 0 {
		opts.Every = 24 * time.Hour
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Service{opts: opts, dl: Download{State: DownloadNone}, ctx: context.Background()}
	s.current, s.dev = func() (Version, bool) {
		v, ok := ParseVersion(opts.Version)
		return v, !ok
	}()
	return s
}

// Dir is where downloads go, in the data folder.
func Dir(dataDir string) string { return filepath.Join(dataDir, downloadsFolderName) }

// Start clears old downloads and checks at start and once a day while
// checking is on, until ctx ends.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	// A download from an earlier run is either installed or stale, and so
	// is an app an earlier install swapped out.
	cleanup()
	if err := os.RemoveAll(Dir(s.opts.DataDir)); err != nil {
		s.opts.Logger.Warn("couldn't clear old update downloads", "err", err)
	}
	if s.dev {
		return
	}
	go s.loop(ctx)
}

func (s *Service) loop(ctx context.Context) {
	t := time.NewTimer(s.opts.FirstCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := s.Settings(ctx)
		if err != nil {
			s.opts.Logger.Warn("couldn't read the update settings", "err", err)
		} else if st.Check {
			_, _ = s.check(ctx, false)
		}
		next := s.opts.Every
		s.mu.Lock()
		if wait := s.waitUntil.Sub(s.opts.Now()); wait > next {
			next = wait
		}
		s.mu.Unlock()
		t.Reset(next)
	}
}

// Settings returns the streamer's choices.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st := DefaultSettings
	if _, err := s.opts.Settings.Load(ctx, settingsKey, &st); err != nil {
		return DefaultSettings, err
	}
	return st, nil
}

// SetSettings saves the streamer's choices.
func (s *Service) SetSettings(ctx context.Context, st Settings) (Settings, error) {
	if err := s.opts.Settings.Save(ctx, settingsKey, st); err != nil {
		return Settings{}, err
	}
	s.opts.Logger.Info("update settings changed", "check", st.Check)
	return st, nil
}

// Status reports the check and any download.
func (s *Service) Status(ctx context.Context) Status {
	st, err := s.Settings(ctx)
	if err != nil {
		s.opts.Logger.Warn("couldn't read the update settings", "err", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Status{Version: s.opts.Version, Settings: st, Download: s.dl, TestServer: s.opts.TestServer}
	if s.latest != nil {
		r := *s.latest
		out.Latest = &r
	}
	if !s.checkedAt.IsZero() {
		out.CheckedAt = s.checkedAt.UTC().Format(time.RFC3339)
	}
	switch {
	case s.dev:
		out.State = StateDev
	case s.checking:
		out.State = StateChecking
	case s.checkErr != "":
		out.State, out.Error = StateFailed, s.checkErr
	case s.latest != nil:
		out.State = StateAvailable
	case !s.checkedAt.IsZero():
		out.State = StateUpToDate
	case !st.Check:
		out.State = StateOff
	default:
		out.State = StateNotChecked
	}
	return out
}

// minManualGap is the least time between checks the streamer asks for:
// asking again sooner answers what's known.
const minManualGap = time.Minute

// Check looks for a newer version now, whether or not checking is on.
func (s *Service) Check(ctx context.Context) (Status, error) {
	if s.dev {
		return s.Status(ctx), ErrDev
	}
	_, err := s.check(ctx, true)
	return s.Status(ctx), err
}

func (s *Service) check(ctx context.Context, manual bool) (*Release, error) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	now := s.opts.Now()
	s.mu.Lock()
	if now.Before(s.waitUntil) || (manual && !s.checkedAt.IsZero() && now.Sub(s.checkedAt) < minManualGap) {
		r := s.latest
		s.mu.Unlock()
		return r, nil
	}
	s.checking = true
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := s.listReleases(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.checking = false
	s.checkedAt = s.opts.Now()
	if err != nil {
		var rl errRateLimited
		if errors.As(err, &rl) && rl.until.After(now) {
			s.waitUntil = rl.until
		}
		s.checkErr = err.Error()
		s.opts.Logger.Warn("checking for updates failed", "err", err)
		return nil, err
	}
	s.checkErr = ""
	s.latest = s.newest(list)
	if s.latest != nil {
		s.opts.Logger.Info("a new version is out", "version", s.latest.Version, "installable", s.latest.Installable)
	} else {
		s.opts.Logger.Info("checked for updates: this is the newest version", "version", s.opts.Version)
	}
	return s.latest, nil
}

// newest picks the newest release newer than this one. A full release only
// moves to full releases; a beta moves to betas and full releases alike.
func (s *Service) newest(list []ghRelease) *Release {
	var best *ghRelease
	var bestV Version
	for i := range list {
		r := &list[i]
		v, ok := tagVersion(r.Tag)
		if r.Draft || !ok || v.Compare(s.current) <= 0 {
			continue
		}
		if !s.current.Prerelease() && (r.Prerelease || v.Prerelease()) {
			continue
		}
		if best == nil || v.Compare(bestV) > 0 {
			best, bestV = r, v
		}
	}
	if best == nil {
		return nil
	}
	out := &Release{Version: bestV.String(), Prerelease: best.Prerelease || bestV.Prerelease(), Notes: best.Body,
		URL: best.HTMLURL, PublishedAt: best.PublishedAt}
	want := assetName(bestV.String(), s.opts.GOOS, s.opts.GOARCH)
	assets := map[string]ghAsset{}
	for _, a := range best.Assets {
		if a.State == "" || a.State == "uploaded" {
			assets[a.Name] = a
		}
	}
	file, hasFile := assets[want]
	sums, hasSums := assets[SumsFile]
	sig, hasSig := assets[SigFile]
	switch {
	case s.opts.GOOS == "linux":
		out.Why = "On Linux, install the .deb, .rpm or .tar.gz from the release page."
	case want == "" || !hasFile:
		out.Why = "This release has no download for this computer. See the release page."
	case !hasSums || !hasSig:
		out.Why = "This release isn't signed, so StreamGremlin won't install it itself. Download it from the release page."
	default:
		out.Installable, out.File, out.Size = true, file.Name, file.Size
		out.fileURL, out.sumsURL, out.sigURL = file.URL, sums.URL, sig.URL
	}
	return out
}

// assetName is the download for a system, or "" when there's none.
func assetName(version, goos, goarch string) string {
	switch {
	case goos == "windows" && goarch == "amd64":
		return "StreamGremlin-" + version + "-windows-x64-setup.exe"
	case goos == "darwin":
		return "StreamGremlin-" + version + "-macos.dmg"
	}
	return ""
}

// maxDownload caps an installer's size.
const maxDownload = 1 << 30

// Download starts downloading the newest release in the background; Status
// shows how it's going.
func (s *Service) Download() error {
	if s.dev {
		return ErrDev
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.latest
	switch {
	case r == nil:
		return ErrNothing
	case !r.Installable:
		return ErrNotInstalling
	case s.dl.State == DownloadRunning || s.dl.State == DownloadInstalling:
		return ErrBusy
	case s.dl.State == DownloadReady && s.dl.Version == r.Version:
		return nil
	}
	s.dl = Download{State: DownloadRunning, Version: r.Version, Total: r.Size}
	s.file, s.fileSum = "", ""
	rel := *r
	ctx := s.ctx
	go s.download(ctx, rel)
	return nil
}

func (s *Service) download(ctx context.Context, r Release) {
	path, sum, err := s.fetch(ctx, r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.dl.State, s.dl.Error = DownloadFailed, err.Error()
		s.opts.Logger.Warn("downloading the update failed", "version", r.Version, "err", err)
		return
	}
	s.dl.State, s.dl.Done = DownloadReady, s.dl.Total
	s.file, s.fileSum = path, sum
	s.opts.Logger.Info("update downloaded and checked", "version", r.Version, "file", r.File)
}

func (s *Service) fetch(ctx context.Context, r Release) (string, string, error) {
	sums, err := s.get(ctx, r.sumsURL, 1<<20, nil)
	if err != nil {
		return "", "", fmt.Errorf("couldn't download %s: %w", SumsFile, err)
	}
	sig, err := s.get(ctx, r.sigURL, MaxSigBytes, nil)
	if err != nil {
		return "", "", fmt.Errorf("couldn't download %s: %w", SigFile, err)
	}
	if err := Verify(s.opts.Keys, sums, sig); err != nil {
		return "", "", err
	}
	list, err := ParseSums(sums)
	if err != nil {
		return "", "", err
	}
	want, ok := list[r.File]
	if !ok {
		return "", "", fmt.Errorf("%s doesn't list %s", SumsFile, r.File)
	}
	dir := Dir(s.opts.DataDir)
	if err := os.RemoveAll(dir); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	final := filepath.Join(dir, filepath.Base(r.File))
	part := final + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // in the data folder
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	_, err = s.get(ctx, r.fileURL, maxDownload, io.MultiWriter(f, h, progress{s}))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(part)
		return "", "", fmt.Errorf("couldn't download %s: %w", r.File, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		_ = os.Remove(part)
		return "", "", fmt.Errorf("%s doesn't match %s: it may have been damaged on the way; try again", r.File, SumsFile)
	}
	if err := os.Rename(part, final); err != nil {
		return "", "", err
	}
	return final, got, nil
}

// progress counts downloaded bytes into the status.
type progress struct{ s *Service }

func (p progress) Write(b []byte) (int, error) {
	p.s.mu.Lock()
	p.s.dl.Done += int64(len(b))
	p.s.mu.Unlock()
	return len(b), nil
}

// get fetches u, at most limit bytes, into w, or returns the bytes when w
// is nil.
func (s *Service) get(ctx context.Context, u string, limit int64, w io.Writer) ([]byte, error) {
	if !s.opts.TestServer && !strings.HasPrefix(u, "https://") {
		return nil, errors.New("not an HTTPS address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.userAgent())
	resp, err := s.opts.Client.Do(req)
	if err != nil {
		return nil, unwrapURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	if resp.ContentLength > limit {
		return nil, errors.New("it's larger than expected")
	}
	body := io.LimitReader(resp.Body, limit+1)
	if w == nil {
		b, err := io.ReadAll(body)
		if err == nil && int64(len(b)) > limit {
			err = errors.New("it's larger than expected")
		}
		return b, err
	}
	n, err := io.Copy(w, body)
	if err == nil && n > limit {
		err = errors.New("it's larger than expected")
	}
	return nil, err
}

// Install backs up, then starts the installer, which waits for this
// process to quit. The caller quits the app when it returns nil.
func (s *Service) Install(ctx context.Context) error {
	if s.dev {
		return ErrDev
	}
	s.mu.Lock()
	file, want, state := s.file, s.fileSum, s.dl.State
	s.mu.Unlock()
	switch {
	case state == DownloadInstalling:
		return nil
	case state != DownloadReady || file == "":
		return ErrNotReady
	case s.opts.Live != nil && s.opts.Live():
		return ErrLive
	}
	// Checked again: the file sat in the data folder since it was checked.
	got, _, err := hashFile(file)
	if err != nil {
		return err
	}
	if got != want {
		s.mu.Lock()
		s.dl = Download{State: DownloadFailed, Version: s.dl.Version, Error: "the downloaded file changed; download it again"}
		s.file = ""
		s.mu.Unlock()
		return errors.New("the downloaded file changed since it was checked; download it again")
	}
	if s.opts.Backup != nil {
		name, err := s.opts.Backup(ctx)
		if err != nil {
			return fmt.Errorf("couldn't back up first, so nothing was installed: %w", err)
		}
		s.opts.Logger.Info("backed up before installing the update", "backup", name)
	}
	if err := s.opts.Apply(file); err != nil {
		s.opts.Logger.Error("couldn't start installing the update", "err", err)
		return fmt.Errorf("couldn't start installing it: %w", err)
	}
	s.mu.Lock()
	s.dl.State = DownloadInstalling
	s.mu.Unlock()
	s.opts.Logger.Info("installing the update; StreamGremlin closes and opens again", "version", s.dl.Version, "file", filepath.Base(file))
	return nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p) //nolint:gosec // in the data folder
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}
