// Package css is a small CSS reader: rules, at-rules and declarations with the
// line and column each value starts at. It is not a validating parser. It is
// enough for what helmstudio needs to read in a stylesheet — helm-css's own
// tokens and selectors, and a studio's colour literals and font families
// (docs/decisions.md M6 Q5, Q17) — and it never evaluates anything.
package css

import "strings"

// Rule is a style rule or an at-rule. Prelude is the selector list, or the
// at-rule with its condition ("@media (max-width: 900px)"). A block holds
// declarations, nested rules, or both; a statement at-rule ("@import …;") has
// neither and Block false.
type Rule struct {
	Prelude string
	Line    int
	Block   bool
	Decls   []Decl
	Rules   []Rule
}

// Decl is one "property: value" pair. Line and Col are where Value starts,
// 1-based. Important is true when the value ended in !important, which is
// kept in Value.
type Decl struct {
	Property  string
	Value     string
	Line, Col int
	Important bool
}

// Parse reads a stylesheet.
func Parse(src string) []Rule {
	p := &parser{src: blankComments(src)}
	rules, _ := p.items(false)
	return rules
}

// ParseDeclarations reads a declaration list, as in an HTML style attribute.
// line and col are where the list starts in its file.
func ParseDeclarations(src string, line, col int) []Decl {
	p := &parser{src: blankComments(src), line0: line - 1, col0: col - 1}
	_, decls := p.items(false)
	return decls
}

// Selectors splits a rule's prelude on top-level commas.
func Selectors(prelude string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(prelude); i++ {
		c := prelude[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(prelude[start:i]))
			start = i + 1
		}
	}
	if s := strings.TrimSpace(prelude[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// Walk calls f for every rule, depth first, with the at-rules it sits inside.
func Walk(rules []Rule, f func(r Rule, parents []Rule)) {
	var walk func([]Rule, []Rule)
	walk = func(rs []Rule, parents []Rule) {
		for _, r := range rs {
			f(r, parents)
			walk(r.Rules, append(parents, r))
		}
	}
	walk(rules, nil)
}

// blankComments replaces every comment with spaces, keeping newlines, so
// offsets and line numbers still point into the original text.
func blankComments(src string) string {
	b := []byte(src)
	var quote byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote || c == '\n' {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			j := i
			for ; j < len(b) && !(b[j] == '*' && j+1 < len(b) && b[j+1] == '/' && j > i+1); j++ {
				if b[j] != '\n' {
					b[j] = ' '
				}
			}
			for k := j; k < len(b) && k < j+2; k++ {
				b[k] = ' '
			}
			i = j + 1
		}
	}
	return string(b)
}

type parser struct {
	src         string
	pos         int
	line0, col0 int // added to positions, for a fragment inside a file
}

// items reads until the end of input, or the "}" closing the current block
// when inBlock, and returns the rules and declarations found.
func (p *parser) items(inBlock bool) ([]Rule, []Decl) {
	var rules []Rule
	var decls []Decl
	for {
		start := p.pos
		end, stop := p.scan()
		text := p.src[start:end]
		switch stop {
		case '{':
			r := Rule{Prelude: strings.TrimSpace(text), Line: p.lineOf(start + leadingSpace(text)), Block: true}
			p.pos = end + 1
			r.Rules, r.Decls = p.items(true)
			rules = append(rules, r)
		case ';', '}', 0:
			if t := strings.TrimSpace(text); t != "" {
				if strings.HasPrefix(t, "@") {
					rules = append(rules, Rule{Prelude: t, Line: p.lineOf(start + leadingSpace(text))})
				} else if d, ok := p.decl(text, start); ok {
					decls = append(decls, d)
				}
			}
			if stop == 0 {
				p.pos = len(p.src)
				return rules, decls
			}
			p.pos = end + 1
			if stop == '}' && inBlock {
				return rules, decls
			}
		}
	}
}

// scan finds the next top-level "{", ";" or "}" from pos, skipping strings
// and anything inside parentheses (a url(), a :where(), a data: URI).
func (p *parser) scan() (int, byte) {
	depth := 0
	var quote byte
	for i := p.pos; i < len(p.src); i++ {
		c := p.src[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0 && (c == '{' || c == ';' || c == '}'):
			return i, c
		}
	}
	return len(p.src), 0
}

func (p *parser) decl(text string, start int) (Decl, bool) {
	colon := strings.IndexByte(text, ':')
	if colon < 0 {
		return Decl{}, false
	}
	prop := strings.TrimSpace(text[:colon])
	if prop == "" {
		return Decl{}, false
	}
	raw := text[colon+1:]
	valueStart := start + colon + 1 + leadingSpace(raw)
	value := strings.TrimSpace(raw)
	important := false
	if i := strings.LastIndex(strings.ToLower(value), "!"); i >= 0 && strings.TrimSpace(strings.ToLower(value[i+1:])) == "important" {
		important = true
	}
	line, col := p.lineCol(valueStart)
	if !strings.HasPrefix(prop, "--") { // custom property names are case-sensitive
		prop = strings.ToLower(prop)
	}
	return Decl{Property: prop, Value: value, Line: line, Col: col, Important: important}, true
}

func leadingSpace(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t\r\n\f"))
}

func (p *parser) lineOf(off int) int {
	l, _ := p.lineCol(off)
	return l
}

func (p *parser) lineCol(off int) (int, int) {
	if off > len(p.src) {
		off = len(p.src)
	}
	before := p.src[:off]
	line := strings.Count(before, "\n")
	col := off - (strings.LastIndexByte(before, '\n') + 1)
	if line == 0 {
		col += p.col0
	}
	return line + 1 + p.line0, col + 1
}
