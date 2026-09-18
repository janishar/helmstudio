package gen

import (
	"fmt"
	"regexp"
	"strings"
)

// Diagrams (docs/decisions.md M10 Q7, Q9, amended 2026-09-18).
//
// A diagram is an SVG file under site/diagrams, inlined into a page by
// `@diagram <name> caption: <takeaway>`. It is inlined rather than linked so
// that it follows the theme: its colours come from class names this site's
// stylesheet defines in tokens, which is the same rule a studio's stylesheet
// is held to, and a linked <img> could not see them.
//
// Inlining markup into a page is exactly what the rest of this package
// refuses, so a diagram is checked before it is let through, by check below.
// Nothing here trusts the file for being in the repository: the point of the
// check is that a reviewer reading a diff of an SVG cannot reasonably be
// expected to notice a colour literal or an onload= among the path data.
//
// The theme lint cannot do this job. `helm validate -theme` reads .css, .html
// and .htm and skips .svg (internal/themelint/themelint.go), and even for a
// file it reads it inspects only <style> elements and style="" attributes, so
// fill="#fff" on a <rect> is invisible to it. A diagram's colours are checked
// here or nowhere.

var (
	diagramLine = regexp.MustCompile(`^@diagram\s+([A-Za-z0-9_-]+)\s+caption:\s*(.+?)\s*$`)

	svgTitle = regexp.MustCompile(`(?s)<title\b[^>]*>(.*?)</title>`)
	svgDesc  = regexp.MustCompile(`(?s)<desc\b[^>]*>(.*?)</desc>`)

	// What a diagram may not carry. Each is a rule a reviewer would have to
	// hold in their head otherwise.
	banned = []struct {
		what string
		re   *regexp.Regexp
		why  string
	}{
		{"a script", regexp.MustCompile(`(?i)<\s*script\b`), "a diagram is a picture, and the site runs no JavaScript but the theme toggle"},
		{"an event handler", regexp.MustCompile(`(?i)\bon[a-z]+\s*=`), "same reason: nothing in a diagram executes"},
		{"embedded HTML", regexp.MustCompile(`(?i)<\s*foreignObject\b`), "it would carry markup this package never checked"},
		{"a stylesheet", regexp.MustCompile(`(?i)<\s*style\b`), "a diagram is styled by class names in site.css, so the theme reaches it and the theme lint reads it"},
		{"a link", regexp.MustCompile(`(?i)\b(?:xlink:)?(?:href|src)\s*=`), "a figure links to nothing; it also keeps the link checker out of path data"},
		{"an inline style", regexp.MustCompile(`(?i)\bstyle\s*=`), "same reason as a stylesheet: class names only"},
		{"a hex colour", regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`), "colour comes from a token, through a class name"},
		{"a colour function", regexp.MustCompile(`(?i)\b(?:rgba?|hsla?|color|oklch|lab)\s*\(`), "colour comes from a token, through a class name"},
	}

	// Checked in code rather than in the table above, because the rule is
	// "not a colour" and RE2 has no negative lookahead: fill="none" is a
	// shape, and currentColor and a gradient reference inherit rather than
	// state a colour.
	presentationAttr = regexp.MustCompile(`(?i)\b(fill|stroke|stop-color|flood-color|lighting-color)\s*=\s*"([^"]*)"`)
)

// colourless is true for a presentation value that states no colour of its own.
func colourless(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "none", "currentcolor", "inherit", "transparent", "":
		return true
	}
	return strings.HasPrefix(v, "url(#")
}

// checkDiagram returns what is wrong with an SVG, or nil. Everything it
// refuses is refused at build time, so a diagram cannot reach a page unread.
func checkDiagram(name, body string) error {
	if !strings.Contains(body, "<svg") {
		return fmt.Errorf("it is not an SVG")
	}
	if !strings.Contains(body, "viewBox") {
		return fmt.Errorf("it has no viewBox, so it cannot scale to the width it is given")
	}
	for _, b := range banned {
		if m := b.re.FindString(body); m != "" {
			return fmt.Errorf("it carries %s (%q): %s", b.what, strings.TrimSpace(m), b.why)
		}
	}
	for _, m := range presentationAttr.FindAllStringSubmatch(body, -1) {
		if !colourless(m[2]) {
			return fmt.Errorf("it carries a presentation colour (%q): colour comes from a token, through a class name; use class= and let site.css say it", m[0])
		}
	}
	t := svgTitle.FindStringSubmatch(body)
	if t == nil || strings.TrimSpace(t[1]) == "" {
		return fmt.Errorf("it has no <title>, which is the name a screen reader says")
	}
	d := svgDesc.FindStringSubmatch(body)
	if d == nil || strings.TrimSpace(d[1]) == "" {
		return fmt.Errorf("it has no <desc>, which is what a screen reader reads in place of the picture")
	}
	return nil
}
