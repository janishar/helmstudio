// Package web holds the launcher: the shell, the five screens M6 builds, and
// the generated launcher client they reach the daemon through
// (docs/decisions.md M6 Q2, Q12). helm-css and the components are served
// separately, under /sdk/v1/.
package web

import "embed"

// Shelf is the launcher's static files, served at the daemon's root.
//
//go:embed index.html launcher.css launcher.js app.js ui.js catalogue.js studio.js processes.js models.js settings.js switch.js
var Shelf embed.FS
