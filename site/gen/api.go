package gen

import (
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The API reference, generated from api/openapi.yaml (M10 Q2, Q8).
//
// Generated, never written by hand, so that reference material cannot drift
// from the document it describes — the rule the three SDK clients already live
// under. It covers the operations a studio author calls: `studio-api`, and the
// two `public` ones. The 38 `launcher` operations are the daemon's own page's.
//
// Each operation shows its call in the three runtime SDK clients, quoted from
// the generated client files themselves. Every generator writes the operation
// it came from as "(METHOD /path)" beside the method, which is how a method is
// found — and a documented operation a client does not have fails the build,
// rather than a page naming a call that does not exist.

type apiOperation struct {
	ID, Group, Method, Capability string
	HTTPMethod, Path              string
	Summary, Description          string
	Tag                           string
	Params                        []apiParam
	Body                          string // the request schema's name, or a content type
	BodyType                      string
	Responses                     []apiResponse
	Events                        []string
	Calls                         []apiCall
}

type apiParam struct {
	Name, In, Type, Description string
	Required                    bool
}

type apiResponse struct {
	Status, Description, Schema string
}

type apiCall struct {
	Language, Signature string
}

type apiGroup struct {
	Name       string
	Operations []apiOperation
}

func apiReference(o Options) ([]apiGroup, []*Page, error) {
	raw, err := os.ReadFile(filepath.Join(o.Root, "api", "openapi.yaml"))
	if err != nil {
		return nil, nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	clients, err := loadClients(o.Root)
	if err != nil {
		return nil, nil, err
	}

	byGroup := map[string][]apiOperation{}
	schemas := map[string]bool{}
	paths := asMap(doc["paths"])
	for _, p := range sortedKeys(paths) {
		item := asMap(paths[p])
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			opRaw, ok := item[method]
			if !ok {
				continue
			}
			op := asMap(opRaw)
			tag := firstString(op["tags"])
			if tag != "studio-api" && tag != "public" {
				continue
			}
			a := apiOperation{
				ID: str(op["operationId"]), Group: str(op["x-helm-group"]), Method: str(op["x-helm-method"]),
				Capability: str(op["x-helm-capability"]), HTTPMethod: strings.ToUpper(method), Path: p,
				Summary: desc(op["summary"]), Description: desc(op["description"]), Tag: tag,
			}
			if a.Group == "" {
				a.Group = "theme"
			}
			for _, prm := range append(asSlice(item["parameters"]), asSlice(op["parameters"])...) {
				pm := resolve(doc, asMap(prm))
				a.Params = append(a.Params, apiParam{
					Name: str(pm["name"]), In: str(pm["in"]), Required: pm["required"] == true,
					Type: typeOf(doc, asMap(pm["schema"]), schemas), Description: desc(pm["description"]),
				})
			}
			if rb := resolve(doc, asMap(op["requestBody"])); len(rb) > 0 {
				for _, ct := range sortedKeys(asMap(rb["content"])) {
					a.BodyType = ct
					a.Body = typeOf(doc, asMap(asMap(asMap(rb["content"])[ct])["schema"]), schemas)
					break
				}
			}
			resps := asMap(op["responses"])
			for _, status := range sortedKeys(resps) {
				rm := resolve(doc, asMap(resps[status]))
				schema := ""
				for _, ct := range sortedKeys(asMap(rm["content"])) {
					schema = typeOf(doc, asMap(asMap(asMap(rm["content"])[ct])["schema"]), schemas)
					break
				}
				a.Responses = append(a.Responses, apiResponse{Status: status, Description: desc(rm["description"]), Schema: schema})
			}
			events := asMap(op["x-helm-events"])
			for _, name := range sortedKeys(events) {
				a.Events = append(a.Events, name+" → "+typeOf(doc, asMap(events[name]), schemas))
			}
			if tag == "studio-api" {
				calls, err := clients.callsFor(a.HTTPMethod, a.Path)
				if err != nil {
					return nil, nil, fmt.Errorf("%s %s (%s): %w", a.HTTPMethod, a.Path, a.ID, err)
				}
				a.Calls = calls
			}
			byGroup[a.Group] = append(byGroup[a.Group], a)
		}
	}

	var groups []apiGroup
	names := make([]string, 0, len(byGroup))
	for name := range byGroup {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		groups = append(groups, apiGroup{Name: name, Operations: byGroup[name]})
	}
	var pages []*Page
	pages = append(pages, apiIndex(o, raw, groups))
	for _, g := range groups {
		pages = append(pages, apiGroupPage(o, g))
	}
	types, err := typesPage(o, doc, schemas)
	if err != nil {
		return nil, nil, err
	}
	pages = append(pages, types)
	return groups, pages, nil
}

func apiIndex(o Options, raw []byte, groups []apiGroup) *Page {
	var b strings.Builder
	b.WriteString(`<h1 class="helm-title">API reference</h1>`)
	b.WriteString(`<p class="helm-body">Every operation a studio calls. A studio calls them through its runtime SDK client, with the token helmstudio or <code>helm dev</code> gives it in <code>HELM_TOKEN</code>; each operation below names the capability its token needs. The two <code>public</code> operations need no token.</p>`)
	b.WriteString(`<p class="helm-body">Generated from <span class="helm-mono">api/openapi.yaml</span>, whose conventions apply to every page here. They are quoted from the document, without the decision-log references it keeps for its own authors:</p>`)
	b.WriteString(`<pre class="site-quote"><code>` + html.EscapeString(withoutDecisionRefs(conventions(raw))) + `</code></pre>`)
	b.WriteString(`<table class="helm-table"><thead><tr><th>Group</th><th>Operations</th></tr></thead><tbody>`)
	for _, g := range groups {
		fmt.Fprintf(&b, `<tr><td><a class="helm-link" href="%s">%s</a></td><td class="helm-mono">%d</td></tr>`,
			html.EscapeString(joinBase(o.Base, "/docs/reference/api/"+g.Name+"/")), html.EscapeString(g.Name), len(g.Operations))
	}
	b.WriteString(`</tbody></table>`)
	fmt.Fprintf(&b, `<p class="helm-body"><a class="helm-link" href="%s">The types</a> every request and response is made of.</p>`,
		html.EscapeString(joinBase(o.Base, "/docs/reference/api/types/")))
	return &Page{
		URL: "/docs/reference/api/", Title: "API reference", Layout: "docs", Body: template.HTML(b.String()),
		GeneratedFrom: "api/openapi.yaml", Description: "Every operation a studio calls, generated from the API document.",
		Internal: []string{"/docs/reference/api/types/"},
	}
}

// conventions is the document's own header comment on the rules that apply to
// every operation: tags, method names, capabilities, ids, errors and paging.
func conventions(raw []byte) string {
	var lines []string
	started := false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "#") {
			if started {
				break
			}
			continue
		}
		t := strings.TrimPrefix(strings.TrimPrefix(line, "#"), " ")
		if strings.HasPrefix(t, "Conventions that apply everywhere") {
			started = true
		}
		if started {
			lines = append(lines, t)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func apiGroupPage(o Options, g apiGroup) *Page {
	var b strings.Builder
	fmt.Fprintf(&b, `<h1 class="helm-title">%s</h1>`, html.EscapeString(g.Name))
	internal := []string{"/docs/reference/api/types/"}
	for _, a := range g.Operations {
		fmt.Fprintf(&b, `<section class="site-op" id="%s">`, html.EscapeString(a.ID))
		fmt.Fprintf(&b, `<h2 class="site-op-title"><span class="site-verb">%s</span> <span class="helm-mono">%s</span></h2>`, a.HTTPMethod, html.EscapeString(a.Path))
		fmt.Fprintf(&b, `<p class="helm-body">%s</p>`, html.EscapeString(a.Summary))
		if a.Description != "" {
			fmt.Fprintf(&b, `<p class="helm-micro site-prose">%s</p>`, html.EscapeString(strings.TrimSpace(a.Description)))
		}
		switch {
		case a.Tag == "public":
			b.WriteString(`<p class="helm-micro">No token. Not in the runtime SDK clients: a page reads it directly, or through its studio's proxy.</p>`)
		case a.Capability == "token":
			b.WriteString(`<p class="helm-micro">Any studio token.</p>`)
		case a.Capability != "":
			fmt.Fprintf(&b, `<p class="helm-micro">Needs the <code>%s</code> capability.</p>`, html.EscapeString(a.Capability))
		}
		if len(a.Calls) > 0 {
			b.WriteString(`<table class="helm-table site-calls"><tbody>`)
			for _, c := range a.Calls {
				fmt.Fprintf(&b, `<tr><th>%s</th><td><code>%s</code></td></tr>`, c.Language, html.EscapeString(c.Signature))
			}
			b.WriteString(`</tbody></table>`)
		}
		if len(a.Params) > 0 {
			b.WriteString(`<table class="helm-table"><thead><tr><th>Parameter</th><th>In</th><th>Type</th><th>About</th></tr></thead><tbody>`)
			for _, p := range a.Params {
				req := ""
				if p.Required {
					req = " · required"
				}
				fmt.Fprintf(&b, `<tr><td class="helm-mono">%s</td><td>%s%s</td><td>%s</td><td>%s</td></tr>`,
					html.EscapeString(p.Name), html.EscapeString(p.In), req, typeLinks(o, p.Type), html.EscapeString(p.Description))
			}
			b.WriteString(`</tbody></table>`)
		}
		if a.Body != "" {
			fmt.Fprintf(&b, `<p class="helm-body">Request body: %s <span class="helm-micro">(%s)</span></p>`, typeLinks(o, a.Body), html.EscapeString(a.BodyType))
		}
		if len(a.Events) > 0 {
			b.WriteString(`<p class="helm-body">Events:</p><ul>`)
			for _, e := range a.Events {
				fmt.Fprintf(&b, `<li class="helm-mono">%s</li>`, typeLinks(o, e))
			}
			b.WriteString(`</ul>`)
		}
		b.WriteString(`<table class="helm-table"><thead><tr><th>Status</th><th>Body</th><th>Means</th></tr></thead><tbody>`)
		for _, r := range a.Responses {
			fmt.Fprintf(&b, `<tr><td class="helm-mono">%s</td><td>%s</td><td>%s</td></tr>`,
				html.EscapeString(r.Status), typeLinks(o, r.Schema), html.EscapeString(strings.TrimSpace(r.Description)))
		}
		b.WriteString(`</tbody></table></section>`)
	}
	return &Page{
		URL: "/docs/reference/api/" + g.Name + "/", Title: "API · " + g.Name, Layout: "docs",
		Body: template.HTML(b.String()), GeneratedFrom: "api/openapi.yaml", Internal: internal,
		Description: fmt.Sprintf("The %s operations of the helmstudio API.", g.Name),
	}
}

var typeName = regexp.MustCompile(`\b[A-Z][A-Za-z0-9]+\b`)

// typeLinks links every schema name in a type expression to the types page.
func typeLinks(o Options, t string) template.HTML {
	if t == "" {
		return ""
	}
	out := typeName.ReplaceAllStringFunc(html.EscapeString(t), func(name string) string {
		return fmt.Sprintf(`<a class="helm-link" href="%s#%s">%s</a>`, html.EscapeString(joinBase(o.Base, "/docs/reference/api/types/")), name, name)
	})
	return template.HTML(`<span class="helm-mono">` + out + `</span>`)
}

// typesPage lists every schema the documented operations reach.
func typesPage(o Options, doc map[string]any, used map[string]bool) (*Page, error) {
	all := asMap(asMap(doc["components"])["schemas"])
	// Follow references to closure, so a type a type names is here too.
	for changed := true; changed; {
		changed = false
		for name := range used {
			before := len(used)
			collectRefs(all[name], used)
			if len(used) != before {
				changed = true
			}
		}
	}
	var b strings.Builder
	b.WriteString(`<h1 class="helm-title">API types</h1><p class="helm-body">Every schema a studio's requests and responses are made of.</p>`)
	for _, name := range sortedKeys(toAnyMap(used)) {
		s, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("the document references a schema %q it does not define", name)
		}
		sm := asMap(s)
		fmt.Fprintf(&b, `<section class="site-op" id="%s"><h2 class="site-op-title helm-mono">%s</h2>`, html.EscapeString(name), html.EscapeString(name))
		if d := desc(sm["description"]); d != "" {
			fmt.Fprintf(&b, `<p class="helm-micro site-prose">%s</p>`, html.EscapeString(strings.TrimSpace(d)))
		}
		if enum := asSlice(sm["enum"]); len(enum) > 0 {
			var vals []string
			for _, v := range enum {
				vals = append(vals, fmt.Sprint(v))
			}
			fmt.Fprintf(&b, `<p class="helm-body">One of <span class="helm-mono">%s</span>.</p>`, html.EscapeString(strings.Join(vals, ", ")))
		}
		props := asMap(sm["properties"])
		for _, part := range asSlice(sm["allOf"]) {
			pm := asMap(part)
			if ref := str(pm["$ref"]); ref != "" {
				fmt.Fprintf(&b, `<p class="helm-body">Everything in %s, and:</p>`, typeLinks(o, refName(ref)))
			}
			for k, v := range asMap(pm["properties"]) {
				if props == nil {
					props = map[string]any{}
				}
				props[k] = v
			}
		}
		if len(props) > 0 {
			required := map[string]bool{}
			for _, r := range asSlice(sm["required"]) {
				required[fmt.Sprint(r)] = true
			}
			b.WriteString(`<table class="helm-table"><thead><tr><th>Field</th><th>Type</th><th>About</th></tr></thead><tbody>`)
			for _, k := range sortedKeys(props) {
				pm := asMap(props[k])
				req := ""
				if required[k] {
					req = ` <span class="helm-micro">required</span>`
				}
				fmt.Fprintf(&b, `<tr><td class="helm-mono">%s%s</td><td>%s</td><td>%s</td></tr>`,
					html.EscapeString(k), req, typeLinks(o, typeOf(doc, pm, nil)), html.EscapeString(strings.TrimSpace(desc(pm["description"]))))
			}
			b.WriteString(`</tbody></table>`)
		}
		b.WriteString(`</section>`)
	}
	return &Page{
		URL: "/docs/reference/api/types/", Title: "API types", Layout: "docs", Body: template.HTML(b.String()),
		GeneratedFrom: "api/openapi.yaml", Description: "Every schema a studio's requests and responses are made of.",
	}, nil
}

// typeOf writes a schema as a short type expression, recording names it uses.
func typeOf(doc map[string]any, s map[string]any, used map[string]bool) string {
	if len(s) == 0 {
		return ""
	}
	if ref := str(s["$ref"]); ref != "" {
		name := refName(ref)
		if used != nil {
			used[name] = true
		}
		return name
	}
	switch t := s["type"].(type) {
	case string:
		switch t {
		case "array":
			return typeOf(doc, asMap(s["items"]), used) + "[]"
		case "object":
			if ap := asMap(s["additionalProperties"]); len(ap) > 0 {
				return "map of " + typeOf(doc, ap, used)
			}
			return "object"
		}
		if enum := asSlice(s["enum"]); len(enum) > 0 {
			var vals []string
			for _, v := range enum {
				vals = append(vals, fmt.Sprint(v))
			}
			return t + " (" + strings.Join(vals, " | ") + ")"
		}
		return t
	case []any:
		var parts []string
		for _, v := range t {
			parts = append(parts, fmt.Sprint(v))
		}
		return strings.Join(parts, " | ")
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if alts := asSlice(s[key]); len(alts) > 0 {
			var parts []string
			for _, alt := range alts {
				parts = append(parts, typeOf(doc, asMap(alt), used))
			}
			return strings.Join(parts, " | ")
		}
	}
	if enum := asSlice(s["enum"]); len(enum) > 0 {
		var vals []string
		for _, v := range enum {
			vals = append(vals, fmt.Sprint(v))
		}
		return strings.Join(vals, " | ")
	}
	return "any"
}

func collectRefs(v any, used map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			if k == "$ref" {
				if s, ok := vv.(string); ok && strings.HasPrefix(s, "#/components/schemas/") {
					used[refName(s)] = true
				}
				continue
			}
			collectRefs(vv, used)
		}
	case []any:
		for _, vv := range t {
			collectRefs(vv, used)
		}
	}
}

func refName(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

// resolve follows a component $ref for a parameter, a body or a response.
func resolve(doc map[string]any, m map[string]any) map[string]any {
	ref := str(m["$ref"])
	if ref == "" || !strings.HasPrefix(ref, "#/") {
		return m
	}
	cur := any(doc)
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = asMap(cur)[seg]
	}
	return asMap(cur)
}

// ----------------------------------------------------------------- clients

type clientFiles struct {
	goSrc, pySrc, jsSrc string
}

func loadClients(root string) (*clientFiles, error) {
	read := func(parts ...string) (string, error) {
		b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		return string(b), err
	}
	var c clientFiles
	var err error
	if c.goSrc, err = read("packages", "helm-runtime-sdk", "go", "zz_client.go"); err != nil {
		return nil, err
	}
	if c.pySrc, err = read("packages", "helm-runtime-sdk", "python", "helm_runtime_sdk", "_generated.py"); err != nil {
		return nil, err
	}
	if c.jsSrc, err = read("packages", "helm-runtime-sdk", "node", "src", "generated.js"); err != nil {
		return nil, err
	}
	return &c, nil
}

// callsFor finds an operation's method in each generated client, by the
// "(METHOD /path)" each generator writes beside it.
func (c *clientFiles) callsFor(method, path string) ([]apiCall, error) {
	marker := "(" + method + " " + path + ")"
	find := func(lang, src string, pick func(lines []string, i int) string) (apiCall, error) {
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if strings.Contains(line, marker) {
				if sig := pick(lines, i); sig != "" {
					return apiCall{Language: lang, Signature: sig}, nil
				}
			}
		}
		return apiCall{}, fmt.Errorf("the generated %s client has no method for this operation", lang)
	}
	next := func(lines []string, i int) string {
		for j := i + 1; j < len(lines) && j < i+3; j++ {
			if t := strings.TrimSpace(lines[j]); t != "" {
				return strings.TrimSuffix(strings.TrimSuffix(t, "{"), " ")
			}
		}
		return ""
	}
	prev := func(lines []string, i int) string {
		for j := i - 1; j >= 0 && j > i-3; j-- {
			if t := strings.TrimSpace(lines[j]); strings.HasPrefix(t, "def ") {
				return strings.TrimSuffix(t, ":")
			}
		}
		return ""
	}
	goCall, err := find("Go", joinGoComments(c.goSrc), next)
	if err != nil {
		return nil, err
	}
	pyCall, err := find("Python", c.pySrc, prev)
	if err != nil {
		return nil, err
	}
	jsCall, err := find("JavaScript", c.jsSrc, next)
	if err != nil {
		return nil, err
	}
	return []apiCall{goCall, pyCall, jsCall}, nil
}

// joinGoComments puts each run of // comment lines on one line. The Go
// generator wraps a long doc comment, and the wrap can fall at the one space
// inside "(PATCH /gallery/items/{id})", so a marker is only findable in the
// comment as a whole. The line after a run is the method it documents.
func joinGoComments(src string) string {
	var out []string
	var run []string
	flush := func() {
		if len(run) > 0 {
			out = append(out, "// "+strings.Join(run, " "))
			run = nil
		}
	}
	for _, line := range strings.Split(src, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") {
			run = append(run, strings.TrimSpace(strings.TrimPrefix(t, "//")))
			continue
		}
		flush()
		out = append(out, line)
	}
	flush()
	return strings.Join(out, "\n")
}

// ------------------------------------------------------------------ helpers

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// desc reads a description from the contract as this site shows it: without
// the decision-log citations the contract keeps for its own authors. See
// citations.go.
func desc(v any) string { return withoutDecisionRefs(str(v)) }

func firstString(v any) string {
	for _, x := range asSlice(v) {
		if s, ok := x.(string); ok {
			return s
		}
	}
	return ""
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func toAnyMap(m map[string]bool) map[string]any {
	out := make(map[string]any, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
