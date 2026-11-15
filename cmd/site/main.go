// Command site builds StreamGremlin's website into a folder the user hosts
// (M9 slice 4, D-112):
//
//	go run ./cmd/site -out bin/site
//
// bin/site/www is streamgremlin.com: home, features, download, Help (the
// same text as the app's Help, cmd/desktop/frontend/src/help/guide.html,
// with screenshots), privacy, security and the changelog. bin/site/code is
// code.streamgremlin.com: the go-import pages for the Go module's vanity
// path. Everything is static: no scripts, cookies or outside requests.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Options are where the site comes from and goes.
type Options struct {
	Root    string // the repository
	Out     string // the output folder; www and code go inside it
	Release string // built installers to offer (make release), or ""
	Repo    string // where git fetches the code, for go-import
}

func main() {
	var o Options
	flag.StringVar(&o.Root, "root", ".", "the repository")
	flag.StringVar(&o.Out, "out", "bin/site", "where to build the site")
	flag.StringVar(&o.Release, "release", "bin/release", "installers to offer for download (a missing folder offers none)")
	flag.StringVar(&o.Repo, "repo", "https://github.com/shawnbutts/StreamGremlin.git", "the git repository the vanity path points at")
	flag.Parse()
	if err := build(o); err != nil {
		fmt.Fprintln(os.Stderr, "site:", err)
		os.Exit(1)
	}
	fmt.Printf("Built %s/www (streamgremlin.com) and %s/code (code.streamgremlin.com).\n", o.Out, o.Out)
}

// Term is one entry of the shared Help.
type Term struct {
	ID   string
	Name string
	Text template.HTML
	Shot *Shot
}

// Shot is a screenshot taken by cmd/screenshots.
type Shot struct {
	File string // in screenshots/
	Alt  string
}

// shots describes each screenshot, for its alt text. A picture the site
// uses must be here, and a test checks each file exists.
var shots = map[string]Shot{
	"home":            {"home.webp", "The Home screen: nothing needs you, and the Get set up checklist with three of four steps ticked."},
	"live":            {"live.webp", "The Live screen: chat in the middle with timestamps and badges, and coloured buttons with icons on the right."},
	"raid-guard":      {"raid-guard.webp", "The raid guard’s warning on Live: a burst of four first-time chatters right after a raid, saying a friendly raid needs only Dismiss, with Lock chat, Ban these accounts and Dismiss."},
	"workflows":       {"workflows.webp", "The Workflows screen: workflows in groups, each with its trigger, a switch, Test, Edit and Runs."},
	"workflow-editor": {"workflow-editor.webp", "The workflow editor: when a viewer types !clip, for VIPs, mods and you."},
	"events":          {"events.webp", "The Events screen: everything StreamGremlin heard from a made-up stream, newest first."},
	"timers":          {"timers.webp", "The Timers screen: a socials reminder, a hydrate reminder and a Discord plug."},
	"quotes":          {"quotes.webp", "The Quotes screen: numbered quotes with who added them."},
	"overlays":        {"overlays.webp", "The Overlays screen, with an alerts overlay for OBS."},
	"giveaways":       {"giveaways.webp", "A giveaway taking entries: eight viewers typed !enter, and what was said in chat."},
	"points":          {"points.webp", "The Points screen: points called Acorns, and the top viewers."},
	"viewers":         {"viewers.webp", "The Viewers screen: the made-up viewers who chatted, followed or subscribed."},
	"filters":         {"filters.webp", "The Chat filters screen."},
	"banlists":        {"banlists.webp", "The Ban lists screen: a shared list of 250,004 names being looked up on Twitch, with a progress bar, its pace, about when it will finish and what it has found so far, and an earlier list that is done."},
	"wheels":          {"wheels.webp", "The Wheels screen: a wheel of what to play next."},
	"connections":     {"connections.webp", "The Connections screen: the streamer’s Twitch account and a bot account, both signed in."},
	"settings":        {"settings.webp", "The Settings screen."},
}

// helpShots puts a picture beside a Help term.
var helpShots = map[string]string{
	"help-connections": "connections", "help-live": "live", "help-events": "events", "help-workflows": "workflow-editor",
	"help-timers": "timers", "help-quotes": "quotes", "help-overlays": "overlays", "help-giveaways": "giveaways",
	"help-points": "points", "help-viewers": "viewers", "help-chat-filters": "filters", "help-wheels": "wheels",
	"help-ban-lists": "banlists",
}

// Download is one installer on the download page.
type Download struct {
	File   string
	System string // the heading: "Windows", "Linux: Fedora (beta)"
	Button string // after "Download": "for Windows", ".rpm"
	Size   string
	SHA256 string
	rank   int
}

// kinds says what each installer is for, in the order the page lists them.
var kinds = []struct{ suffix, system, button string }{
	{"-setup.exe", "Windows", "for Windows"},
	{".dmg", "macOS", "for macOS"},
	{".deb", "Linux: Ubuntu and Debian (beta)", ".deb"},
	{".rpm", "Linux: Fedora (beta)", ".rpm"},
	{".tar.gz", "Linux: other systems (beta)", ".tar.gz"},
}

// Page is what every template gets.
type Page struct {
	Name      string // the page's file name, without .html
	Site      string // before a link to a page of streamgremlin.com: "" there
	Assets    string // before the stylesheet and pictures: the way to the site's root
	Help      []Term
	Changelog template.HTML
	Security  template.HTML
	Beta      template.HTML
	Version   string
	Downloads []Download
	Shots     map[string]Shot
	Built     string
}

var pages = []string{"index", "features", "download", "help", "privacy", "security", "changelog", "404"}

func build(o Options) error {
	src := filepath.Join(o.Root, "website")
	www := filepath.Join(o.Out, "www")
	if err := os.RemoveAll(o.Out); err != nil {
		return err
	}
	for _, d := range []string{www, filepath.Join(www, "img"), filepath.Join(www, "screenshots")} {
		if err := mkdir(d); err != nil {
			return err
		}
	}

	read := func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(o.Root, p))
		return string(b), err
	}
	guide, err := read("cmd/desktop/frontend/src/help/guide.html")
	if err != nil {
		return err
	}
	terms, err := parseHelp(guide)
	if err != nil {
		return err
	}
	changelog, err := read("CHANGELOG.md")
	if err != nil {
		return err
	}
	security, err := read("SECURITY.md")
	if err != nil {
		return err
	}
	beta, err := read("docs/BETA.md")
	if err != nil {
		return err
	}
	// The pages carry their own h1; the files' first heading goes.
	changelog = dropTitle(changelog)
	security = dropTitle(security)
	beta = dropTitle(beta)

	version, downloads, err := findDownloads(o.Release, filepath.Join(www, "downloads"))
	if err != nil {
		return err
	}
	// markdown escapes everything it's given.
	page := Page{
		Help: terms, Changelog: template.HTML(markdown(changelog, 1)), Security: template.HTML(markdown(security, 0)), //nolint:gosec // escaped by markdown
		Beta:    template.HTML(markdown(beta, 1)), //nolint:gosec // escaped by markdown
		Version: version, Downloads: downloads, Shots: shots,
		Built: time.Now().UTC().Format("2 January 2006"),
	}

	layout, err := template.ParseFiles(filepath.Join(src, "layout.html"))
	if err != nil {
		return err
	}
	for _, name := range pages {
		t, err := template.Must(layout.Clone()).ParseFiles(filepath.Join(src, "pages", name+".html"))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		p := page
		p.Name = name
		if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := writeFile(filepath.Join(www, name+".html"), buf.Bytes()); err != nil {
			return err
		}
	}

	copies := map[string]string{
		filepath.Join(src, "site.css"):                                   filepath.Join(www, "site.css"),
		filepath.Join(src, "img", "banner.webp"):                         filepath.Join(www, "img", "banner.webp"),
		filepath.Join(o.Root, "brand-kit", "streamgremlin-icon-32.png"):  filepath.Join(www, "favicon.png"),
		filepath.Join(o.Root, "brand-kit", "streamgremlin-icon-128.png"): filepath.Join(www, "img", "icon-128.png"),
		filepath.Join(o.Root, "brand-kit", "streamgremlin-icon-256.png"): filepath.Join(www, "img", "icon-256.png"),
	}
	for _, s := range shots {
		copies[filepath.Join(src, "screenshots", s.File)] = filepath.Join(www, "screenshots", s.File)
	}
	for from, to := range copies {
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	if err := writeFile(filepath.Join(www, "robots.txt"), []byte("User-agent: *\nAllow: /\n")); err != nil {
		return err
	}
	return codeSite(o, layout, page, filepath.Join(o.Out, "code"))
}

var termRe = regexp.MustCompile(`(?s)<dt id="([a-z0-9-]+)">(.*?)</dt>\s*<dd>(.*?)</dd>`)

// parseHelp splits the shared Help into its terms.
func parseHelp(guide string) ([]Term, error) {
	var out []Term
	for _, m := range termRe.FindAllStringSubmatch(guide, -1) {
		t := Term{ID: m[1], Name: m[2], Text: template.HTML(strings.TrimSpace(m[3]))} //nolint:gosec // the app's own Help, plain HTML (Help.test.ts checks)
		if s, ok := helpShots[m[1]]; ok {
			shot := shots[s]
			t.Shot = &shot
		}
		out = append(out, t)
	}
	if n := strings.Count(guide, "<dt"); n != len(out) || n == 0 {
		return nil, fmt.Errorf("the shared Help has %d terms, but %d read as a dt with an id and its dd", n, len(out))
	}
	return out, nil
}

func dropTitle(md string) string {
	lines := strings.SplitN(md, "\n", 3)
	switch {
	case len(lines) > 0 && strings.HasPrefix(lines[0], "# "):
		return strings.Join(lines[1:], "\n")
	case len(lines) > 1 && strings.Trim(strings.TrimSpace(lines[1]), "=") == "" && strings.TrimSpace(lines[1]) != "":
		if len(lines) > 2 {
			return lines[2]
		}
		return ""
	}
	return md
}

var installer = regexp.MustCompile(`^StreamGremlin-(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)-(macos|windows|linux)[^/]*\.(dmg|exe|deb|rpm|tar\.gz|zip)$`)

// findDownloads offers the newest version's installers from dir, copying
// them into to with their SHA-256. No folder, or none in it, offers none.
func findDownloads(dir, to string) (string, []Download, error) {
	if dir == "" {
		return "", nil, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	} else if err != nil {
		return "", nil, err
	}
	byVersion := map[string][]string{}
	var versions []string
	for _, e := range entries {
		m := installer.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		if byVersion[m[1]] == nil {
			versions = append(versions, m[1])
		}
		byVersion[m[1]] = append(byVersion[m[1]], e.Name())
	}
	if len(versions) == 0 {
		return "", nil, nil
	}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[i], versions[j]) })
	newest := versions[len(versions)-1]
	if err := mkdir(to); err != nil {
		return "", nil, err
	}
	var out []Download
	var sums strings.Builder
	files := byVersion[newest]
	sort.Strings(files)
	for _, f := range files {
		sum, size, err := copyHashed(filepath.Join(dir, f), filepath.Join(to, f))
		if err != nil {
			return "", nil, err
		}
		d := Download{File: f, System: "Other", Button: "", Size: humanSize(size), SHA256: sum, rank: len(kinds)}
		for i, k := range kinds {
			if strings.HasSuffix(f, k.suffix) {
				d.System, d.Button, d.rank = k.system, k.button, i
				break
			}
		}
		out = append(out, d)
		sums.WriteString(sum + "  " + f + "\n")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].rank < out[j].rank })
	return newest, out, writeFile(filepath.Join(to, "SHA256SUMS.txt"), []byte(sums.String()))
}

var versionPart = regexp.MustCompile(`\d+|[A-Za-z]+`)

// versionLess orders versions like 0.1.0-beta.2 < 0.1.0-beta.10 < 0.1.0.
func versionLess(a, b string) bool {
	core := func(v string) (string, string) {
		c, pre, _ := strings.Cut(v, "-")
		return c, pre
	}
	cmp := func(x, y string) int {
		xs, ys := versionPart.FindAllString(x, -1), versionPart.FindAllString(y, -1)
		for i := 0; i < len(xs) && i < len(ys); i++ {
			xn, xerr := strconv.Atoi(xs[i])
			yn, yerr := strconv.Atoi(ys[i])
			switch {
			case xerr == nil && yerr == nil && xn != yn:
				return xn - yn
			case (xerr != nil || yerr != nil) && xs[i] != ys[i]:
				return strings.Compare(xs[i], ys[i])
			}
		}
		return len(xs) - len(ys)
	}
	ac, ap := core(a)
	bc, bp := core(b)
	if c := cmp(ac, bc); c != 0 {
		return c < 0
	}
	switch {
	case ap == bp:
		return false
	case ap == "":
		return false // a release comes after its pre-releases
	case bp == "":
		return true
	}
	return cmp(ap, bp) < 0
}

func humanSize(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

func copyFile(from, to string) error {
	_, _, err := copyHashed(from, to)
	return err
}

func copyHashed(from, to string) (string, int64, error) {
	in, err := os.Open(from)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // a web server reads the site
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// The site is for a web server to read, so its files are readable by all.

func mkdir(p string) error { return os.MkdirAll(p, 0o755) } //nolint:gosec // a web server reads the site

func writeFile(p string, b []byte) error { return os.WriteFile(p, b, 0o644) } //nolint:gosec // a web server reads the site
