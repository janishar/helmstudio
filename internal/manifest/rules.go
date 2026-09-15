package manifest

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// procRef pairs a process with the JSON pointer it came from, so an error can
// point at "/processes/2" or "/run" rather than just a bare name — two
// processes can share a name only once validation already failed elsewhere,
// but the pointer is unambiguous either way.
type procRef struct {
	Proc    Process
	Pointer string
}

// effProcesses returns the supervised process group regardless of whether the
// manifest used processes[] or the run sugar.
//
// The m.Run branch is unreachable today and has no test: schema/manifest.json
// requires `processes` unconditionally, so a run-only manifest fails schema
// validation before semantic rules run (contradiction 1 in
// docs/agents/reports/00-contracts.md). Cover it through Validate() with a
// run-only fixture once the schema is fixed.
func effProcesses(m *Manifest) []procRef {
	if m.Run != nil {
		return []procRef{{Proc: *m.Run, Pointer: "/run"}}
	}
	out := make([]procRef, len(m.Processes))
	for i, p := range m.Processes {
		out[i] = procRef{Proc: p, Pointer: fmt.Sprintf("/processes/%d", i)}
	}
	return out
}

// placeholderRe matches the substitution syntax the schema documents for
// process.cmd: {port}, {ports.<name>}, {models.<name>}, {models.selected},
// {root}, {data}, {venv}. It is deliberately permissive here — anything
// brace-delimited — so ruleKnownPlaceholders can reject what doesn't belong
// to that closed set, rather than this regex silently skipping it.
var placeholderRe = regexp.MustCompile(`\{([a-zA-Z0-9_.]+)\}`)

func placeholders(cmd string) []string {
	matches := placeholderRe.FindAllStringSubmatch(cmd, -1)
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m[1]
	}
	return out
}

// validateSemantic runs the seven rules JSON Schema cannot express, plus a
// few more in the same category — a manifest bug a JSON Schema validator
// structurally cannot see, not a new policy. It assumes m already passed
// schema validation.
func validateSemantic(file string, m *Manifest) []Error {
	var errs []Error
	procs := effProcesses(m)

	names := make(map[string]bool, len(procs))
	for _, pr := range procs {
		if pr.Proc.Name != "" {
			names[pr.Proc.Name] = true
		}
	}

	errs = append(errs, ruleUniqueNames(file, procs, m.Weights)...)
	errs = append(errs, ruleDependsOnKnown(file, procs, names)...)
	errs = append(errs, ruleNoCycle(file, procs, names)...)
	errs = append(errs, ruleExactlyOneMain(file, procs)...)
	errs = append(errs, ruleAtMostOneHeavy(file, procs)...)
	errs = append(errs, ruleKnownPlaceholders(file, procs)...)
	errs = append(errs, ruleModelsSubstitution(file, m, procs)...)
	errs = append(errs, rulePortsSubstitution(file, procs, names)...)
	errs = append(errs, ruleCwdWithinRoot(file, m, procs)...)

	return errs
}

// Names must be unique within the studio: "Unique within the studio" is
// process.name's own schema description, but JSON Schema has no keyword for
// uniqueness across an array's own sibling values (uniqueItems compares
// whole items, not one field of each). Weight names have the same gap and
// the same consequence — rule 5 below resolves {models.<name>} against
// whichever same-named weight it finds first, silently, if two share a name.
func ruleUniqueNames(file string, procs []procRef, weights []Weight) []Error {
	var errs []Error

	seen := make(map[string]string, len(procs)) // name -> first pointer
	for _, pr := range procs {
		if pr.Proc.Name == "" {
			continue
		}
		if first, dup := seen[pr.Proc.Name]; dup {
			errs = append(errs, Error{
				File:     file,
				Pointer:  pr.Pointer + "/name",
				Rule:     "unique-names",
				Message:  fmt.Sprintf("process name %q is also used at %s", pr.Proc.Name, first),
				Expected: "a name unique among this manifest's processes",
			})
			continue
		}
		seen[pr.Proc.Name] = pr.Pointer
	}

	seenW := make(map[string]int, len(weights)) // name -> first index
	for i, w := range weights {
		if w.Name == "" {
			continue
		}
		if first, dup := seenW[w.Name]; dup {
			errs = append(errs, Error{
				File:     file,
				Pointer:  fmt.Sprintf("/weights/%d/name", i),
				Rule:     "unique-names",
				Message:  fmt.Sprintf("weight name %q is also used at /weights/%d", w.Name, first),
				Expected: "a name unique among this manifest's weights",
			})
			continue
		}
		seenW[w.Name] = i
	}

	return errs
}

// Rule 1: every name in a depends_on refers to a process declared in the
// same manifest.
func ruleDependsOnKnown(file string, procs []procRef, names map[string]bool) []Error {
	var errs []Error
	for _, pr := range procs {
		for i, dep := range pr.Proc.DependsOn {
			if !names[dep] {
				errs = append(errs, Error{
					File:     file,
					Pointer:  fmt.Sprintf("%s/depends_on/%d", pr.Pointer, i),
					Rule:     "depends_on-known",
					Message:  fmt.Sprintf("process %q depends_on %q, which is not declared in this manifest", pr.Proc.Name, dep),
					Expected: "a name matching a sibling process",
				})
			}
		}
	}
	return errs
}

// Rule 2: the dependency graph has no cycle, including a process depending
// on itself. Every visit pops its own stack frame and marks itself done on
// the way out — including the two paths that found a cycle — so that a
// second, disjoint cycle elsewhere in the manifest is explored with a clean
// stack rather than one still carrying frames from the first.
func ruleNoCycle(file string, procs []procRef, names map[string]bool) []Error {
	byName := make(map[string]procRef, len(procs))
	for _, pr := range procs {
		byName[pr.Proc.Name] = pr
	}

	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(procs))
	var errs []Error
	var stack []string

	var visit func(name string) bool // true if a cycle was found starting here
	visit = func(name string) bool {
		state[name] = visiting
		stack = append(stack, name)
		defer func() {
			state[name] = done
			stack = stack[:len(stack)-1]
		}()

		pr, ok := byName[name]
		if !ok {
			return false
		}
		for _, dep := range pr.Proc.DependsOn {
			if !names[dep] {
				continue // rule 1 already reports this; don't cascade
			}
			switch state[dep] {
			case unvisited:
				if visit(dep) {
					return true
				}
			case visiting:
				start := 0
				for i, s := range stack {
					if s == dep {
						start = i
						break
					}
				}
				cycle := append(append([]string{}, stack[start:]...), dep)
				errs = append(errs, Error{
					File:     file,
					Pointer:  pr.Pointer + "/depends_on",
					Rule:     "no-cycle",
					Message:  fmt.Sprintf("dependency cycle: %s", strings.Join(cycle, " -> ")),
					Expected: "a dependency graph with no cycle",
				})
				return true
			}
		}
		return false
	}

	for _, pr := range procs {
		if state[pr.Proc.Name] == unvisited {
			visit(pr.Proc.Name)
		}
	}
	return errs
}

// Rule 3: exactly one process has role: main. This is the milestone's own
// wording (docs/agents/milestones/00-contracts.md); docs/design/05 criterion
// 4 says "at least one main process" instead — a real tension between the
// two documents, raised rather than silently picked between in
// docs/agents/reports/00-contracts.md. This implements the milestone's text,
// which is the more specific instruction for what `helm validate` checks.
func ruleExactlyOneMain(file string, procs []procRef) []Error {
	var mains []procRef
	for _, pr := range procs {
		if pr.Proc.EffectiveRole() == "main" {
			mains = append(mains, pr)
		}
	}
	if len(mains) == 1 {
		return nil
	}
	if len(mains) == 0 {
		pointer := "/processes"
		if len(procs) == 1 {
			pointer = procs[0].Pointer // "/run" when that's the sugar in use
		}
		return []Error{{
			File:     file,
			Pointer:  pointer,
			Rule:     "exactly-one-main",
			Message:  "no process has role: main",
			Expected: "exactly one process with role main",
		}}
	}
	var pointers []string
	for _, pr := range mains {
		pointers = append(pointers, pr.Pointer)
	}
	return []Error{{
		File:     file,
		Pointer:  "/processes",
		Rule:     "exactly-one-main",
		Message:  fmt.Sprintf("%d processes have role: main (%s)", len(mains), strings.Join(pointers, ", ")),
		Expected: "exactly one process with role main",
	}}
}

// Rule 4: at most one process is heavy.
func ruleAtMostOneHeavy(file string, procs []procRef) []Error {
	var heavy []procRef
	for _, pr := range procs {
		if pr.Proc.Heavy {
			heavy = append(heavy, pr)
		}
	}
	if len(heavy) <= 1 {
		return nil
	}
	var pointers []string
	for _, pr := range heavy {
		pointers = append(pointers, pr.Pointer)
	}
	return []Error{{
		File:     file,
		Pointer:  "/processes",
		Rule:     "at-most-one-heavy",
		Message:  fmt.Sprintf("%d processes are heavy (%s)", len(heavy), strings.Join(pointers, ", ")),
		Expected: "at most one heavy process",
	}}
}

// knownPlaceholder reports whether ph is one of the substitutions
// process.cmd documents: {port}, {root}, {data}, {venv}, {ports.<name>},
// {models.<name>}, {models.selected}. Anything else — a typo like {prot}, a
// bare {models} with no name — is not a template variable this manifest
// format has, and silently leaving it in cmd means the literal text
// "{prot}" reaches argv at spawn time.
func knownPlaceholder(ph string) bool {
	switch ph {
	case "port", "root", "data", "venv":
		return true
	}
	if rest, ok := strings.CutPrefix(ph, "ports."); ok {
		return rest != ""
	}
	if rest, ok := strings.CutPrefix(ph, "models."); ok {
		return rest != ""
	}
	return false
}

func ruleKnownPlaceholders(file string, procs []procRef) []Error {
	var errs []Error
	for _, pr := range procs {
		for _, ph := range placeholders(pr.Proc.Cmd) {
			if !knownPlaceholder(ph) {
				errs = append(errs, Error{
					File:     file,
					Pointer:  pr.Pointer + "/cmd",
					Rule:     "known-placeholder",
					Message:  fmt.Sprintf("process %q references {%s}, which is not a substitution this schema defines", pr.Proc.Name, ph),
					Expected: "one of {port} {root} {data} {venv} {ports.<name>} {models.<name>} {models.selected}",
				})
			}
		}
	}
	return errs
}

// Rule 5, and its converse: every {models.x} substitution resolves to a
// declared weight, {models.selected} only when some weight is selectable,
// and a weight marked selectable but never referenced by {models.selected}
// in any command is itself an error.
func ruleModelsSubstitution(file string, m *Manifest, procs []procRef) []Error {
	var errs []Error

	weightNames := make(map[string]bool, len(m.Weights))
	anySelectable := false
	for _, w := range m.Weights {
		if w.Name != "" {
			weightNames[w.Name] = true
		}
		if w.Selectable {
			anySelectable = true
		}
	}

	selectedReferenced := false
	for _, pr := range procs {
		for _, ph := range placeholders(pr.Proc.Cmd) {
			rest, ok := strings.CutPrefix(ph, "models.")
			if !ok {
				continue
			}
			if rest == "selected" {
				selectedReferenced = true
				if !anySelectable {
					errs = append(errs, Error{
						File:     file,
						Pointer:  pr.Pointer + "/cmd",
						Rule:     "models-substitution",
						Message:  fmt.Sprintf("process %q references {models.selected}, but no weight is marked selectable", pr.Proc.Name),
						Expected: "at least one weights[] entry with selectable: true",
					})
				}
				continue
			}
			if !weightNames[rest] {
				errs = append(errs, Error{
					File:     file,
					Pointer:  pr.Pointer + "/cmd",
					Rule:     "models-substitution",
					Message:  fmt.Sprintf("process %q references {models.%s}, which is not a declared weight", pr.Proc.Name, rest),
					Expected: "a name matching a weights[] entry",
				})
			}
		}
	}

	if anySelectable && !selectedReferenced {
		for i, w := range m.Weights {
			if w.Selectable {
				errs = append(errs, Error{
					File:     file,
					Pointer:  fmt.Sprintf("/weights/%d/selectable", i),
					Rule:     "models-substitution",
					Message:  fmt.Sprintf("weight %q is selectable, but no process command references {models.selected}", w.Name),
					Expected: "a process cmd containing {models.selected}",
				})
			}
		}
	}

	return errs
}

// Rule 6: every {ports.x} refers to a sibling process in the same manifest.
//
// Whether that sibling must also appear in depends_on is a real question we
// do not answer here: 02-data-model.md §7 assigns a port only when a process
// moves queued -> starting, so a process that names a sibling's port without
// depending on it can be substituted a port that does not exist yet. Raised
// in docs/agents/reports/00-contracts.md rather than enforced — it changes
// what a manifest author must write, which is a call for whoever owns
// supervision (M2), not this validator.
func rulePortsSubstitution(file string, procs []procRef, names map[string]bool) []Error {
	var errs []Error
	for _, pr := range procs {
		for _, ph := range placeholders(pr.Proc.Cmd) {
			rest, ok := strings.CutPrefix(ph, "ports.")
			if !ok {
				continue
			}
			if !names[rest] {
				errs = append(errs, Error{
					File:     file,
					Pointer:  pr.Pointer + "/cmd",
					Rule:     "ports-substitution",
					Message:  fmt.Sprintf("process %q references {ports.%s}, which is not a declared process", pr.Proc.Name, rest),
					Expected: "a name matching a sibling process",
				})
			}
		}
	}
	return errs
}

// Rule 7: cwd does not escape the studio root — checked by resolving the
// path against a virtual root and cleaning it, not by looking for ".." in
// the string. A directory named "..cache" contains the substring ".." and
// must not be rejected by a naive check; "a/../.." must be, even though its
// own segments never repeat ".." outside a legal-looking pair. Covers
// processes[]/run, build[] and import — every cwd the schema declares as
// "relative to the studio root".
func ruleCwdWithinRoot(file string, m *Manifest, procs []procRef) []Error {
	const root = "/studio-root"
	check := func(pointer, cwd string) *Error {
		if cwd == "" {
			return nil
		}
		if path.IsAbs(cwd) {
			return &Error{
				File:     file,
				Pointer:  pointer,
				Rule:     "cwd-within-root",
				Message:  fmt.Sprintf("cwd %q is absolute", cwd),
				Expected: "a path relative to the studio root",
			}
		}
		resolved := path.Clean(path.Join(root, cwd))
		if resolved != root && !strings.HasPrefix(resolved, root+"/") {
			return &Error{
				File:     file,
				Pointer:  pointer,
				Rule:     "cwd-within-root",
				Message:  fmt.Sprintf("cwd %q escapes the studio root", cwd),
				Expected: "a path that stays within the studio root after resolution",
			}
		}
		return nil
	}

	var errs []Error
	for _, pr := range procs {
		if e := check(pr.Pointer+"/cwd", pr.Proc.Cwd); e != nil {
			errs = append(errs, *e)
		}
	}
	for i, b := range m.Build {
		if e := check(fmt.Sprintf("/build/%d/cwd", i), b.Cwd); e != nil {
			errs = append(errs, *e)
		}
	}
	if m.Import != nil {
		if e := check("/import/cwd", m.Import.Cwd); e != nil {
			errs = append(errs, *e)
		}
	}
	return errs
}
