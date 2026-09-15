// Package themelint checks a studio's stylesheets against helm-css: no colour
// literal outside the vendored token file, and no font family but Plex through
// its tokens (docs/design/03-design-system.md §5, 07-platform-services.md §6;
// the definitions are docs/decisions.md M6 Q17).
//
// It reads .css files, and <style> elements and style="" attributes in .html
// files, anywhere under a directory except vendor/helm/. It looks only at
// declaration values, never at selectors, so "#app" or ".red" is not a finding.
// Colours set from JavaScript are not found.
package themelint

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/janishar/helmstudio/internal/css"
)

// Kinds of finding.
const (
	KindColour = "colour"
	KindFont   = "font"
)

// Finding is one literal or family, where its declaration value starts.
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Kind     string `json:"kind"`
	Property string `json:"property"`
	Text     string `json:"text"`
	Message  string `json:"message"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d:%d: %s", f.File, f.Line, f.Col, f.Message)
}

// Report is everything found under a directory.
type Report struct {
	Files    int       `json:"files"`
	Findings []Finding `json:"findings"`
}

// VendorDir is where a studio keeps its copy of helm-css (04-packages.md §8).
const VendorDir = "vendor/helm"

// Dir lints every stylesheet under root. Paths in findings are relative to
// root, with forward slashes. Symlinks are not followed.
func Dir(root string) (Report, error) {
	var rep Report
	info, err := os.Stat(root)
	if err != nil {
		return rep, err
	}
	if !info.IsDir() {
		return rep, fmt.Errorf("%s is not a directory", root)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || rel == VendorDir || strings.HasSuffix(rel, "/"+VendorDir)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".css" && ext != ".html" && ext != ".htm" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rep.Files++
		if ext == ".css" {
			rep.Findings = append(rep.Findings, CSS(rel, string(b))...)
		} else {
			rep.Findings = append(rep.Findings, HTML(rel, string(b))...)
		}
		return nil
	})
	if errors.Is(err, fs.SkipAll) {
		err = nil
	}
	return rep, err
}

// CSS lints one stylesheet.
func CSS(file, src string) []Finding {
	var out []Finding
	css.Walk(css.Parse(src), func(r css.Rule, parents []css.Rule) {
		if strings.HasPrefix(r.Prelude, "@font-face") {
			return // a family is declared there, not used
		}
		out = append(out, decls(file, r.Decls)...)
	})
	return out
}

var (
	styleElement   = regexp.MustCompile(`(?is)<style\b[^>]*>(.*?)</style\s*>`)
	styleAttribute = regexp.MustCompile(`(?i)\sstyle\s*=\s*("([^"]*)"|'([^']*)')`)
)

// HTML lints the <style> elements and style attributes of one page.
func HTML(file, src string) []Finding {
	var out []Finding
	for _, m := range styleElement.FindAllStringSubmatchIndex(src, -1) {
		body := src[m[2]:m[3]]
		line, col := position(src, m[2])
		for _, f := range CSS(file, body) {
			// positions inside the element are relative to its first byte
			if f.Line == 1 {
				f.Col += col - 1
			}
			f.Line += line - 1
			out = append(out, f)
		}
	}
	for _, m := range styleAttribute.FindAllStringSubmatchIndex(src, -1) {
		start, end := m[4], m[5]
		if start < 0 {
			start, end = m[6], m[7]
		}
		line, col := position(src, start)
		out = append(out, decls(file, css.ParseDeclarations(src[start:end], line, col))...)
	}
	slices.SortStableFunc(out, func(a, b Finding) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Col - b.Col
	})
	return out
}

func position(src string, off int) (int, int) {
	before := src[:off]
	return strings.Count(before, "\n") + 1, off - strings.LastIndexByte(before, '\n')
}

func decls(file string, ds []css.Decl) []Finding {
	var out []Finding
	for _, d := range ds {
		for _, lit := range colours(d.Property, d.Value) {
			line, col := d.Line, d.Col+lit.offset
			if nl := strings.LastIndexByte(d.Value[:lit.offset], '\n'); nl >= 0 {
				line += strings.Count(d.Value[:lit.offset], "\n")
				col = lit.offset - nl
			}
			out = append(out, Finding{File: file, Line: line, Col: col, Kind: KindColour, Property: d.Property, Text: lit.text,
				Message: fmt.Sprintf("colour literal %q in %s; use a helm token (docs/design/03-design-system.md §2a)", lit.text, d.Property)})
		}
		if family, bad := fontFamily(d.Property, d.Value); bad {
			out = append(out, Finding{File: file, Line: d.Line, Col: d.Col, Kind: KindFont, Property: d.Property, Text: family,
				Message: fmt.Sprintf("font family %q in %s; use var(--helm-font-sans) or var(--helm-font-mono)", family, d.Property)})
		}
	}
	return out
}

type literal struct {
	text   string
	offset int
}

var colourFunctions = map[string]bool{
	"rgb": true, "rgba": true, "hsl": true, "hsla": true, "hwb": true,
	"lab": true, "lch": true, "oklab": true, "oklch": true, "color": true,
}

// Properties whose identifiers are names, not colours: a keyframe called
// "tan" or a grid area called "red" is not a literal.
var namesNotColours = map[string]bool{
	"font": true, "font-family": true, "grid-area": true, "grid-template-areas": true, "grid-row": true, "grid-column": true,
	"animation": true, "animation-name": true, "transition": true, "transition-property": true, "will-change": true,
	"counter-reset": true, "counter-increment": true, "counter-set": true, "list-style-type": true, "content": true,
	"view-transition-name": true, "container-name": true, "anchor-name": true,
}

// colours finds every colour literal in a declaration value.
func colours(prop, value string) []literal {
	var out []literal
	checkNames := !namesNotColours[prop]
	for i := 0; i < len(value); {
		c := value[i]
		switch {
		case c == '"' || c == '\'':
			i = skipString(value, i)
		case c == '#':
			j := i + 1
			for j < len(value) && isHex(value[j]) {
				j++
			}
			n := j - i - 1
			if (n == 3 || n == 4 || n == 6 || n == 8) && (j == len(value) || !isIdent(value[j])) {
				out = append(out, literal{value[i:j], i})
			}
			i = max(j, i+1)
		case isDigit(c) || (c == '.' && i+1 < len(value) && isDigit(value[i+1])):
			j := i
			for j < len(value) && (isIdent(value[j]) || value[j] == '.') {
				j++
			}
			i = j
		case isIdentStart(c) || (c == '-' && i+1 < len(value) && (isIdentStart(value[i+1]) || value[i+1] == '-')):
			j := i
			for j < len(value) && isIdent(value[j]) {
				j++
			}
			word := value[i:j]
			lower := strings.ToLower(word)
			switch {
			case j < len(value) && value[j] == '(':
				if lower == "url" {
					j = skipParens(value, j)
				} else if colourFunctions[lower] {
					end := skipParens(value, j)
					out = append(out, literal{value[i:end], i})
					j = end
				}
			case strings.HasPrefix(word, "--"):
				// a custom property's name, e.g. inside var()
			case checkNames && namedColours[lower]:
				out = append(out, literal{word, i})
			}
			i = max(j, i+1)
		default:
			i++
		}
	}
	return out
}

var fontKeywords = map[string]bool{"inherit": true, "initial": true, "unset": true, "revert": true, "revert-layer": true}

var helmFamily = regexp.MustCompile(`^var\(\s*--helm-font-(sans|mono)\s*(,[^)]*)?\)$`)
var helmType = regexp.MustCompile(`^var\(\s*--helm-type-[a-z-]+\s*\)$`)
var fontSize = regexp.MustCompile(`^(\d*\.?\d+)(px|em|rem|%|pt|pc|ex|ch|vw|vh|vmin|vmax|cap|ic|lh|rlh|q|mm|cm|in)(/\S+)?$|^(xx-small|x-small|small|medium|large|x-large|xx-large|xxx-large|smaller|larger)(/\S+)?$`)

// fontFamily reports a family that is not a helm font token. font-family must
// be the token; the font shorthand must be a type token, a keyword, or end in
// the family token after its size.
func fontFamily(prop, value string) (string, bool) {
	v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "!important"))
	lower := strings.ToLower(v)
	switch prop {
	case "font-family":
		if fontKeywords[lower] || helmFamily.MatchString(v) {
			return "", false
		}
		return v, true
	case "font":
		if fontKeywords[lower] || helmType.MatchString(v) {
			return "", false
		}
		fields := strings.Fields(v)
		for i, f := range fields {
			if fontSize.MatchString(strings.ToLower(f)) {
				family := strings.TrimSpace(strings.Join(fields[i+1:], " "))
				if family == "" || helmFamily.MatchString(family) {
					return "", false
				}
				return family, true
			}
		}
		// caption, menu and the other system fonts, or something unreadable
		return v, true
	}
	return "", false
}

func skipString(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' {
			j++
		} else if s[j] == q {
			return j + 1
		}
	}
	return len(s)
}

func skipParens(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '"', '\'':
			j = skipString(s, j) - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(s)
}

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') || c >= 0x80 }
func isIdent(c byte) bool      { return isIdentStart(c) || isDigit(c) || c == '-' }

// namedColours are CSS Color 4's named colours. transparent and currentColor
// are keywords a token-only stylesheet still needs, so they are not here.
var namedColours = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(`aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue
blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk crimson cyan darkblue
darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki darkmagenta darkolivegreen darkorange darkorchid
darkred darksalmon darkseagreen darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink
deepskyblue dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite gold
goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki lavender lavenderblush
lawngreen lemonchiffon lightblue lightcoral lightcyan lightgoldenrodyellow lightgray lightgreen lightgrey
lightpink lightsalmon lightseagreen lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime
limegreen linen magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen
mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream mistyrose moccasin
navajowhite navy oldlace olive olivedrab orange orangered orchid palegoldenrod palegreen paleturquoise
palevioletred papayawhip peachpuff peru pink plum powderblue purple rebeccapurple red rosybrown royalblue
saddlebrown salmon sandybrown seagreen seashell sienna silver skyblue slateblue slategray slategrey snow
springgreen steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow yellowgreen
canvas canvastext linktext visitedtext activetext buttonface buttontext buttonborder field fieldtext highlight
highlighttext selecteditem selecteditemtext mark marktext graytext accentcolor accentcolortext`) {
		m[n] = true
	}
	return m
}()
