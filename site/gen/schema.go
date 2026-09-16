package gen

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"sort"
	"strings"

	"github.com/janishar/helmstudio/schema"
)

// The manifest reference, generated from schema/manifest.json (M10 Q8).
//
// Each field shows what the schema says about it and nothing more. A field the
// schema gives no description is shown as having none — the schema is the
// contract, and a description invented here would be a second one. Those
// fields are reported by MissingDescriptions, for the milestone report to list
// as findings against the schema.

// MissingDescriptions lists the manifest fields the schema does not describe.
func MissingDescriptions() ([]string, error) {
	var doc map[string]any
	if err := json.Unmarshal(schema.Manifest, &doc); err != nil {
		return nil, err
	}
	var out []string
	walkSchema(doc, doc, "", func(ptr string, node map[string]any) {
		if str(node["description"]) == "" && str(node["$ref"]) == "" {
			out = append(out, ptr)
		}
	})
	sort.Strings(out)
	return out, nil
}

func walkSchema(doc, node map[string]any, ptr string, visit func(string, map[string]any)) {
	props := asMap(node["properties"])
	for _, k := range sortedKeys(props) {
		child := asMap(props[k])
		p := ptr + "/" + k
		visit(p, child)
		if len(asMap(child["properties"])) > 0 {
			walkSchema(doc, child, p, visit)
		}
	}
}

func manifestReference(o Options) (*Page, error) {
	var doc map[string]any
	if err := json.Unmarshal(schema.Manifest, &doc); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(`<h1 class="helm-title">Manifest reference</h1>`)
	if d := str(doc["description"]); d != "" {
		fmt.Fprintf(&b, `<p class="helm-body site-prose">%s</p>`, html.EscapeString(strings.TrimSpace(d)))
	}
	b.WriteString(`<p class="helm-body">A studio's <span class="helm-mono">helmstudio.yaml</span>, field by field. Generated from <span class="helm-mono">schema/manifest.json</span>; <code>helm validate</code> checks a manifest against the same file, and adds rules a schema cannot state, such as a process's working directory staying inside the studio.</p>`)
	required := map[string]bool{}
	for _, r := range asSlice(doc["required"]) {
		required[fmt.Sprint(r)] = true
	}
	fmt.Fprintf(&b, `<p class="helm-body">Required at the top: <span class="helm-mono">%s</span>.</p>`, html.EscapeString(joinSorted(required)))

	renderProps(&b, doc, doc, "", 2)

	defs := asMap(doc["$defs"])
	for _, name := range sortedKeys(defs) {
		def := asMap(defs[name])
		fmt.Fprintf(&b, `<section class="site-op" id="def-%s"><h2 class="site-op-title helm-mono">$defs/%s</h2>`, html.EscapeString(name), html.EscapeString(name))
		writeFacts(&b, doc, def)
		renderProps(&b, doc, def, "#/$defs/"+name, 3)
		b.WriteString(`</section>`)
	}
	return &Page{
		URL: "/docs/reference/manifest/", Title: "Manifest reference", Layout: "docs", Body: template.HTML(b.String()),
		GeneratedFrom: "schema/manifest.json", Description: "Every field of helmstudio.yaml, generated from the manifest schema.",
	}, nil
}

func renderProps(b *strings.Builder, doc, node map[string]any, ptr string, level int) {
	props := asMap(node["properties"])
	required := map[string]bool{}
	for _, r := range asSlice(node["required"]) {
		required[fmt.Sprint(r)] = true
	}
	for _, k := range sortedKeys(props) {
		child := asMap(props[k])
		p := ptr + "/" + k
		id := "f" + strings.NewReplacer("/", "-", "#", "", "$", "").Replace(p)
		fmt.Fprintf(b, `<section class="site-op" id="%s"><h%d class="site-op-title"><span class="helm-mono">%s</span>`, html.EscapeString(id), level, html.EscapeString(p))
		if required[k] {
			b.WriteString(` <span class="helm-micro">required</span>`)
		}
		fmt.Fprintf(b, `</h%d>`, level)
		writeFacts(b, doc, child)
		b.WriteString(`</section>`)
		if len(asMap(child["properties"])) > 0 && level < 5 {
			renderProps(b, doc, child, p, level+1)
		}
		if items := asMap(child["items"]); len(asMap(items["properties"])) > 0 && level < 5 {
			renderProps(b, doc, items, p+"/*", level+1)
		}
	}
}

func writeFacts(b *strings.Builder, doc, node map[string]any) {
	var facts []string
	if t := schemaType(node); t != "" {
		facts = append(facts, "type "+t)
	}
	for _, key := range []string{"pattern", "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems", "default", "format"} {
		if v, ok := node[key]; ok {
			j, _ := json.Marshal(v)
			facts = append(facts, key+" "+string(j))
		}
	}
	if len(facts) > 0 {
		fmt.Fprintf(b, `<p class="helm-mono">%s</p>`, html.EscapeString(strings.Join(facts, " · ")))
	}
	if d := str(node["description"]); d != "" {
		fmt.Fprintf(b, `<p class="helm-body site-prose">%s</p>`, html.EscapeString(strings.TrimSpace(d)))
	} else if str(node["$ref"]) == "" {
		b.WriteString(`<p class="helm-micro">The schema gives this field no description.</p>`)
	}
}

func schemaType(n map[string]any) string {
	if ref := str(n["$ref"]); ref != "" {
		return ref
	}
	if enum := asSlice(n["enum"]); len(enum) > 0 {
		var vals []string
		for _, v := range enum {
			j, _ := json.Marshal(v)
			vals = append(vals, string(j))
		}
		return "one of " + strings.Join(vals, ", ")
	}
	switch t := n["type"].(type) {
	case string:
		if t == "array" {
			if it := schemaType(asMap(n["items"])); it != "" {
				return "array of " + it
			}
		}
		return t
	case []any:
		var parts []string
		for _, v := range t {
			parts = append(parts, fmt.Sprint(v))
		}
		return strings.Join(parts, " or ")
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		if alts := asSlice(n[key]); len(alts) > 0 {
			var parts []string
			for _, a := range alts {
				parts = append(parts, schemaType(asMap(a)))
			}
			return strings.Join(parts, " or ")
		}
	}
	return ""
}

func joinSorted(m map[string]bool) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
