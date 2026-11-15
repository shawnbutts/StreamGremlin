package brand

import (
	"os"
	"path/filepath"
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
