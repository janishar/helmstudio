// Package platform holds every operating-system-specific decision helmstudio
// makes: where files live, where secrets live, and how a process claims a file
// exclusively.
//
// Each decision is an unexported implementation selected by build constraints
// (darwin, linux, and an explicit refusal everywhere else), behind the exported
// API in this package. Nothing outside this package may branch on the
// operating system; `make gate` fails on a runtime.GOOS reference anywhere
// else in the tree.
//
// Every path in the system comes from Dirs. Tests get an isolated tree from
// the platformtest package, never the user's.
package platform
