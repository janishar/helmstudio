// Package helmcss is helm-css as files the daemon can serve: the four layers,
// helm.css and helm.min.css concatenating them, tokens.json, and the Plex
// fonts (docs/design/04-packages.md §3, 03-design-system.md §2a and §5).
//
// helm-css itself is text and depends on nothing. This Go file only embeds it
// and builds the three derived files; `make css` writes them, and a test fails
// when the committed copies differ from what Build produces.
package helmcss

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/janishar/helmstudio/internal/css"
)

// Version is helm-css's own version. Tokens are additive within a major
// (04 §9).
const Version = "1.0.0-rc.1"

// Files is everything served under /sdk/v1/ for helm-css.
//
//go:embed helm-tokens.css helm-base.css helm-layout.css helm-components.css helm.css helm.min.css tokens.json fonts
var Files embed.FS

// Layers are the source files, in cascade order.
var Layers = []string{"helm-tokens.css", "helm-base.css", "helm-layout.css", "helm-components.css"}

// Derived are the files Build writes.
var Derived = []string{"helm.css", "helm.min.css", "tokens.json"}

// Tokens is helm-tokens.css read as data: the themed tokens with one value per
// theme, the tokens that do not change with theme, and the values that
// prefers-reduced-motion replaces.
type Tokens struct {
	Name          string                       `json:"name"`
	Version       string                       `json:"version"`
	Themes        map[string]map[string]string `json:"themes"`
	Tokens        map[string]string            `json:"tokens"`
	ReducedMotion map[string]string            `json:"reduced_motion"`
}

// Build produces helm.css, helm.min.css and tokens.json from the layers in
// fsys.
func Build(fsys fs.FS) (map[string][]byte, error) {
	var all bytes.Buffer
	all.WriteString("/* helm-css " + Version + " — helm-tokens.css, helm-base.css, helm-layout.css and helm-components.css concatenated. Built by `make css`; do not edit. */\n")
	var tokenSrc string
	for _, name := range Layers {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		if name == "helm-tokens.css" {
			tokenSrc = string(b)
		}
		all.WriteString("\n")
		all.Write(b)
	}
	toks, err := ParseTokens(tokenSrc)
	if err != nil {
		return nil, err
	}
	js, err := json.MarshalIndent(toks, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"helm.css":     all.Bytes(),
		"helm.min.css": []byte(Minify(all.String()) + "\n"),
		"tokens.json":  append(js, '\n'),
	}, nil
}

const (
	rootSelector       = ":where(:root)"
	lightMediaPrelude  = "@media (prefers-color-scheme: light)"
	lightMediaSelector = ":where(:root:not([data-theme]))"
	lightSelector      = `:where(:root[data-theme="light"])`
	motionPrelude      = "@media (prefers-reduced-motion: reduce)"
)

// ParseTokens reads helm-tokens.css. A token is themed when the light blocks
// redefine it; the two light blocks (system preference, explicit choice)
// must agree exactly, and every light token must have a dark default.
func ParseTokens(src string) (Tokens, error) {
	dark, light, mediaLight, motion := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	for _, r := range css.Parse(src) {
		switch {
		case r.Prelude == rootSelector:
			collect(dark, r.Decls)
		case r.Prelude == lightSelector:
			collect(light, r.Decls)
		case r.Prelude == lightMediaPrelude:
			for _, in := range r.Rules {
				if in.Prelude != lightMediaSelector {
					return Tokens{}, fmt.Errorf("helm-tokens.css line %d: %s holds %q, want only %s", in.Line, lightMediaPrelude, in.Prelude, lightMediaSelector)
				}
				collect(mediaLight, in.Decls)
			}
		case r.Prelude == motionPrelude:
			for _, in := range r.Rules {
				collect(motion, in.Decls)
			}
		default:
			return Tokens{}, fmt.Errorf("helm-tokens.css line %d: unexpected rule %q; the file holds only the root, light and reduced-motion blocks", r.Line, r.Prelude)
		}
	}
	if !maps.Equal(light, mediaLight) {
		return Tokens{}, fmt.Errorf("helm-tokens.css: the prefers-color-scheme light block and the data-theme=\"light\" block differ; they must be identical")
	}
	out := Tokens{Name: "helm-css", Version: Version, Themes: map[string]map[string]string{"dark": {}, "light": light}, Tokens: map[string]string{}, ReducedMotion: motion}
	for k, v := range dark {
		if _, themed := light[k]; themed {
			out.Themes["dark"][k] = v
		} else {
			out.Tokens[k] = v
		}
	}
	for _, k := range slices.Sorted(maps.Keys(light)) {
		if _, ok := dark[k]; !ok {
			return Tokens{}, fmt.Errorf("helm-tokens.css: %s has a light value and no dark default", k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(motion)) {
		if _, ok := out.Tokens[k]; !ok {
			return Tokens{}, fmt.Errorf("helm-tokens.css: reduced motion replaces %s, which is not a theme-independent token", k)
		}
	}
	return out, nil
}

// collect keeps custom properties; color-scheme and the like are not tokens.
func collect(into map[string]string, decls []css.Decl) {
	for _, d := range decls {
		if strings.HasPrefix(d.Property, "--") {
			into[d.Property] = d.Value
		}
	}
}

// Names lists every token name.
func (t Tokens) Names() []string {
	names := slices.Collect(maps.Keys(t.Tokens))
	names = append(names, slices.Collect(maps.Keys(t.Themes["dark"]))...)
	slices.Sort(names)
	return names
}

// Minify removes comments and the whitespace CSS does not need. Strings and
// the insides of parentheses keep their spacing, apart from runs collapsing
// to one space.
func Minify(src string) string {
	var b strings.Builder
	var quote byte
	space := false
	last := byte(0)
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				b.WriteByte(src[i])
			} else if c == quote {
				quote = 0
			}
			last = c
			continue
		}
		if c == '/' && i+1 < len(src) && src[i+1] == '*' {
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				break
			}
			i += end + 3
			continue
		}
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' || c == '\f' {
			space = true
			continue
		}
		if space && b.Len() > 0 && !strings.ContainsRune("{};,", rune(last)) && !strings.ContainsRune("{};,", rune(c)) {
			b.WriteByte(' ')
		}
		space = false
		if c == '}' && last == ';' {
			s := b.String()
			b.Reset()
			b.WriteString(s[:len(s)-1])
		}
		if c == '"' || c == '\'' {
			quote = c
		}
		b.WriteByte(c)
		last = c
	}
	return b.String()
}
