package gen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The four launch manifests, annotated (M10 Q10).
//
// Notes are keyed by JSON pointer rather than by line, so that a comment added
// to a studio file does not move every note by one. Three things fail the build:
//
//   - a pointer that names nothing in the file;
//   - a field at the top of the file, or at the top of its manifest, with no
//     note — a field the studio gained is a field a reader would not be told
//     about;
//   - a studio file whose digest is not the one its notes were written against.
//     The digest is bumped on purpose, which is what makes someone read the
//     notes again rather than let them go quietly stale.

type annotations struct {
	File   string            `yaml:"file"`
	Digest string            `yaml:"digest"`
	Title  string            `yaml:"title"`
	Intro  string            `yaml:"intro"`
	Notes  map[string]string `yaml:"notes"`
}

func annotatedManifests(o Options) ([]*Page, error) {
	var pages []*Page
	for _, id := range studiosInOrder {
		p, err := annotatedManifest(o, id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		pages = append(pages, p)
	}
	return pages, nil
}

// Digest is what an annotations file records for the manifest it describes.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func annotatedManifest(o Options, id string) (*Page, error) {
	raw, err := os.ReadFile(filepath.Join(o.Site, "annotations", id+".yaml"))
	if err != nil {
		return nil, err
	}
	var a annotations
	if err := yaml.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	src, err := os.ReadFile(filepath.Join(o.Root, filepath.FromSlash(a.File)))
	if err != nil {
		return nil, err
	}
	if got := Digest(src); got != a.Digest {
		return nil, fmt.Errorf("%s changed since its notes were written (digest %s, notes written against %s): read the notes again, then set digest to the new value", a.File, got, a.Digest)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, err
	}
	lines := map[string]int{} // pointer -> the line its key is on
	indexNode(root.Content[0], "", lines)

	var missing []string
	for ptr := range lines {
		if depth(ptr) == 1 || (strings.HasPrefix(ptr, "/manifest/") && depth(ptr) == 2) {
			if _, ok := a.Notes[ptr]; !ok {
				missing = append(missing, ptr)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return nil, fmt.Errorf("fields with no note: %s", strings.Join(missing, ", "))
	}
	byLine := map[int][]string{}
	for ptr, note := range a.Notes {
		line, ok := lines[ptr]
		if !ok {
			return nil, fmt.Errorf("a note for %s, which is not in %s", ptr, a.File)
		}
		byLine[line] = append(byLine[line], note)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<h1 class="helm-title">%s</h1>`, html.EscapeString(a.Title))
	fmt.Fprintf(&b, `<p class="helm-body site-prose">%s</p>`, html.EscapeString(strings.TrimSpace(a.Intro)))
	fmt.Fprintf(&b, `<p class="helm-micro">The file is <a class="helm-link" href="%s/blob/main/%s"><span class="helm-mono">%s</span></a>, shown in full. A note follows the line it explains.</p>`,
		Repo, html.EscapeString(a.File), html.EscapeString(a.File))
	b.WriteString(`<pre class="site-manifest"><code>`)
	for i, line := range strings.Split(strings.TrimRight(string(src), "\n"), "\n") {
		b.WriteString(html.EscapeString(line))
		b.WriteString("\n")
		notes := byLine[i+1]
		sort.Strings(notes)
		for _, n := range notes {
			fmt.Fprintf(&b, `<span class="note">%s</span>`, html.EscapeString(strings.TrimSpace(n)))
		}
	}
	b.WriteString(`</code></pre>`)
	return &Page{
		URL: "/docs/manifests/" + id + "/", Title: a.Title, Layout: "docs", Body: template.HTML(b.String()),
		Source: "site/annotations/" + id + ".yaml", GeneratedFrom: a.File,
		Description: fmt.Sprintf("%s's manifest, annotated line by line.", id),
	}, nil
}

// indexNode records the line of every mapping key and sequence item.
func indexNode(n *yaml.Node, ptr string, lines map[string]int) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			p := ptr + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(k.Value)
			lines[p] = k.Line
			indexNode(v, p, lines)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			p := ptr + "/" + strconv.Itoa(i)
			lines[p] = item.Line
			indexNode(item, p, lines)
		}
	}
}

func depth(ptr string) int { return strings.Count(ptr, "/") }
