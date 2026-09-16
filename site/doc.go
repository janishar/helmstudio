// Package site builds helmstudio's documentation and the site at helmstudio.in
// (docs/agents/milestones/10-docs-and-site.md).
//
// It is a module of its own so that the Markdown renderer it uses never enters
// the daemon's module graph (docs/decisions.md M10 Q7): the daemon's binary
// links nothing from here.
package site
