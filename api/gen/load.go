package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// obj is a YAML mapping that remembers its key order, so generated code
// follows the document rather than an alphabet.
type obj struct {
	keys []string
	m    map[string]any
}

func (o *obj) get(k string) any {
	if o == nil {
		return nil
	}
	return o.m[k]
}

func (o *obj) str(k string) string {
	s, _ := o.get(k).(string)
	return s
}

func (o *obj) child(k string) *obj {
	c, _ := o.get(k).(*obj)
	return c
}

func conv(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		return conv(n.Content[0])
	case yaml.AliasNode:
		return conv(n.Alias)
	case yaml.MappingNode:
		o := &obj{m: map[string]any{}}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			v, err := conv(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			if _, dup := o.m[k]; dup {
				return nil, fmt.Errorf("line %d: duplicate key %q", n.Content[i].Line, k)
			}
			o.keys = append(o.keys, k)
			o.m[k] = v
		}
		return o, nil
	case yaml.SequenceNode:
		out := []any{}
		for _, c := range n.Content {
			v, err := conv(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!int":
			return strconv.ParseInt(n.Value, 10, 64)
		case "!!float":
			return strconv.ParseFloat(n.Value, 64)
		case "!!bool":
			return n.Value == "true", nil
		case "!!null":
			return nil, nil
		}
		return n.Value, nil
	}
	return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
}

// Schema is the subset of JSON Schema the contract uses.
type Schema struct {
	Ref         string // a component schema name
	Types       []string
	Format      string
	Enum        []string
	Const       string
	Items       *Schema
	Props       []Prop
	Required    map[string]bool
	AllOf       []*Schema
	OneOf       []*Schema
	Closed      bool // additionalProperties: false
	Description string
	Pattern     string
	MinLength   *int64
	MaxLength   *int64
	Minimum     *float64
	Maximum     *float64
	ExclMinimum *float64
	MinItems    *int64
	MaxItems    *int64
	MinProps    *int64
	Default     any
}

type Prop struct {
	Name string
	S    *Schema
}

func num(v any) *float64 {
	switch t := v.(type) {
	case int64:
		f := float64(t)
		return &f
	case float64:
		return &t
	}
	return nil
}

func integer(v any) *int64 {
	if t, ok := v.(int64); ok {
		return &t
	}
	return nil
}

func parseSchema(v any) *Schema {
	o, ok := v.(*obj)
	if !ok {
		return &Schema{}
	}
	s := &Schema{Required: map[string]bool{}}
	if r := o.str("$ref"); r != "" {
		s.Ref = strings.TrimPrefix(r, "#/components/schemas/")
		return s
	}
	switch t := o.get("type").(type) {
	case string:
		s.Types = []string{t}
	case []any:
		for _, x := range t {
			s.Types = append(s.Types, fmt.Sprint(x))
		}
	}
	s.Format = o.str("format")
	s.Description = o.str("description")
	s.Pattern = o.str("pattern")
	if e, ok := o.get("enum").([]any); ok {
		for _, x := range e {
			s.Enum = append(s.Enum, fmt.Sprint(x))
		}
	}
	if c, ok := o.get("const").(string); ok {
		s.Const = c
	}
	if it := o.get("items"); it != nil {
		s.Items = parseSchema(it)
	}
	if p := o.child("properties"); p != nil {
		for _, k := range p.keys {
			s.Props = append(s.Props, Prop{Name: k, S: parseSchema(p.m[k])})
		}
	}
	if r, ok := o.get("required").([]any); ok {
		for _, x := range r {
			s.Required[fmt.Sprint(x)] = true
		}
	}
	for _, key := range []string{"allOf", "oneOf"} {
		if l, ok := o.get(key).([]any); ok {
			for _, x := range l {
				if key == "allOf" {
					s.AllOf = append(s.AllOf, parseSchema(x))
				} else {
					s.OneOf = append(s.OneOf, parseSchema(x))
				}
			}
		}
	}
	if b, ok := o.get("additionalProperties").(bool); ok && !b {
		s.Closed = true
	}
	s.MinLength, s.MaxLength = integer(o.get("minLength")), integer(o.get("maxLength"))
	s.Minimum, s.Maximum, s.ExclMinimum = num(o.get("minimum")), num(o.get("maximum")), num(o.get("exclusiveMinimum"))
	s.MinItems, s.MaxItems, s.MinProps = integer(o.get("minItems")), integer(o.get("maxItems")), integer(o.get("minProperties"))
	s.Default = o.get("default")
	return s
}

// nullable reports whether null is an allowed value, and returns the schema
// without the null alternative.
func nullable(s *Schema) (*Schema, bool) {
	for i, t := range s.Types {
		if t == "null" {
			c := *s
			c.Types = append(append([]string{}, s.Types[:i]...), s.Types[i+1:]...)
			return &c, true
		}
	}
	if len(s.OneOf) == 2 {
		for i, alt := range s.OneOf {
			if len(alt.Types) == 1 && alt.Types[0] == "null" {
				c := *s.OneOf[1-i]
				if c.Description == "" {
					c.Description = s.Description
				}
				return &c, true
			}
		}
	}
	return s, false
}

func (s *Schema) typ() string {
	if len(s.Types) == 1 {
		return s.Types[0]
	}
	if len(s.Props) > 0 || len(s.AllOf) > 0 {
		return "object"
	}
	return ""
}

type Param struct {
	Name     string
	In       string
	Required bool
	S        *Schema
	Explode  bool
}

type Response struct {
	Status      int
	ContentType string
	S           *Schema // nil for no body
	ETag        bool
}

type Op struct {
	Path, Verb, ID     string
	Group, Method, Cap string
	Tags               []string
	Summary            string
	Params             []Param
	BodyType           string // content type, "" when none
	Body               *Schema
	BodyRequired       bool
	Success            []Response
	Events             []Prop // SSE event name → data schema
}

func (op *Op) studio() bool {
	for _, t := range op.Tags {
		if t == "studio-api" {
			return true
		}
	}
	return false
}

// Kind of response the operation produces.
func (op *Op) kind() string {
	for _, r := range op.Success {
		switch {
		case r.ContentType == "text/event-stream":
			return "sse"
		case r.ContentType != "" && r.ContentType != "application/json":
			return "raw"
		}
	}
	for _, r := range op.Success {
		if r.S != nil {
			return "json"
		}
	}
	return "empty"
}

func (op *Op) result() *Response {
	for i := range op.Success {
		if op.Success[i].S != nil {
			return &op.Success[i]
		}
	}
	return nil
}

func (op *Op) statuses() []int {
	var out []int
	for _, r := range op.Success {
		out = append(out, r.Status)
	}
	sort.Ints(out)
	return out
}

func (op *Op) paramsIn(in ...string) []Param {
	var out []Param
	for _, p := range op.Params {
		for _, i := range in {
			if p.In == i {
				out = append(out, p)
			}
		}
	}
	return out
}

type Doc struct {
	Version    string
	Ops        []*Op
	Schemas    map[string]*Schema
	SchemaKeys []string
}

func load(path string) (*Doc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	v, err := conv(&n)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	root := v.(*obj)
	d := &Doc{Version: root.child("info").str("version"), Schemas: map[string]*Schema{}}
	comps := root.child("components")
	schemas := comps.child("schemas")
	for _, k := range schemas.keys {
		d.Schemas[k] = parseSchema(schemas.m[k])
		d.SchemaKeys = append(d.SchemaKeys, k)
	}
	params := comps.child("parameters")
	resolveParam := func(v any) *obj {
		o := v.(*obj)
		if r := o.str("$ref"); r != "" {
			return params.child(strings.TrimPrefix(r, "#/components/parameters/"))
		}
		return o
	}
	paths := root.child("paths")
	for _, p := range paths.keys {
		item := paths.child(p)
		for _, verb := range item.keys {
			o := item.child(verb)
			op := &Op{Path: p, Verb: strings.ToUpper(verb), ID: o.str("operationId"),
				Group: o.str("x-helm-group"), Method: o.str("x-helm-method"), Cap: o.str("x-helm-capability"),
				Summary: o.str("summary")}
			for _, t := range o.get("tags").([]any) {
				op.Tags = append(op.Tags, t.(string))
			}
			if l, ok := o.get("parameters").([]any); ok {
				for _, pv := range l {
					po := resolveParam(pv)
					req, _ := po.get("required").(bool)
					explode, _ := po.get("explode").(bool)
					op.Params = append(op.Params, Param{Name: po.str("name"), In: po.str("in"), Required: req,
						S: parseSchema(po.get("schema")), Explode: explode})
				}
			}
			if rb := o.child("requestBody"); rb != nil {
				op.BodyRequired, _ = rb.get("required").(bool)
				content := rb.child("content")
				op.BodyType = content.keys[0]
				op.Body = parseSchema(content.child(op.BodyType).get("schema"))
			}
			if ev := o.child("x-helm-events"); ev != nil {
				for _, k := range ev.keys {
					op.Events = append(op.Events, Prop{Name: k, S: parseSchema(ev.m[k])})
				}
			}
			resps := o.child("responses")
			for _, code := range resps.keys {
				status, err := strconv.Atoi(code)
				if err != nil || status < 200 || status > 299 {
					continue
				}
				r := resps.child(code)
				resp := Response{Status: status}
				if h := r.child("headers"); h != nil && h.get("ETag") != nil {
					resp.ETag = true
				}
				if c := r.child("content"); c != nil {
					resp.ContentType = c.keys[0]
					resp.S = parseSchema(c.child(resp.ContentType).get("schema"))
				}
				op.Success = append(op.Success, resp)
			}
			d.Ops = append(d.Ops, op)
		}
	}
	return d, nil
}
