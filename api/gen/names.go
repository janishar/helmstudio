package main

import (
	"strings"
	"unicode"
)

var initialisms = map[string]string{
	"id": "ID", "url": "URL", "kv": "KV", "api": "API", "sha256": "SHA256", "etag": "ETag",
	"ns": "NS", "fps": "FPS", "ui": "UI", "json": "JSON", "http": "HTTP",
}

// words splits snake_case, kebab-case, dotted and camelCase names.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ' || r == '/':
			flush()
		case unicode.IsUpper(r) && i > 0 && unicode.IsLower(rs[i-1]):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// goName is an exported Go identifier.
func goName(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		if v, ok := initialisms[w]; ok {
			b.WriteString(v)
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// camel is a lowerCamel identifier for JavaScript.
func camel(s string) string {
	ws := words(s)
	for i := range ws {
		if i > 0 {
			ws[i] = strings.ToUpper(ws[i][:1]) + ws[i][1:]
		}
	}
	return strings.Join(ws, "")
}

// snake is a snake_case identifier for Python.
func snake(s string) string { return strings.Join(words(s), "_") }
