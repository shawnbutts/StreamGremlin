package main

import (
	"bytes"
	"fmt"
	"go/doc"
	"go/parser"
	"go/token"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The module's name, served by code.streamgremlin.com (D-005).
const module = "code.streamgremlin.com/streamgremlin"

// Pkg is one package of the module, as the code site shows it.
type Pkg struct {
	Path     string // its import path
	Name     string // its last element, or the module's name
	Rel      string // its folder in the repository, "" for the root
	Link     string // to its page, from the page showing this
	Synopsis string // the first sentence of its doc comment
	Kind     string // module, program, internal or package
	Install  bool   // go install makes something useful of it
	Children []Pkg  // the nearest packages inside it
}

// CodePage is what the code site's templates get.
type CodePage struct {
	Page
	Module, Repo, Home, GoVersion string
	Programs, Packages            []Pkg
	Pkg                           Pkg
}

// codeSite writes code.streamgremlin.com (D-116): a home page with every
// program and package, a page for each with the go-import tag pointing git
// at the repository and the go-source tag pointing pkg.go.dev at its
// source, and a 404. It looks like streamgremlin.com, whose stylesheet and
// pictures it carries, and links to it for everything else.
func codeSite(o Options, layout *template.Template, base Page, out string) error {
	pkgs, err := findPackages(o.Root)
	if err != nil {
		return err
	}
	goVersion, err := goVersionOf(filepath.Join(o.Root, "go.mod"))
	if err != nil {
		return err
	}
	for _, d := range []string{out, filepath.Join(out, "img")} {
		if err := mkdir(d); err != nil {
			return err
		}
	}
	src := filepath.Join(o.Root, "website", "code")
	parse := func(name string) (*template.Template, error) {
		return template.Must(layout.Clone()).ParseFiles(filepath.Join(src, name+".html"))
	}
	render := func(t *template.Template, p CodePage, to string) error {
		var buf bytes.Buffer
		if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
			return fmt.Errorf("%s: %w", to, err)
		}
		return writeFile(to, buf.Bytes())
	}
	page := CodePage{Page: base, Module: module, Repo: o.Repo, Home: strings.TrimSuffix(o.Repo, ".git"), GoVersion: goVersion}
	page.Site = "https://streamgremlin.com/"

	// Each package's page, at its import path.
	pkgT, err := parse("package")
	if err != nil {
		return err
	}
	for _, p := range pkgs {
		dir := filepath.Join(out, "streamgremlin", filepath.FromSlash(p.Rel))
		if err := mkdir(dir); err != nil {
			return err
		}
		depth := 1
		if p.Rel != "" {
			depth += strings.Count(p.Rel, "/") + 1
		}
		pp := page
		pp.Name, pp.Assets, pp.Pkg = "package", strings.Repeat("../", depth), p
		for i := range pp.Pkg.Children {
			pp.Pkg.Children[i].Link = strings.TrimPrefix(strings.TrimPrefix(pp.Pkg.Children[i].Rel, p.Rel), "/") + "/index.html"
		}
		if err := render(pkgT, pp, filepath.Join(dir, "index.html")); err != nil {
			return err
		}
	}

	// The home page, with programs first.
	for _, p := range pkgs {
		p.Link, p.Children = "streamgremlin/"+p.Rel+"/index.html", nil
		if p.Rel == "" {
			p.Link = "streamgremlin/index.html"
		}
		switch p.Kind {
		case "program":
			page.Programs = append(page.Programs, p)
		case "internal":
			page.Packages = append(page.Packages, p)
		}
	}
	for name, file := range map[string]string{"index": "index.html", "404": "404.html"} {
		t, err := parse(name)
		if err != nil {
			return err
		}
		pp := page
		pp.Name = name
		if err := render(t, pp, filepath.Join(out, file)); err != nil {
			return err
		}
	}

	copies := map[string]string{
		filepath.Join(o.Root, "website", "site.css"):                     filepath.Join(out, "site.css"),
		filepath.Join(o.Root, "brand-kit", "streamgremlin-icon-32.png"):  filepath.Join(out, "favicon.png"),
		filepath.Join(o.Root, "brand-kit", "streamgremlin-icon-128.png"): filepath.Join(out, "img", "icon-128.png"),
	}
	for from, to := range copies {
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	return writeFile(filepath.Join(out, "robots.txt"), []byte("User-agent: *\nAllow: /\n"))
}

// findPackages reads every folder of the repository with Go code (not only
// tests): its package name and doc comment. The root is the module itself.
func findPackages(root string) ([]Pkg, error) {
	type info struct{ name, doc string }
	found := map[string]*info{"": {}}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata" || name == "bin" || name == "tmp") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		in := found[rel]
		if in == nil {
			in = &info{}
			found[rel] = in
		}
		in.name = f.Name.Name
		if f.Doc != nil && in.doc == "" {
			in.doc = f.Doc.Text()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var rels []string
	for rel := range found {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	byRel := map[string]*Pkg{}
	var out []*Pkg
	for _, rel := range rels {
		in := found[rel]
		p := &Pkg{Path: module, Rel: rel, Synopsis: synopsis(in.doc)}
		switch {
		case rel == "":
			p.Kind = "module"
		case in.name == "main":
			p.Kind = "program"
		case strings.HasPrefix(rel, "internal/"):
			p.Kind = "internal"
		default:
			p.Kind = "package"
		}
		p.Name = "streamgremlin"
		if rel != "" {
			p.Path += "/" + rel
			p.Name = rel[strings.LastIndex(rel, "/")+1:]
		}
		p.Install = rel == "cmd/streamgremlin"
		byRel[rel] = p
		out = append(out, p)
	}
	// Each package under its nearest parent with Go code.
	for _, p := range out {
		if p.Rel == "" {
			continue
		}
		parent := p.Rel
		for {
			i := strings.LastIndex(parent, "/")
			if i < 0 {
				parent = ""
			} else {
				parent = parent[:i]
			}
			if q, ok := byRel[parent]; ok {
				q.Children = append(q.Children, *p)
				break
			}
		}
	}
	res := make([]Pkg, len(out))
	for i, p := range out {
		res[i] = *p
	}
	return res, nil
}

// synopsis is a doc comment's first sentence, ending with a full stop
// even where it led into a list, and without Markdown's backticks.
func synopsis(text string) string {
	s := strings.ReplaceAll(new(doc.Package).Synopsis(text), "`", "")
	if strings.HasSuffix(s, ":") {
		s = strings.TrimSuffix(s, ":") + "."
	}
	return s
}

var goLine = regexp.MustCompile(`(?m)^go (\d+\.\d+)`)

// goVersionOf is the Go version go.mod asks for, as 1.27.
func goVersionOf(gomod string) (string, error) {
	b, err := os.ReadFile(gomod) //nolint:gosec // the repository's own go.mod
	if err != nil {
		return "", err
	}
	m := goLine.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("%s has no go line", gomod)
	}
	return string(m[1]), nil
}
