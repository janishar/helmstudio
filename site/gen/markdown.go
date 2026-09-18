package gen

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Markdown, as the documentation is written (docs/decisions.md M10 Q7, Q9).
//
// What is added to plain Markdown, and nothing else:
//
//   - A line `@sample <path>` includes a file from site/samples as a code
//     block. Code is never written inline in prose, because a sample that lives
//     in a page cannot be run and rots silently; a sample that is a file is run
//     by the gate. `@sample <path> not-run: <reason>` marks one the gate cannot
//     run — a step that needs the network — and the page says so.
//   - `@sample <path>#<region>` shows part of that file. The region is marked
//     in the file itself, between a line holding `helm:region <name>` and one
//     holding `helm:endregion`, in whatever comment syntax the file is written
//     in; the marker lines are not shown. The gate still runs the whole file,
//     so an excerpt cannot drift from something that works, and a region that
//     is renamed or deleted fails the build rather than going quiet. Nothing
//     is re-indented: what a page shows is what the file holds.
//   - A link or image whose destination starts with "/" is internal: it is
//     written under the site's base path, and recorded so the build can fail on
//     one that leads nowhere.
//   - `@capabilities` and `@criteria <manifest>` are tables read from
//     internal/manifest, the same tables the approval screen, the editor and
//     `helm validate -criteria` read, so a page cannot describe a capability
//     or a criterion differently from the software. `@criteria` scores a
//     sample manifest, which is included like any other sample.
//
// Raw HTML in a page is not rendered. Everything a page shows is Markdown or a
// sample, which is what keeps a page from carrying a colour or a script the
// theme lint and the reviewer never see.

var (
	sampleLine       = regexp.MustCompile(`^@sample\s+([^\s#]+)(?:#([A-Za-z0-9_-]+))?(?:\s+not-run:\s*(.+))?\s*$`)
	regionMarker     = regexp.MustCompile(`helm:(region|endregion)(?:\s+([A-Za-z0-9_-]+))?`)
	capabilitiesLine = regexp.MustCompile(`^@capabilities\s*$`)
	criteriaLine     = regexp.MustCompile(`^@criteria\s+(\S+)\s*$`)
)

// Sample is one file a page includes.
type Sample struct {
	Path   string // relative to site/samples
	Region string // the part of it a page shows, or "" for the whole file
	NotRun string // why the gate does not run it, or ""
}

// Rendered is a page's Markdown turned into HTML, with what it referred to.
type Rendered struct {
	HTML     string
	Title    string
	Samples  []Sample
	Internal []string // internal link destinations, before the base path
}

type pageRenderer struct {
	base    string
	samples string // the samples directory
	md      goldmark.Markdown
}

func newRenderer(base, samplesDir string) *pageRenderer {
	r := &pageRenderer{base: base, samples: samplesDir}
	r.md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(sampleBlocks{}, 100)),
			// Raw HTML stays escaped: see the package comment.
			ghtml.WithXHTML(),
		),
	)
	return r
}

// render expands samples, parses, rewrites internal links and renders.
func (r *pageRenderer) render(src []byte) (Rendered, error) {
	var out Rendered
	expanded, samples, err := r.expandSamples(src)
	if err != nil {
		return out, err
	}
	out.Samples = samples

	doc := r.md.Parser().Parse(text.NewReader(expanded))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Heading:
			if v.Level == 1 && out.Title == "" {
				out.Title = plainText(v, expanded)
			}
		case *ast.Link:
			v.Destination = r.internal(v.Destination, &out)
		case *ast.Image:
			v.Destination = r.internal(v.Destination, &out)
		}
		return ast.WalkContinue, nil
	})
	var buf bytes.Buffer
	if err := r.md.Renderer().Render(&buf, expanded, doc); err != nil {
		return out, err
	}
	out.HTML = buf.String()
	return out, nil
}

// internal writes an internal destination under the base path.
func (r *pageRenderer) internal(dest []byte, out *Rendered) []byte {
	d := string(dest)
	if !strings.HasPrefix(d, "/") || strings.HasPrefix(d, "//") {
		return dest
	}
	out.Internal = append(out.Internal, d)
	return []byte(joinBase(r.base, d))
}

// joinBase puts an internal path under the base path: "/docs/" under
// "/helmstudio/" is "/helmstudio/docs/".
func joinBase(base, p string) string {
	if base == "" || base == "/" {
		return p
	}
	return strings.TrimSuffix(base, "/") + p
}

// expandSamples replaces each @sample line with a fenced block of the file.
func (r *pageRenderer) expandSamples(src []byte) ([]byte, []Sample, error) {
	var out bytes.Buffer
	var samples []Sample
	for n, line := range strings.SplitAfter(string(src), "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if t := strings.TrimSpace(trimmed); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			// Code written inline is what the directive exists to prevent.
			return nil, nil, fmt.Errorf("line %d: a code block is written inline; put the code in site/samples and include it with @sample", n+1)
		}
		if capabilitiesLine.MatchString(trimmed) {
			out.WriteString(capabilitiesTable())
			continue
		}
		if m := criteriaLine.FindStringSubmatch(trimmed); m != nil {
			file := filepath.Join(r.samples, filepath.FromSlash(m[1]))
			table, err := criteriaTable(file)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: @criteria %s: %w", n+1, m[1], err)
			}
			samples = append(samples, Sample{Path: m[1]})
			out.WriteString(table)
			continue
		}
		m := sampleLine.FindStringSubmatch(trimmed)
		if m == nil {
			if strings.HasPrefix(strings.TrimSpace(trimmed), "@") && !strings.HasPrefix(strings.TrimSpace(trimmed), "@ ") {
				return nil, nil, fmt.Errorf("line %d: %q is not a directive; the directives are @sample, @capabilities and @criteria", n+1, strings.TrimSpace(trimmed))
			}
			out.WriteString(line)
			continue
		}
		s := Sample{Path: m[1], Region: m[2], NotRun: strings.TrimSpace(m[3])}
		body, err := os.ReadFile(filepath.Join(r.samples, filepath.FromSlash(s.Path)))
		if err != nil {
			return nil, nil, fmt.Errorf("@sample %s: %w", s.Path, err)
		}
		if s.Region != "" {
			body, err = cutRegion(body, s.Region)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: @sample %s#%s: %w", n+1, s.Path, s.Region, err)
			}
		}
		samples = append(samples, s)
		fence := "```"
		for strings.Contains(string(body), fence) {
			fence += "`"
		}
		shown := s.Path
		if s.Region != "" {
			shown += "#" + s.Region
		}
		info := language(s.Path) + " sample=" + strconv.Quote(shown)
		if s.NotRun != "" {
			info += " notrun=" + strconv.Quote(s.NotRun)
		}
		fmt.Fprintf(&out, "%s%s\n%s", fence, info, body)
		if !bytes.HasSuffix(body, []byte("\n")) {
			out.WriteString("\n")
		}
		out.WriteString(fence + "\n")
	}
	return out.Bytes(), samples, nil
}

// cutRegion returns the part of a sample file between the line that holds
// `helm:region <name>` and the next one that holds `helm:endregion`, with both
// marker lines dropped. The markers are looked for anywhere in a line so that
// each sample can write them in its own comment syntax, and the bytes between
// them are returned untouched: a page shows what the file holds, at the
// indentation the file holds it at.
//
// Every way of getting it wrong is a build failure rather than a quiet empty
// block, because a region is a claim about a file that the file can stop
// honouring without anyone editing the page.
func cutRegion(body []byte, name string) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	start, end := -1, -1
	for i, l := range lines {
		m := regionMarker.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if m[1] == "region" {
			switch {
			case start >= 0 && end < 0 && m[2] != name:
				return nil, fmt.Errorf("region %q holds another region, opened at line %d; regions do not nest", name, i+1)
			case m[2] != name:
			case start >= 0:
				return nil, fmt.Errorf("region %q is opened twice, at lines %d and %d", name, start+1, i+1)
			default:
				start = i
			}
			continue
		}
		if start >= 0 && end < 0 && (m[2] == "" || m[2] == name) {
			end = i
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("there is no region %q in it; mark one with \"helm:region %s\" and \"helm:endregion\"", name, name)
	}
	if end < 0 {
		return nil, fmt.Errorf("region %q is opened at line %d and never closed with \"helm:endregion\"", name, start+1)
	}
	if end == start+1 {
		return nil, fmt.Errorf("region %q is empty", name)
	}
	return []byte(strings.Join(lines[start+1:end], "\n") + "\n"), nil
}

// language names a sample's language from its extension, for the class a
// code block carries.
func language(p string) string {
	switch path.Ext(p) {
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".js", ".mjs":
		return "javascript"
	case ".sh":
		return "shell"
	case ".yaml", ".yml":
		return "yaml"
	case ".css":
		return "css"
	case ".html":
		return "html"
	case ".json":
		return "json"
	}
	return "text"
}

func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if t, ok := c.(*ast.Text); ok {
				b.Write(t.Segment.Value(src))
			}
			if t, ok := c.(*ast.CodeSpan); ok {
				for cc := t.FirstChild(); cc != nil; cc = cc.NextSibling() {
					if tt, ok := cc.(*ast.Text); ok {
						b.Write(tt.Segment.Value(src))
					}
				}
				return ast.WalkSkipChildren, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// sampleBlocks renders a fenced block as a figure naming the file it came from,
// and whether the gate runs it.
type sampleBlocks struct{}

func (sampleBlocks) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, func(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		n := node.(*ast.FencedCodeBlock)
		info := ""
		if n.Info != nil {
			info = string(n.Info.Segment.Value(src))
		}
		lang, attrs := parseInfo(info)
		fmt.Fprintf(w, `<figure class="sample">`)
		if file := attrs["sample"]; file != "" {
			fmt.Fprintf(w, `<figcaption><span class="helm-mono">%s</span>`, html.EscapeString(file))
			if why := attrs["notrun"]; why != "" {
				fmt.Fprintf(w, ` <span class="not-run">Not run by the gate: %s</span>`, html.EscapeString(why))
			}
			fmt.Fprint(w, `</figcaption>`)
		}
		fmt.Fprintf(w, `<pre><code class="language-%s">`, html.EscapeString(lang))
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			_, _ = w.WriteString(html.EscapeString(string(seg.Value(src))))
		}
		fmt.Fprint(w, "</code></pre></figure>\n")
		return ast.WalkSkipChildren, nil
	})
}

var infoAttr = regexp.MustCompile(`(\w+)="((?:[^"\\]|\\.)*)"`)

func parseInfo(info string) (string, map[string]string) {
	attrs := map[string]string{}
	lang, rest, _ := strings.Cut(info, " ")
	for _, m := range infoAttr.FindAllStringSubmatch(rest, -1) {
		if v, err := strconv.Unquote(`"` + m[2] + `"`); err == nil {
			attrs[m[1]] = v
		}
	}
	return lang, attrs
}

// capabilitiesTable is every capability with the sentence the approval screen
// shows for it.
func capabilitiesTable() string {
	var b strings.Builder
	b.WriteString("\n| Capability | What the approval screen says |\n|---|---|\n")
	for _, c := range manifest.KnownCapabilities() {
		sentence := c.Sentence
		if c.Warning {
			sentence = "**" + sentence + "** Shown as a warning."
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", c.Name, cell(sentence))
	}
	b.WriteString("\n")
	return b.String()
}

// criteriaTable scores a manifest as `helm validate -criteria` does.
func criteriaTable(file string) (string, error) {
	m, res, err := manifest.Load(file)
	if err != nil {
		return "", err
	}
	if !res.OK() {
		return "", fmt.Errorf("the manifest is not valid: %v", res.Errors)
	}
	scored := manifest.Criteria(m)
	var b strings.Builder
	fmt.Fprintf(&b, "\n| # | Criterion | Required | This manifest: %d of %d checkable pass |\n|---|---|---|---|\n", scored.Passed, scored.Checkable)
	for _, c := range scored.Items {
		required := "No"
		if c.Required {
			required = "Yes"
		}
		var result string
		switch c.State {
		case manifest.CriterionPass:
			result = "Passes"
		case manifest.CriterionFail:
			result = "**Fails:** " + c.Detail
		default:
			result = c.Detail
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", c.Number, cell(c.Title), required, cell(result))
	}
	b.WriteString("\n")
	return b.String(), nil
}

// cell makes text safe inside a Markdown table cell.
func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}
