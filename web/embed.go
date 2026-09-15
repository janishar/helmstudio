// Package web holds the plain shelf: the minimum page that lists studios,
// starts and stops them, and shows their logs. It is deliberately unstyled —
// the design tokens arrive with M6, and anything invented here would become a
// migration then.
package web

import "embed"

// Shelf is the shelf's static files, served at the daemon's root.
//
//go:embed index.html shelf.js
var Shelf embed.FS
