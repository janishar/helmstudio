package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The site builds under the custom domain's root and under a project-pages
// path, every internal link and fragment leads somewhere under either, and the
// three files GitHub Pages reads are there.
func TestTheSiteBuildsForGitHubPages(t *testing.T) {
	for _, base := range []string{"/", "/helmstudio/"} {
		t.Run(base, func(t *testing.T) {
			res, out := buildSite(t, base)
			if problems := CheckLinks(out, base, res); len(problems) > 0 {
				t.Errorf("broken links under %s:\n%s", base, strings.Join(problems, "\n"))
			}
			if b, err := os.ReadFile(filepath.Join(out, "CNAME")); err != nil || string(b) != "helmstudio.in\n" {
				t.Errorf("CNAME = %q, %v; want the domain on one line", b, err)
			}
			for _, f := range []string{".nojekyll", "404.html", "index.html", "favicon.svg", "css/helm.css", "css/site.css", "docs/index.html", "docs/quickstart/index.html"} {
				if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
					t.Errorf("%s: %v", f, err)
				}
			}
			fonts, _ := filepath.Glob(filepath.Join(out, "css", "fonts", "*"))
			if len(fonts) == 0 {
				t.Error("helm-css's fonts were not copied beside it")
			}
			// Nothing is addressed from the domain's root when the site is not
			// served from it.
			if base != "/" {
				b, _ := os.ReadFile(filepath.Join(out, "docs", "quickstart", "index.html"))
				if m := regexp.MustCompile(`(?:href|src)="/(?:docs|css|favicon)`).Find(b); m != nil {
					t.Errorf("the quickstart has %s, outside %s", m, base)
				}
			}
		})
	}
}

// make site runs the command, not the function, so the command is built and
// run once too.
func TestTheCommandBuildsTheSite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	cmd := exec.Command("go", "run", "./cmd/site", "-root", repoRoot, "-site", siteDir, "-out", out, "-helm", helmBin)
	cmd.Dir = siteDir
	b, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(b), "pages in") {
		t.Fatalf("go run ./cmd/site: %v\n%s", err, b)
	}
}

// A broken link or a missing fragment is reported, whether a page's Markdown
// or a template wrote it.
func TestABrokenLinkIsReported(t *testing.T) {
	res, out := buildSite(t, "/")
	page := filepath.Join(out, "docs", "index.html")
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	planted := strings.Replace(string(b), "</main>", `<a href="/docs/nowhere/">x</a><a href="/docs/quickstart/#no-such-heading">y</a><a href="../relative/">z</a></main>`, 1)
	if err := os.WriteFile(page, []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	problems := strings.Join(CheckLinks(out, "/", res), "\n")
	for _, want := range []string{`"/docs/nowhere/" leads nowhere`, "#no-such-heading", `"../relative/" is neither under the base path`} {
		if !strings.Contains(problems, want) {
			t.Errorf("no problem mentions %s; got:\n%s", want, problems)
		}
	}
}

// Every documentation page is in the navigation, and every link in the
// navigation says what its page's title says.
func TestTheNavigationReachesEveryPage(t *testing.T) {
	res, _ := buildSite(t, "/")
	inNav := map[string]string{}
	for _, sec := range res.Nav {
		for _, it := range sec.Items {
			if _, dup := inNav[it.URL]; dup {
				t.Errorf("%s is in the navigation twice", it.URL)
			}
			inNav[it.URL] = it.Title
		}
	}
	for _, p := range res.Pages {
		if !strings.HasPrefix(p.URL, "/docs/") {
			continue
		}
		title, ok := inNav[p.URL]
		if !ok {
			t.Errorf("%s (%s) is in no section of the navigation", p.URL, p.Title)
		} else if title != p.Title {
			t.Errorf("the navigation calls %s %q, and the page calls itself %q", p.URL, title, p.Title)
		}
	}
}

// The output is emptied before a build, so a page for something the contract
// no longer has cannot outlive it; and a directory that is not a build of the
// site is never emptied.
func TestAStalePageDoesNotSurviveABuild(t *testing.T) {
	_, out := buildSite(t, "/")
	stale := filepath.Join(out, "docs", "reference", "api", "removed", "index.html")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("an operation the document no longer has"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(Options{Root: repoRoot, Site: siteDir, Out: out, Base: "/", Helm: helmBin}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a page from the last build survived this one")
	}

	notSite := t.TempDir()
	keep := filepath.Join(notSite, "keep.txt")
	if err := os.WriteFile(keep, []byte("someone's file"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Build(Options{Root: repoRoot, Site: siteDir, Out: notSite, Base: "/", Helm: helmBin})
	if err == nil || !strings.Contains(err.Error(), "not a build of the site") {
		t.Errorf("building into a directory of other files: %v; want a refusal", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the directory was emptied anyway: %v", err)
	}
}

// The API reference documents exactly the studio-api and public operations
// the contract has: one section each, and no other.
func TestTheAPIReferenceIsTheContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string   `yaml:"operationId"`
			Tags        []string `yaml:"tags"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, item := range doc.Paths {
		for _, op := range item {
			if len(op.Tags) > 0 && (op.Tags[0] == "studio-api" || op.Tags[0] == "public") {
				want = append(want, op.OperationID)
			}
		}
	}
	sort.Strings(want)

	_, out := buildSite(t, "/")
	pages, _ := filepath.Glob(filepath.Join(out, "docs", "reference", "api", "*", "index.html"))
	section := regexp.MustCompile(`<section class="site-op" id="([A-Za-z0-9]+)">`)
	var got []string
	for _, p := range pages {
		if filepath.Base(filepath.Dir(p)) == "types" {
			continue
		}
		b, _ := os.ReadFile(p)
		for _, m := range section.FindAllStringSubmatch(string(b), -1) {
			got = append(got, m[1])
		}
	}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the reference documents:\n%v\nthe contract has:\n%v", got, want)
	}
}

// The site's own stylesheet is held to the theme lint a studio's is, and the
// lint bites on it.
func TestTheSiteStylesheetPassesTheThemeLint(t *testing.T) {
	if out, err := exec.Command(helmBin, "validate", "-strict", "-theme", filepath.Join(siteDir, "gen", "assets")).CombinedOutput(); err != nil {
		t.Fatalf("helm validate -strict -theme gen/assets: %v\n%s", err, out)
	}
	css, err := os.ReadFile(filepath.Join(siteDir, "gen", "assets", "site.css"))
	if err != nil {
		t.Fatal(err)
	}
	planted := t.TempDir()
	if err := os.WriteFile(filepath.Join(planted, "site.css"), append(css, []byte("\n.site-planted { color: #ff0000; }\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(helmBin, "validate", "-strict", "-theme", planted).CombinedOutput(); err == nil {
		t.Fatalf("a colour literal in the site's stylesheet passed the lint:\n%s", out)
	}
}
