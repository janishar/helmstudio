// Package gen builds the documentation and the site as static files
// (docs/agents/milestones/10-docs-and-site.md).
//
// The output is meant to be served from GitHub Pages under a custom domain
// (docs/decisions.md, M10): every page is a directory's index.html, so
// /docs/quickstart/ needs no server rewriting; .nojekyll stops GitHub running
// the output through Jekyll; 404.html is the page GitHub serves for a path that
// does not exist; and a CNAME file names the domain for a deploy from a branch.
// Every internal address is written under a base path, "/" for a custom
// domain, so the same build also works from a project-pages address such as
// /helmstudio/ while the domain is being set up.
package gen

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	helmcss "github.com/janishar/helmstudio/packages/helm-css"
)

//go:embed templates assets
var embedded embed.FS

// Repo is the source repository every page links to.
const Repo = "https://github.com/janishar/helmstudio"

// Options says where the build reads from and writes to.
type Options struct {
	// Root is the repository's root: api/openapi.yaml, schema/, studios/ and
	// the generated clients are read from there.
	Root string
	// Site is the site's own directory: content/, samples/ and annotations/.
	Site string
	// Out is the directory the site is written to. It is emptied first.
	Out string
	// Base is the path the site is served under: "/" for a custom domain.
	Base string
	// CNAME is the custom domain GitHub Pages serves the site at, or "" for
	// none.
	CNAME string
	// Helm is a built `helm` binary, whose own usage the CLI reference quotes.
	Helm string
}

// Page is one page of the output.
type Page struct {
	URL           string // "/docs/quickstart/"
	Title         string
	Description   string
	Body          template.HTML
	Layout        string // "docs" or "page"
	Source        string // the file a person edits, for "This page's source"
	GeneratedFrom string // what a generated page is generated from
	Banner        string // a line shown above the page, for a caution

	Samples  []Sample
	Internal []string
}

// view is what a template renders.
type view struct {
	*Page
	Nav    []NavSection
	Repo   string
	InDocs bool
}

// Result is what a build produced, for the tests to read.
type Result struct {
	Pages   []*Page
	Nav     []NavSection
	Samples map[string][]string // sample path -> the pages that include it
}

// Build writes the site.
func Build(o Options) (*Result, error) {
	if o.Base == "" {
		o.Base = "/"
	}
	if !strings.HasPrefix(o.Base, "/") || !strings.HasSuffix(o.Base, "/") {
		return nil, fmt.Errorf("the base path %q must start and end with /", o.Base)
	}
	r := newRenderer(o.Base, filepath.Join(o.Site, "samples"))

	var pages []*Page
	content, err := contentPages(o, r)
	if err != nil {
		return nil, err
	}
	pages = append(pages, content...)

	groups, apiPages, err := apiReference(o)
	if err != nil {
		return nil, fmt.Errorf("the API reference: %w", err)
	}
	pages = append(pages, apiPages...)

	manifestRef, err := manifestReference(o)
	if err != nil {
		return nil, fmt.Errorf("the manifest reference: %w", err)
	}
	pages = append(pages, manifestRef)

	cliRef, err := cliReference(o)
	if err != nil {
		return nil, fmt.Errorf("the CLI reference: %w", err)
	}
	pages = append(pages, cliRef)

	annotated, err := annotatedManifests(o)
	if err != nil {
		return nil, fmt.Errorf("the annotated manifests: %w", err)
	}
	pages = append(pages, annotated...)

	landing, err := landingPage(o, r)
	if err != nil {
		return nil, fmt.Errorf("the landing page: %w", err)
	}
	pages = append(pages, landing, notFoundPage(o.Base))

	nav, err := titled(navigation(groups), pages)
	if err != nil {
		return nil, err
	}
	if err := write(o, pages, nav); err != nil {
		return nil, err
	}
	res := &Result{Pages: pages, Nav: nav, Samples: map[string][]string{}}
	for _, p := range pages {
		for _, s := range p.Samples {
			res.Samples[s.Path] = append(res.Samples[s.Path], p.URL)
		}
	}
	return res, nil
}

// contentPages renders every Markdown file under content/.
func contentPages(o Options, r *pageRenderer) ([]*Page, error) {
	dir := filepath.Join(o.Site, "content")
	var pages []*Page
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".md" {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out, err := r.render(src)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if out.Title == "" {
			return fmt.Errorf("%s: a page starts with a # title", rel)
		}
		url := "/" + strings.TrimSuffix(rel, ".md") + "/"
		if path.Base(rel) == "index.md" {
			url = "/" + strings.TrimSuffix(rel, "index.md")
		}
		pages = append(pages, &Page{
			URL: url, Title: out.Title, Body: template.HTML(out.HTML), Layout: "docs",
			Source: "site/content/" + rel, Samples: out.Samples, Internal: out.Internal,
			Description: description(src),
		})
		return nil
	})
	sort.Slice(pages, func(i, j int) bool { return pages[i].URL < pages[j].URL })
	return pages, err
}

// description is a page's first paragraph after its title, for the meta tag.
func description(src []byte) string {
	for _, block := range strings.Split(string(src), "\n\n") {
		b := strings.TrimSpace(block)
		if b == "" || strings.HasPrefix(b, "#") || strings.HasPrefix(b, "@sample") || strings.HasPrefix(b, "|") || strings.HasPrefix(b, "-") {
			continue
		}
		b = strings.Join(strings.Fields(b), " ")
		for _, ch := range []string{"**", "`", "_"} {
			b = strings.ReplaceAll(b, ch, "")
		}
		if len(b) > 200 {
			b = b[:strings.LastIndex(b[:200], " ")] + "…"
		}
		return b
	}
	return ""
}

// notFoundPage is what GitHub Pages serves for an address with no page, at
// whatever depth it was asked for, so its links are written from the root.
func notFoundPage(base string) *Page {
	return &Page{
		URL: "/404.html", Title: "Not found", Layout: "page",
		Body: template.HTML(fmt.Sprintf(`<h1>Not found</h1><p>There is no page at this address. The <a class="helm-link" href="%s">documentation</a> starts here, and the <a class="helm-link" href="%s">home page</a> is here.</p>`,
			html.EscapeString(joinBase(base, "/docs/")), html.EscapeString(joinBase(base, "/")))),
		Internal: []string{"/docs/", "/"},
	}
}

// write renders every page and copies the stylesheets and fonts.
func write(o Options, pages []*Page, nav []NavSection) error {
	if err := emptyOutput(o.Out); err != nil {
		return err
	}
	funcs := template.FuncMap{"url": func(p string) string { return joinBase(o.Base, p) }}
	layouts := map[string]*template.Template{}
	for _, name := range []string{"docs", "page"} {
		t, err := template.New("").Funcs(funcs).ParseFS(embedded, "templates/base.html", "templates/"+name+".html")
		if err != nil {
			return err
		}
		layouts[name] = t
	}
	seen := map[string]bool{}
	for _, p := range pages {
		if seen[p.URL] {
			return fmt.Errorf("two pages at %s", p.URL)
		}
		seen[p.URL] = true
		var buf bytes.Buffer
		v := view{Page: p, Nav: nav, Repo: Repo, InDocs: strings.HasPrefix(p.URL, "/docs/")}
		if err := layouts[p.Layout].ExecuteTemplate(&buf, "base", v); err != nil {
			return fmt.Errorf("%s: %w", p.URL, err)
		}
		if err := writeFile(filepath.Join(o.Out, fileFor(p.URL)), buf.Bytes()); err != nil {
			return err
		}
	}

	// helm-css, copied rather than forked (M10 Q14): the file and its fonts,
	// side by side, because helm.css names its fonts relative to itself.
	for _, name := range []string{"helm.css"} {
		b, err := fs.ReadFile(helmcss.Files, name)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(o.Out, "css", name), b); err != nil {
			return err
		}
	}
	fonts, err := fs.ReadDir(helmcss.Files, "fonts")
	if err != nil {
		return err
	}
	for _, f := range fonts {
		b, err := fs.ReadFile(helmcss.Files, "fonts/"+f.Name())
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(o.Out, "css", "fonts", f.Name()), b); err != nil {
			return err
		}
	}
	for dst, src := range map[string]string{"css/site.css": "assets/site.css", "favicon.svg": "assets/favicon.svg"} {
		b, err := fs.ReadFile(embedded, src)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(o.Out, dst), b); err != nil {
			return err
		}
	}

	// GitHub Pages. .nojekyll, so the output is served as it is. CNAME names the
	// domain when the output is published from a branch; a deploy from a
	// workflow ignores the file, and the domain lives in the repository's Pages
	// settings instead, so it is harmless there.
	if err := writeFile(filepath.Join(o.Out, ".nojekyll"), nil); err != nil {
		return err
	}
	if o.CNAME != "" {
		if err := writeFile(filepath.Join(o.Out, "CNAME"), []byte(o.CNAME+"\n")); err != nil {
			return err
		}
	}
	return nil
}

// emptyOutput removes a previous build. It refuses a directory that holds
// anything and is not a build of this site — one with no .nojekyll — because
// emptying the wrong directory, given a mistyped -out, is not recoverable.
func emptyOutput(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		if _, err := os.Stat(filepath.Join(dir, ".nojekyll")); err != nil {
			return fmt.Errorf("%s is not empty and is not a build of the site (it has no .nojekyll), so it is not emptied; choose another -out", dir)
		}
	}
	return os.RemoveAll(dir)
}

// fileFor is where a URL's page is written: a directory's index.html, so
// GitHub Pages serves /docs/quickstart/ without rewriting anything.
func fileFor(url string) string {
	if strings.HasSuffix(url, ".html") {
		return filepath.FromSlash(strings.TrimPrefix(url, "/"))
	}
	return filepath.FromSlash(strings.TrimPrefix(url, "/") + "index.html")
}

func writeFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
