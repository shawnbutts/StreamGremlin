package brand

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// root is the repository, from this package's folder.
const root = "../.."

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, p)) //nolint:gosec // The repository's own files.
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Reports go to GitHub (D-115): every place that tells someone where to
// report a problem says the same place.
func TestWhereReportsGo(t *testing.T) {
	for p, want := range map[string][]string{
		"SECURITY.md":  {SecurityURL},
		"docs/BETA.md": {IssuesURL, Repo + "/security"},
		"README.md":    {IssuesURL},
		"cmd/desktop/frontend/src/screens/Help.svelte": {IssuesURL, SecurityURL},
		"website/pages/help.html":                      {IssuesURL},
		".github/ISSUE_TEMPLATE/config.yml":            {SecurityURL},
		".github/ISSUE_TEMPLATE/bug_report.yml":        {SecurityURL},
	} {
		text := read(t, p)
		for _, u := range want {
			if !strings.Contains(text, u) {
				t.Errorf("%s doesn't say %s", p, u)
			}
		}
		for _, old := range []string{"security@streamgremlin.com", "person who invited", "whoever invited"} {
			if strings.Contains(text, old) {
				t.Errorf("%s still says %q", p, old)
			}
		}
	}
}

// GitHub's issue forms: each has a name, a description and a body whose
// fields have unique ids, and blank issues are off so every report uses
// one.
func TestIssueForms(t *testing.T) {
	forms, err := filepath.Glob(filepath.Join(root, ".github", "ISSUE_TEMPLATE", "*.yml"))
	if err != nil || len(forms) < 3 {
		t.Fatalf("issue forms: %v %v", forms, err)
	}
	for _, f := range forms {
		raw, err := os.ReadFile(f) //nolint:gosec // The repository's own files.
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(f) == "config.yml" {
			var c struct {
				Blank    *bool `yaml:"blank_issues_enabled"`
				Contacts []struct {
					Name, URL, About string
				} `yaml:"contact_links"`
			}
			if err := yaml.Unmarshal(raw, &c); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if c.Blank == nil || *c.Blank || len(c.Contacts) == 0 {
				t.Errorf("%s: blank issues should be off, with contact links", f)
			}
			continue
		}
		var form struct {
			Name, Description string
			Body              []struct {
				Type       string
				ID         string
				Attributes map[string]any
			}
		}
		if err := yaml.Unmarshal(raw, &form); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if form.Name == "" || form.Description == "" || len(form.Body) == 0 {
			t.Errorf("%s: needs a name, a description and a body", f)
		}
		ids := map[string]bool{}
		for _, b := range form.Body {
			if b.Type == "markdown" {
				continue
			}
			if b.ID == "" || ids[b.ID] || b.Attributes["label"] == nil {
				t.Errorf("%s: a %s field needs a unique id and a label", f, b.Type)
			}
			ids[b.ID] = true
		}
	}
}

// The release workflow (D-118): GitHub's own actions pinned to a commit,
// no permissions unless a job asks, every job but the last kept off
// Forgejo, the Twitch Client ID only from a secret, and every make target
// it runs is one the Makefile has.
func TestReleaseWorkflow(t *testing.T) {
	raw := read(t, ".github/workflows/release.yml")
	var wf struct {
		On          map[string]any    `yaml:"on"`
		Permissions map[string]any    `yaml:"permissions"`
		Env         map[string]string `yaml:"env"`
		Jobs        map[string]struct {
			If          string            `yaml:"if"`
			Needs       []string          `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				If   string            `yaml:"if"`
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	if wf.Permissions == nil || len(wf.Permissions) != 0 {
		t.Errorf("the workflow's default permissions should be none: %v", wf.Permissions)
	}
	// A pushed tag, or a run by hand that stops before the release.
	if _, ok := wf.On["push"]; !ok || len(wf.On) != 2 || wf.On["workflow_dispatch"] != nil {
		t.Errorf("only a pushed tag or a run by hand, with no inputs: %v", wf.On)
	}
	makefile := read(t, "Makefile")
	pinned := regexp.MustCompile(`^actions/[a-z-]+@[0-9a-f]{40}$`)
	target := regexp.MustCompile(`\bmake ([a-z-]+(?: [a-z-]+)*)`)
	for name, job := range wf.Jobs {
		if job.If != "github.server_url == 'https://github.com'" && len(job.Needs) == 0 {
			t.Errorf("job %s would run on Forgejo too", name)
		}
		if len(job.Permissions) == 0 {
			t.Errorf("job %s should name its permissions", name)
		}
		for p, level := range job.Permissions {
			if level == "write" && (name != "publish" || p != "contents") {
				t.Errorf("job %s writes %s", name, p)
			}
		}
		for _, s := range job.Steps {
			if s.Uses != "" && !pinned.MatchString(s.Uses) {
				t.Errorf("job %s uses %s: only GitHub's own actions, pinned to a commit", name, s.Uses)
			}
			if strings.Contains(s.Run, "gh release") && s.If != "github.ref_type == 'tag'" {
				t.Errorf("job %s releases from a run that isn't a tag's", name)
			}
			if strings.Contains(s.Run, "${{") {
				t.Errorf("job %s puts an expression in a script; pass it in env", name)
			}
			for k, v := range s.Env {
				if strings.Contains(k, "CLIENT") && v != "${{ secrets."+k+" }}" {
					t.Errorf("job %s sets %s from %q, not its secret", name, k, v)
				}
			}
			for _, m := range target.FindAllStringSubmatch(s.Run, -1) {
				for _, tg := range strings.Fields(m[1]) {
					if !strings.Contains(makefile, "\n"+tg+":") {
						t.Errorf("job %s runs make %s, which the Makefile hasn't", name, tg)
					}
				}
			}
		}
	}
	if len(wf.Jobs) != 3 {
		t.Errorf("jobs: %d", len(wf.Jobs))
	}
	goLine := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindStringSubmatch(read(t, "go.mod"))
	if goLine == nil || wf.Env["GO_SERIES"] != goLine[1]+".x" {
		t.Errorf("the workflow builds with Go %q; go.mod says %v", wf.Env["GO_SERIES"], goLine)
	}
	// make reads the environment, so a workflow-wide name that is also a
	// Makefile variable changes the build (GO once replaced the go command).
	for k := range wf.Env {
		if regexp.MustCompile(`(?m)^` + k + `\s*[:?+]?=`).MatchString(makefile) {
			t.Errorf("the workflow's env %s is also a Makefile variable", k)
		}
	}
}
