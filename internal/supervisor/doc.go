// Package supervisor turns a studio manifest into a running process group and
// keeps it honest: dependency-ordered start with health gating, teardown in
// reverse by process group, crash handling, re-adoption after a daemon restart,
// the one-heavy-group rule, and per-process logs
// (docs/design/01-prd.md §7, docs/design/02-data-model.md §4 and §7).
//
// Three rules shape everything here and are worth knowing before reading it:
//
//   - Every process is started through platform.StartInGroup, so it leads its
//     own process group, and every signal goes to that group, never the pid.
//   - A main process is never restarted. Restart exists only on the exit path
//     and only for sidecars and workers; a health probe can change
//     health_state and nothing else once a process is running.
//   - Nothing is signalled after a daemon restart unless pid, process start
//     time and process group all still match what was recorded at spawn.
package supervisor
