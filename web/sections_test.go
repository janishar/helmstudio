package web

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/schema"
)

// The editor's form is generated from schema/manifest.json, with a hand-written
// map from pointers to 03 §13a's sections. This is the test that map exists
// for.
//
// The risk the design names: "a hand-written form ... would silently lose a
// field the day the schema gained one." Generating the controls removes half
// of that — the control for a field is whatever the schema says it is — but
// not the other half, because a generated control still has to be placed. So
// every property the schema describes must be either in a section or on the
// list of parts that are deliberately edited as text, with a reason. A new
// property is in neither, and fails here.

func TestEverySchemaPropertyIsPlacedOrDeliberatelyNot(t *testing.T) {
	sections, yamlOnly := readMap(t)

	var doc map[string]any
	if err := json.Unmarshal(schema.Manifest, &doc); err != nil {
		t.Fatal(err)
	}

	covered := func(pointer string) string {
		if sections[pointer] {
			return "form"
		}
		for p := pointer; p != ""; p = p[:strings.LastIndex(p, "/")] {
			if yamlOnly[p] != "" {
				return "yaml"
			}
		}
		return ""
	}

	var missing []string
	var walk func(node map[string]any, at string)
	walk = func(node map[string]any, at string) {
		props, _ := node["properties"].(map[string]any)
		for name := range props {
			pointer := at + "/" + name
			child := resolve(doc, props[name].(map[string]any))
			if covered(pointer) != "" {
				continue
			}
			if _, ok := child["properties"]; ok {
				walk(child, pointer)
				continue
			}
			missing = append(missing, pointer)
		}
	}
	walk(doc, "")

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("the schema describes %d field(s) the editor neither draws nor names:\n  %s\n\n"+
			"Put each one in a section in web/sections.js, or in YAML_ONLY with the reason it is text.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// A pointer in both lists would draw a control for a field the map also says
// is text, and the two would disagree about which one someone had edited.
func TestNoFieldIsBothDrawnAndText(t *testing.T) {
	sections, yamlOnly := readMap(t)
	for pointer := range sections {
		for p := pointer; p != ""; p = p[:strings.LastIndex(p, "/")] {
			if yamlOnly[p] != "" {
				t.Errorf("%s is in a form section and %s is YAML-only", pointer, p)
			}
		}
	}
}

// A field the schema does not describe would draw a control that edits nothing,
// and the daemon would refuse the document the moment it was saved.
func TestEveryFormFieldExistsInTheSchema(t *testing.T) {
	sections, _ := readMap(t)
	var doc map[string]any
	if err := json.Unmarshal(schema.Manifest, &doc); err != nil {
		t.Fatal(err)
	}
	for pointer := range sections {
		if _, err := at(doc, pointer); err != nil {
			t.Errorf("%s: %v", pointer, err)
		}
	}
}

// Every reason is a sentence someone can disagree with. An empty one is a
// field that fell off the form without anybody deciding it should.
func TestEveryTextOnlyFieldSaysWhy(t *testing.T) {
	_, yamlOnly := readMap(t)
	if len(yamlOnly) == 0 {
		t.Fatal("no YAML-only fields were read; the map's shape has changed")
	}
	for pointer, reason := range yamlOnly {
		if len(reason) < 20 || !strings.HasSuffix(reason, ".") {
			t.Errorf("%s: %q is not a reason", pointer, reason)
		}
	}
}

// resolve follows a local $ref, so /sdk/runtime is the string its $defs entry
// describes rather than an object with one property called $ref.
func resolve(doc, node map[string]any) map[string]any {
	ref, ok := node["$ref"].(string)
	if !ok || !strings.HasPrefix(ref, "#/") {
		return node
	}
	cur := any(doc)
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return node
		}
		cur = m[seg]
	}
	if m, ok := cur.(map[string]any); ok {
		return resolve(doc, m)
	}
	return node
}

// at walks a JSON pointer through the schema's `properties`, which is how the
// form generator finds the node for a field.
func at(doc map[string]any, pointer string) (map[string]any, error) {
	node := doc
	for _, seg := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("nothing under %q has properties", seg)
		}
		child, ok := props[seg].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("the schema has no %q", seg)
		}
		node = resolve(doc, child)
	}
	return node, nil
}

var (
	fieldsBlock = regexp.MustCompile(`(?s)fields:\s*\[(.*?)\]`)
	quoted      = regexp.MustCompile(`"(/[^"]*)"`)
	yamlPair    = regexp.MustCompile(`"(/[^"]*)":\s*"((?:[^"\\]|\\.)*)"`)
)

// readMap reads web/sections.js. The map is JavaScript because the form is
// JavaScript; reading it from Go is how helm-ui-sdk's rules are checked too.
func readMap(t *testing.T) (map[string]bool, map[string]string) {
	t.Helper()
	src, err := os.ReadFile("sections.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)

	sections := map[string]bool{}
	for _, block := range fieldsBlock.FindAllStringSubmatch(text, -1) {
		for _, m := range quoted.FindAllStringSubmatch(block[1], -1) {
			sections[m[1]] = true
		}
	}
	if len(sections) == 0 {
		t.Fatal("no form fields were read; the map's shape has changed")
	}

	// Only the YAML_ONLY literal: the labels below it are pointers too, and
	// reading them as reasons would make every field look accounted for.
	start := strings.Index(text, "export const YAML_ONLY = {")
	if start < 0 {
		t.Fatal("YAML_ONLY is gone from web/sections.js")
	}
	end := strings.Index(text[start:], "\n};")
	if end < 0 {
		t.Fatal("YAML_ONLY is not closed")
	}
	yamlOnly := map[string]string{}
	for _, m := range yamlPair.FindAllStringSubmatch(text[start:start+end], -1) {
		yamlOnly[m[1]] = m[2]
	}
	return sections, yamlOnly
}
