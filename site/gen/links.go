package gen

import (
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// CheckLinks reads the built site the way a browser would follow it: every
// href and src under the base path must reach a file in the output, and every
// #fragment an element with that id. It reads the written HTML rather than the
// pages' own lists, so a link a template adds is checked as surely as one a
// page's Markdown does (M10 Q8).
func CheckLinks(out, base string, res *Result) []string {
	attr := regexp.MustCompile(`(?:href|src)="([^"]*)"`)
	ids := map[string]map[string]bool{}
	idAttr := regexp.MustCompile(`\sid="([^"]*)"`)

	var files []string
	_ = filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".html") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	idsOf := func(file string) map[string]bool {
		if m, ok := ids[file]; ok {
			return m
		}
		m := map[string]bool{}
		if b, err := os.ReadFile(file); err == nil {
			for _, mm := range idAttr.FindAllStringSubmatch(string(b), -1) {
				m[html.UnescapeString(mm[1])] = true
			}
		}
		ids[file] = m
		return m
	}

	var problems []string
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		rel, _ := filepath.Rel(out, file)
		for _, m := range attr.FindAllStringSubmatch(string(b), -1) {
			link := html.UnescapeString(m[1])
			if link == "" || strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "mailto:") {
				continue
			}
			target, fragment, _ := strings.Cut(link, "#")
			var dest string
			switch {
			case target == "":
				dest = file
			case strings.HasPrefix(target, base):
				dest = filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(target, base)))
				if strings.HasSuffix(target, "/") {
					dest = filepath.Join(dest, "index.html")
				}
			default:
				problems = append(problems, fmt.Sprintf("%s: %q is neither under the base path %s nor external", rel, link, base))
				continue
			}
			if info, err := os.Stat(dest); err != nil || info.IsDir() {
				problems = append(problems, fmt.Sprintf("%s: %q leads nowhere", rel, link))
				continue
			}
			if fragment != "" && strings.HasSuffix(dest, ".html") && !idsOf(dest)[fragment] {
				problems = append(problems, fmt.Sprintf("%s: %q names #%s, which that page does not have", rel, link, fragment))
			}
		}
	}
	return problems
}
