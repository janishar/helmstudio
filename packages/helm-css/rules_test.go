package helmcss

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/janishar/helmstudio/internal/css"
	"github.com/janishar/helmstudio/internal/themelint"
)

// specificity counts ids, classes (with attributes and pseudo-classes) and
// type selectors (with pseudo-elements), as CSS Selectors 4 does; :where()
// counts nothing, :is(), :not() and :has() count their argument.
func specificity(sel string) (ids, classes, types int) {
	for i := 0; i < len(sel); {
		c := sel[i]
		switch {
		case c == '#':
			ids++
			i = identEnd(sel, i+1)
		case c == '.':
			classes++
			i = identEnd(sel, i+1)
		case c == '[':
			classes++
			i = strings.IndexByte(sel[i:], ']') + i + 1
		case c == ':' && i+1 < len(sel) && sel[i+1] == ':':
			types++
			i = identEnd(sel, i+2)
			if i < len(sel) && sel[i] == '(' {
				i = closeParen(sel, i)
			}
		case c == ':':
			name := sel[i+1 : identEnd(sel, i+1)]
			i = identEnd(sel, i+1)
			if i < len(sel) && sel[i] == '(' {
				end := closeParen(sel, i)
				arg := sel[i+1 : end-1]
				i = end
				switch name {
				case "where":
				case "is", "not", "has":
					a, b, c := specificity(arg)
					ids, classes, types = ids+a, classes+b, types+c
				default:
					classes++
				}
			} else {
				classes++
			}
		case isNameStart(c):
			types++
			i = identEnd(sel, i)
		default:
			i++
		}
	}
	return ids, classes, types
}

func isNameStart(c byte) bool { return c == '*' || c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') }

func identEnd(s string, i int) int {
	for i < len(s) && (s[i] == '-' || s[i] == '_' || (s[i]|0x20 >= 'a' && s[i]|0x20 <= 'z') || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i < len(s) && s[i] == '*' {
		i++
	}
	return i
}

func closeParen(s string, i int) int {
	depth := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}

func TestSpecificityCountsAsCSSDoes(t *testing.T) {
	for sel, want := range map[string][3]int{
		".helm-btn":                         {0, 1, 0},
		".helm-btn:where(:hover)":           {0, 1, 0},
		":where(.helm-table) :where(th)":    {0, 0, 0},
		".helm-step::after":                 {0, 1, 1},
		".a.b":                              {0, 2, 0},
		".a:hover":                          {0, 2, 0},
		".a:not(.b)":                        {0, 2, 0},
		"#id":                               {1, 0, 0},
		"div .a":                            {0, 1, 1},
		`:where(:root[data-theme="light"])`: {0, 0, 0},
		"*":                                 {0, 0, 1},
	} {
		a, b, c := specificity(sel)
		if [3]int{a, b, c} != want {
			t.Errorf("specificity(%q) = %v, want %v", sel, [3]int{a, b, c}, want)
		}
	}
}

var classNames = regexp.MustCompile(`\.(-?[_a-zA-Z][_a-zA-Z0-9-]*)`)

// 03 §5 and R50: every class is prefixed helm-, nothing is !important, and a
// selector is at most one class, so a studio overrides with one rule. helm-css
// must also never assume a component's markup (04 §11 rule 1).
func TestLayersKeepTheOverrideRules(t *testing.T) {
	for _, name := range Layers {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		css.Walk(css.Parse(string(src)), func(r css.Rule, parents []css.Rule) {
			for _, d := range r.Decls {
				if d.Important {
					t.Errorf("%s:%d: %s is !important", name, d.Line, d.Property)
				}
			}
			if strings.HasPrefix(r.Prelude, "@keyframes") {
				if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(r.Prelude, "@keyframes")), "helm-") {
					t.Errorf("%s:%d: %q is not prefixed helm-", name, r.Line, r.Prelude)
				}
				return
			}
			if strings.HasPrefix(r.Prelude, "@") || !r.Block {
				return
			}
			if len(parents) > 0 && strings.HasPrefix(parents[len(parents)-1].Prelude, "@keyframes") {
				return
			}
			for _, sel := range css.Selectors(r.Prelude) {
				ids, classes, types := specificity(sel)
				if ids > 0 || classes > 1 || (classes == 1 && types > 0 && !strings.Contains(sel, "::")) {
					t.Errorf("%s:%d: %q has specificity (%d,%d,%d); at most one class, or elements only inside :where()", name, r.Line, sel, ids, classes, types)
				}
				for _, m := range classNames.FindAllStringSubmatch(sel, -1) {
					if !strings.HasPrefix(m[1], "helm-") {
						t.Errorf("%s:%d: class %q in %q is not prefixed helm-", name, r.Line, m[1], sel)
					}
				}
				if regexp.MustCompile(`(^|[\s(,>+~])helm-[a-z]`).MatchString(sel) {
					t.Errorf("%s:%d: %q selects a helm- element; helm-css must not assume a component's markup", name, r.Line, sel)
				}
			}
		})
	}
}

// The three layers after the tokens follow the rule helm validate -theme
// applies to studios: every colour and family comes from a token.
func TestLayersUseOnlyTokens(t *testing.T) {
	for _, name := range Layers[1:] {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range themelint.CSS(name, string(src)) {
			t.Error(f)
		}
	}
}

// Every var(--helm-…) a layer reads is a token (or one of its private
// --_helm- properties); a typo would silently fall back to nothing.
func TestLayersReadOnlyTokensThatExist(t *testing.T) {
	tok, _ := readTokens(t)
	known := map[string]bool{}
	for _, n := range tok.Names() {
		known[n] = true
	}
	ref := regexp.MustCompile(`var\(\s*(--helm-[a-z0-9-]+)`)
	for _, name := range Layers {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ref.FindAllStringSubmatch(string(src), -1) {
			if !known[m[1]] {
				t.Errorf("%s reads %s, which is not a token", name, m[1])
			}
		}
	}
}
