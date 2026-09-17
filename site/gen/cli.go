package gen

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The CLI reference, generated from `helm`'s own usage (M10 defaults).
//
// Only the commands that exist are documented, in the words helm prints. The
// design names more — init, test, doctor, adopt — and none is built; the page
// says so rather than documenting a command a reader would type and not find.

func cliReference(o Options) (*Page, error) {
	helm := o.Helm
	if helm == "" {
		dir, err := os.MkdirTemp("", "helm-site-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		helm = filepath.Join(dir, "helm")
		build := exec.Command("go", "build", "-o", helm, "./cmd/helm")
		build.Dir = o.Root
		if out, err := build.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("building helm: %v\n%s", err, out)
		}
	}
	usage := func(args ...string) (string, error) {
		cmd := exec.Command(helm, args...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		_ = cmd.Run() // usage exits non-zero; the text is what matters
		text := strings.TrimSpace(out.String())
		if text == "" {
			return "", fmt.Errorf("helm %s printed nothing", strings.Join(args, " "))
		}
		return text, nil
	}
	top, err := usage()
	if err != nil {
		return nil, err
	}
	validate, err := usage("validate", "-h")
	if err != nil {
		return nil, err
	}
	dev, err := usage("dev", "-h")
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString(`<h1 class="helm-title">CLI reference</h1>`)
	b.WriteString(`<p class="helm-body">What <code>helm</code> prints about itself, verbatim. Install it as <a class="helm-link" href="https://github.com/janishar/helmstudio/blob/main/docs/releasing.md#installing-helm">Installing helm</a> says, or build it from a clone with <code>go build -o bin/helm ./cmd/helm</code>; <code>helm --version</code> says which one you have.</p>`)
	for _, c := range []struct{ title, text string }{
		{"helm", top}, {"helm validate", validate}, {"helm dev", dev},
	} {
		fmt.Fprintf(&b, `<h2 id="%s">%s</h2><pre class="site-quote"><code>%s</code></pre>`,
			strings.ReplaceAll(c.title, " ", "-"), html.EscapeString(c.title), html.EscapeString(c.text))
	}
	b.WriteString(`<h2 id="not-built">Not built</h2><p class="helm-body">The design names four more commands, and none of them exists yet: <code>helm studio init</code> (scaffold a studio), <code>helm test</code> (the smoke harness certification runs), <code>helm doctor</code> (which provider a studio would get, and why) and <code>helm adopt</code>. They belong to no milestone yet. <code>helm dev</code>'s design also names <code>--fixtures</code> and <code>--fail</code>, which are not built either.</p>`)
	return &Page{
		URL: "/docs/reference/cli/", Title: "CLI reference", Layout: "docs", Body: template.HTML(b.String()),
		GeneratedFrom: "helm's own usage", Description: "The helm command, in its own words.",
	}, nil
}
