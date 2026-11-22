package studio

// API 1.47 (contracts/README.md, Param rules, option help and memory per run): the one expression language of `when`,
// rules and peak_gb formulas (check.py expr_eval), the rules a filled graph breaks (rule_errors) and a native run's
// own planned peak (run_peak).

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/helmstudio/studio_service/registry"
)

var exprToken = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?|'[^']*'|"[^"]*"|==|!=|<=|>=|&&|\|\||\*\*|[-+*/<>!(),]|[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)?)`)

var exprWords = map[string]any{"true": true, "false": false, "null": nil, "True": true, "False": false, "None": nil}

// errNoRef is what a getter returns for a name that resolves to nothing.
var errNoRef = errors.New("no such reference")

type exprParser struct {
	tokens []string
	i      int
	get    func(string) (any, error)
}

type exprError struct{ err error }

// evalExpr evaluates text (check.py expr_eval): numbers are float64, rows []any.
func evalExpr(text string, get func(string) (any, error)) (out any, err error) {
	p := &exprParser{get: get}
	for rest := text; strings.TrimSpace(rest) != ""; {
		m := exprToken.FindStringSubmatchIndex(rest)
		if m == nil {
			return nil, fmt.Errorf("unexpected %q", strings.TrimSpace(rest))
		}
		p.tokens = append(p.tokens, rest[m[2]:m[3]])
		rest = rest[m[1]:]
	}
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(exprError)
			if !ok {
				panic(r)
			}
			out, err = nil, e.err
		}
	}()
	out = p.ternary()
	if p.i < len(p.tokens) {
		return nil, fmt.Errorf("unexpected %q", p.tokens[p.i])
	}
	return out, nil
}

func (p *exprParser) fail(format string, args ...any) { panic(exprError{fmt.Errorf(format, args...)}) }

func (p *exprParser) take(ts ...string) string {
	if p.i < len(p.tokens) && slices.Contains(ts, p.tokens[p.i]) {
		p.i++
		return p.tokens[p.i-1]
	}
	return ""
}

func (p *exprParser) num(v any) float64 {
	n, ok := v.(float64)
	if !ok {
		p.fail("%v is not a number", v)
	}
	return n
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	}
	return true
}

func (p *exprParser) ternary() any {
	v := p.or()
	if p.take("if") != "" {
		c := p.or()
		if p.take("else") == "" {
			p.fail("missing else")
		}
		other := p.ternary()
		if truthy(c) {
			return v
		}
		return other
	}
	return v
}

func (p *exprParser) or() any {
	v := p.and()
	for p.take("||", "or") != "" {
		r := p.and()
		if !truthy(v) {
			v = r
		}
	}
	return v
}

func (p *exprParser) and() any {
	v := p.cmp()
	for p.take("&&", "and") != "" {
		r := p.cmp()
		if truthy(v) {
			v = r
		}
	}
	return v
}

func (p *exprParser) cmp() any {
	v := p.sum()
	for op := p.take("==", "!=", "<", "<=", ">", ">="); op != ""; op = p.take("==", "!=", "<", "<=", ">", ">=") {
		r := p.sum()
		switch op {
		case "==", "!=":
			v = equalValues(v, r) == (op == "==")
		default:
			a, b := p.num(v), p.num(r)
			v = map[string]bool{"<": a < b, "<=": a <= b, ">": a > b, ">=": a >= b}[op]
		}
	}
	return v
}

// equalValues is == of two values of the same JSON type (a number and a string are never equal).
func equalValues(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case float64, string, bool:
		return a == b
	case []any:
		y, ok := b.([]any)
		return ok && fmt.Sprint(x) == fmt.Sprint(y)
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func (p *exprParser) sum() any {
	v := p.term()
	for op := p.take("+", "-"); op != ""; op = p.take("+", "-") {
		r := p.num(p.term())
		if op == "+" {
			v = p.num(v) + r
		} else {
			v = p.num(v) - r
		}
	}
	return v
}

func (p *exprParser) term() any {
	v := p.unary()
	for op := p.take("*", "/"); op != ""; op = p.take("*", "/") {
		r := p.num(p.unary())
		if op == "*" {
			v = p.num(v) * r
		} else if r == 0 {
			p.fail("division by zero")
		} else {
			v = p.num(v) / r
		}
	}
	return v
}

func (p *exprParser) unary() any {
	if p.take("!", "not") != "" {
		return !truthy(p.unary())
	}
	if p.take("-") != "" {
		return -p.num(p.unary())
	}
	v := p.atom()
	if p.take("**") != "" {
		b, e := p.num(v), p.num(p.unary())
		out := 1.0
		for range int(e) { // ponytail: whole exponents only (none of the formulas uses another)
			out *= b
		}
		v = out
	}
	return v
}

var exprName = regexp.MustCompile(`^[A-Za-z_]\w*(\.[A-Za-z_]\w*)?$`)

func (p *exprParser) atom() any {
	if p.i >= len(p.tokens) {
		p.fail("unexpected end")
	}
	t := p.tokens[p.i]
	p.i++
	switch {
	case t == "(":
		v := p.ternary()
		if p.take(")") == "" {
			p.fail("missing )")
		}
		return v
	case t[0] == '\'' || t[0] == '"':
		return t[1 : len(t)-1]
	case t[0] >= '0' && t[0] <= '9':
		n, _ := strconv.ParseFloat(t, 64)
		return n
	}
	if v, ok := exprWords[t]; ok {
		return v
	}
	if !exprName.MatchString(t) {
		p.fail("unexpected %q", t)
	}
	if p.take("(") != "" {
		var args []any
		for p.take(")") == "" {
			args = append(args, p.ternary())
			p.take(",")
		}
		return p.call(t, args)
	}
	v, err := p.get(t)
	if err != nil {
		panic(exprError{err})
	}
	if n, ok := v.(int64); ok { // a resolved int param
		return float64(n)
	}
	return v
}

func (p *exprParser) call(name string, args []any) any {
	switch name {
	case "len":
		if len(args) != 1 {
			p.fail("len takes one value")
		}
		if args[0] == nil {
			return 0.0
		}
		items, ok := args[0].([]any)
		if !ok {
			p.fail("len of %v", args[0])
		}
		return float64(len(items))
	case "min", "max":
		if len(args) == 0 {
			p.fail("%s of nothing", name)
		}
		out := p.num(args[0])
		for _, a := range args[1:] {
			if n := p.num(a); name == "min" && n < out || name == "max" && n > out {
				out = n
			}
		}
		return out
	case "gcd":
		if len(args) != 2 {
			p.fail("gcd takes two values")
		}
		a, b := p.num(args[0]), p.num(args[1])
		if a != float64(int64(a)) || b != float64(int64(b)) {
			p.fail("gcd takes whole numbers")
		}
		x, y := int64(a), int64(b)
		if x < 0 {
			x = -x
		}
		if y < 0 {
			y = -y
		}
		for y != 0 {
			x, y = y, x%y
		}
		return float64(x)
	}
	p.fail("unknown function %s", name)
	return nil
}

// exprText writes a value into a rule's text: a whole number without its point.
func exprText(v any) string {
	if n, ok := v.(float64); ok {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	if v == nil {
		return "null"
	}
	return fmt.Sprint(v)
}

var ruleHole = regexp.MustCompile(`\{([^{}]+)\}`)

// ruleText is the rule's error when its expression holds, "" when it does not (or fails).
func ruleText(rule registry.Rule, get func(string) (any, error)) string {
	v, err := evalExpr(rule.When, get)
	if err != nil || !truthy(v) {
		return ""
	}
	failed := false
	out := ruleHole.ReplaceAllStringFunc(rule.Error, func(hole string) string {
		v, err := evalExpr(hole[1:len(hole)-1], get)
		failed = failed || err != nil
		return exprText(v)
	})
	if failed {
		return ""
	}
	return out
}

// ruleErrors is every rule a filled graph breaks (check.py rule_errors): node id order, param order, then items.
func ruleErrors(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) []string {
	var out []string
	getter := func(id string, item map[string]any) func(string) (any, error) {
		return func(ref string) (any, error) {
			a, b, dotted := strings.Cut(ref, ".")
			scope := nodes[id].Params
			if dotted {
				src := a
				if wire, ok := nodes[id].In[a]; ok {
					src, _, _ = strings.Cut(wire, ".")
				}
				node, ok := nodes[src]
				if !ok {
					return nil, errNoRef
				}
				scope, a = node.Params, b
			} else if item != nil {
				scope = item
			}
			v, ok := scope[a]
			if !ok {
				return nil, errNoRef
			}
			return v, nil
		}
	}
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[id]
		if node.Bypass {
			continue
		}
		for _, param := range schemas[node.Kind].Params {
			for _, rule := range param.Rules {
				if text := ruleText(rule, getter(id, nil)); text != "" {
					out = append(out, id+": "+text)
				}
			}
			items, _ := node.Params[param.Name].([]any)
			if param.Type != "rows" || len(items) == 0 {
				continue
			}
			fields := itemFields(param)
			for i, raw := range items {
				given, _ := raw.(map[string]any)
				item := map[string]any{}
				for _, f := range fields {
					item[f.Name] = f.Default
				}
				maps.Copy(item, given)
				for _, f := range fields {
					for _, rule := range f.Rules {
						if text := ruleText(rule, getter(id, item)); text != "" {
							out = append(out, fmt.Sprintf("%s.%s[%d].%s: %s", id, param.Name, i+1, f.Name, text))
						}
					}
				}
			}
		}
	}
	return out
}

// itemFields are a rows param's item params (named by their field), by name.
func itemFields(param registry.Param) []registry.Param {
	var out []registry.Param
	for _, name := range slices.Sorted(maps.Keys(param.Item)) {
		var f registry.Param
		if json.Unmarshal(param.Item[name], &f) == nil && f.Type != "" {
			f.Name = name
			out = append(out, f)
		}
	}
	return out
}

// runPeak is a run's own planned peak in GB (check.py run_peak): the largest of its nodes' peak_gb formulas over the
// node's params, mp and frames; false when none has one or a formula names anything else.
func runPeak(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) (float64, bool) {
	sizes := map[string]any{"mp": 0.0, "frames": 0.0}
	for _, node := range nodes {
		w, wok := number(node.Params["width"])
		h, hok := number(node.Params["height"])
		if wok && hok && w*h/1e6 > sizes["mp"].(float64) {
			sizes["mp"] = w * h / 1e6
		}
		if f, ok := number(node.Params["frames"]); ok && f > sizes["frames"].(float64) {
			sizes["frames"] = f
		}
	}
	peak, found := 0.0, false
	for _, node := range nodes {
		formula := schemas[node.Kind].PeakGB
		if formula == "" {
			continue
		}
		v, err := evalExpr(formula, func(ref string) (any, error) {
			if s, ok := sizes[ref]; ok {
				return s, nil
			}
			if v, ok := node.Params[ref]; ok && !strings.Contains(ref, ".") {
				return v, nil
			}
			return nil, errNoRef
		})
		n, ok := v.(float64)
		if err != nil || !ok {
			return 0, false
		}
		if !found || n > peak {
			peak, found = n, true
		}
	}
	return peak, found
}
