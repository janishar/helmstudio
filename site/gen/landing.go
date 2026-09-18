package gen

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/theme"
)

// The landing page (docs/plan/01-build-plan.md, phase 9; M10 Q12, Q13).
//
// Every fact about a studio on it comes from that studio's manifest — its
// badges are generated, never written — and nothing on it describes something
// the software does not do: no screen recording until there is a real one to
// show (M9), and no install instructions for binaries that do not exist yet.

type landingStudio struct {
	ID, Name, Description, Licence, Repo string
	Kinds                                []string
	Badges                               []string
	HueDark, HueLight                    string
}

// studioBadges is a studio's runtime, in the words a visitor checks their own
// machine against.
func studioBadges(m *manifest.Manifest) []string {
	var out []string
	if m.Runtime.Framework != "" {
		out = append(out, m.Runtime.Framework)
	}
	out = append(out, m.Runtime.Backends...)
	for _, os := range m.Requires.OS {
		name := map[string]string{"darwin": "macOS", "linux": "Linux", "windows": "Windows"}[os]
		if name == "" {
			name = os
		}
		out = append(out, name)
	}
	for _, a := range m.Requires.Arch {
		name := map[string]string{"arm64": "Apple Silicon", "amd64": "x86-64"}[a]
		if name == "" || !contains(m.Requires.OS, "darwin") && a == "arm64" {
			name = a
		}
		out = append(out, name)
	}
	if m.Python != nil && m.Python.Version != "" {
		out = append(out, "Python "+m.Python.Version)
	}
	if m.PeakRAMGB > 0 {
		out = append(out, fmt.Sprintf("peak ~%d GB", m.PeakRAMGB))
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func landingPage(o Options, r *pageRenderer) (*Page, error) {
	var studios []landingStudio
	for _, id := range studiosInOrder {
		b, err := os.ReadFile(filepath.Join(o.Root, "studios", id+".yaml"))
		if err != nil {
			return nil, err
		}
		name := "studios/" + id + ".yaml"
		e, res, err := manifest.LoadEntryBytes(name, b)
		if err != nil {
			return nil, err
		}
		if !res.OK() {
			return nil, fmt.Errorf("%s is not a valid registry entry: %v", name, res.Errors)
		}
		if e.Manifest == nil {
			// The site reads files and never fetches a repository, so a card
			// for a studio whose manifest lives only in its repository would be
			// a card of blanks.
			return nil, fmt.Errorf("%s carries no inline manifest, and the site does not fetch the studio's repository to read one", name)
		}
		m := e.Manifest
		// The hue the launcher shows: the declared one, or the ramp entry the
		// studio's id selects (03 §2c).
		hue := theme.Accent(m)
		studios = append(studios, landingStudio{
			ID: e.ID, Name: m.Name, Description: m.Description,
			Licence: m.License, Repo: e.Repo, Kinds: m.Kinds,
			Badges: studioBadges(m), HueDark: hue.Dark, HueLight: hue.Light,
		})
	}
	// The landing page is a template, not Markdown, so @diagram cannot reach
	// it. `diagram` is the same mechanism behind a template function: the same
	// files, the same check, and the name recorded the same way, so the test
	// that every diagram is on a page counts this one too.
	var used []string
	var diagramErr error
	t, err := template.New("landing").Funcs(template.FuncMap{
		"url":  func(p string) string { return joinBase(o.Base, p) },
		"join": strings.Join,
		"diagram": func(name, caption string) template.HTML {
			body, err := os.ReadFile(filepath.Join(o.Site, "diagrams", name+".svg"))
			if err != nil {
				diagramErr = fmt.Errorf("@diagram %s: %w", name, err)
				return ""
			}
			if err := checkDiagram(name, string(body)); err != nil {
				diagramErr = fmt.Errorf("@diagram %s: %w", name, err)
				return ""
			}
			used = append(used, name)
			return template.HTML(`<figure class="diagram">` + strings.TrimSpace(string(body)) +
				`<figcaption>` + captionHTML(caption) + `</figcaption></figure>`)
		},
	}).ParseFS(embedded, "templates/landing.html")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "landing", map[string]any{"Studios": studios, "Repo": Repo}); err != nil {
		return nil, err
	}
	// A template function cannot fail the execution, so it records why and the
	// build fails here rather than writing a page with a hole in it.
	if diagramErr != nil {
		return nil, diagramErr
	}
	var internal []string
	for _, s := range studios {
		internal = append(internal, "/docs/manifests/"+s.ID+"/")
	}
	internal = append(internal, "/docs/quickstart/", "/docs/guides/wrap-a-repository/", "/docs/reference/manifest/", "/docs/publishing/", "/docs/")
	return &Page{
		URL: "/", Title: "", Layout: "page", Body: template.HTML(buf.String()), Internal: internal, Diagrams: used,
		Description: "Open-weight video, image and speech models on your own machine, without the afternoon of setup.",
		Source:      "site/gen/templates/landing.html",
	}, nil
}
