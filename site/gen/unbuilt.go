package gen

import (
	"fmt"
	"html"
	"strings"
)

// What the design names and nobody has built.
//
// One list, in one place. It was on the documentation home, in the CLI
// reference and in the guide to developing in isolation, and the day a
// command ships every copy has to be found — a copy that is missed leaves the
// site claiming less than the software does, which is the same failure as
// claiming more, pointing the other way.
//
// Each entry says what the thing would do, from the design rather than from
// imagination, and cites where that is written.

type unbuiltThing struct {
	Name string // in code voice: it is a command, a flag, or a screen
	What string // what the design says it would do
}

// unbuiltCommands is `helm`'s side of it. `helm` itself dispatches validate,
// dev and upgrade and nothing else (cmd/helm/main.go).
var unbuiltCommands = []unbuiltThing{
	{"helm studio init", "scaffold a studio, which the quickstart's example stands in for"},
	{"helm test", "run the smoke harness that certification needs (03 §12)"},
	{"helm doctor", "score the criteria against a working tree, and say which provider a studio would get and why (01 §R6c, 05 §4)"},
	{"helm adopt", "import a studio's standalone data into the daemon, hardlinking blobs and replaying rows (01 §R6b)"},
	{"helm dev --fixtures", "serve canned data in place of a provider"},
	{"helm dev --fail", "make an operation fail on purpose, to see what a studio does"},
}

// unbuiltScreens is the launcher's side. web/app.js names both and says why.
var unbuiltScreens = []unbuiltThing{
	{"the launcher's Gallery screen", "show every studio's items in one place"},
	{"the launcher's Timeline screen", "open a sequence in an editor, which is why `:open` answers 501"},
}

// notBuiltHTML is the callout a page gets from `@notbuilt`, and the CLI
// reference's own section, so the two cannot disagree.
func notBuiltHTML(things []unbuiltThing) string {
	var b strings.Builder
	b.WriteString(`<div class="site-notbuilt"><p class="helm-section-label">Not built yet</p><ul>`)
	for _, t := range things {
		fmt.Fprintf(&b, `<li><span class="helm-mono">%s</span> — %s</li>`,
			html.EscapeString(t.Name), inlineCode(html.EscapeString(t.What)))
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// inlineCode turns `x` into code, the one piece of Markdown these lines use.
func inlineCode(s string) string {
	for {
		i := strings.Index(s, "`")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+1:], "`")
		if j < 0 {
			break
		}
		s = s[:i] + "<code>" + s[i+1:i+1+j] + "</code>" + s[i+2+j:]
	}
	return s
}
