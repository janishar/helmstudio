// Package web holds the launcher: the shell, its screens and the generated
// launcher client they reach the daemon through (docs/decisions.md M6 Q2, Q12;
// M7 Q17). helm-css and the components are served separately, under /sdk/v1/,
// and schema/manifest.json — which the editor's form is generated from — at
// /schema/manifest.json.
package web

import "embed"

// Shelf is the launcher's static files, served at the daemon's root.
//
//go:embed index.html launcher.css launcher.js app.js ui.js morph.js catalogue.js studio.js processes.js models.js settings.js switch.js approval.js sections.js schemaform.js editor.js importer.js add.js
var Shelf embed.FS
