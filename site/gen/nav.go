package gen

import "fmt"

// The documentation's order (docs/agents/milestones/10-docs-and-site.md,
// tasks 3–7). It is written down rather than derived from file names, because
// the order a newcomer should read in is a decision, and a test fails when a
// page exists that the navigation does not reach. A link's words are the
// page's own title, so the two cannot disagree, and the build fails on a link
// to a page that does not exist.

// NavItem is one link in the documentation's sidebar.
type NavItem struct {
	Title string
	URL   string
}

// NavSection is a heading in the sidebar and the pages under it.
type NavSection struct {
	Title string
	Items []NavItem
}

// studiosInOrder is the launch studios, in the order the site shows them.
var studiosInOrder = []string{"h3-studio", "ltx-studio", "iris-studio", "auk-studio"}

func navigation(groups []apiGroup) []NavSection {
	nav := []NavSection{
		{Title: "Start", Items: items("/docs/", "/docs/quickstart/")},
		{Title: "Concepts", Items: items(
			"/docs/concepts/manifest/",
			"/docs/concepts/process-groups/",
			"/docs/concepts/weights/",
			"/docs/concepts/capabilities/",
			"/docs/concepts/providers/",
			"/docs/concepts/independent-repositories/",
		)},
		{Title: "Guides", Items: items(
			"/docs/guides/wrap-a-repository/",
			"/docs/guides/record-with-provenance/",
			"/docs/guides/sessions/",
			"/docs/guides/timeline/",
			"/docs/guides/theming/",
			"/docs/guides/develop-in-isolation/",
		)},
		{Title: "Publishing", Items: items("/docs/publishing/")},
	}
	manifests := NavSection{Title: "Launch manifests, annotated"}
	for _, id := range studiosInOrder {
		manifests.Items = append(manifests.Items, NavItem{URL: "/docs/manifests/" + id + "/"})
	}
	nav = append(nav, manifests)

	ref := NavSection{Title: "Reference", Items: items("/docs/reference/manifest/", "/docs/reference/cli/", "/docs/reference/api/")}
	for _, g := range groups {
		ref.Items = append(ref.Items, NavItem{URL: "/docs/reference/api/" + g.Name + "/"})
	}
	ref.Items = append(ref.Items, NavItem{URL: "/docs/reference/api/types/"})
	return append(nav, ref)
}

func items(urls ...string) []NavItem {
	out := make([]NavItem, len(urls))
	for i, u := range urls {
		out[i] = NavItem{URL: u}
	}
	return out
}

// titled gives every link its page's title.
func titled(nav []NavSection, pages []*Page) ([]NavSection, error) {
	titles := make(map[string]string, len(pages))
	for _, p := range pages {
		titles[p.URL] = p.Title
	}
	out := make([]NavSection, len(nav))
	for i, sec := range nav {
		out[i] = NavSection{Title: sec.Title}
		for _, it := range sec.Items {
			t, ok := titles[it.URL]
			if !ok {
				return nil, fmt.Errorf("the navigation links to %s, and there is no page there", it.URL)
			}
			out[i].Items = append(out[i].Items, NavItem{Title: t, URL: it.URL})
		}
	}
	return out, nil
}
