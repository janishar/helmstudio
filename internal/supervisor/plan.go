package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
)

// Studio is one manifest the daemon knows, with the file it came from.
type Studio struct {
	Manifest *manifest.Manifest
	File     string
}

// planned is one process of a launch, resolved: ports assigned, placeholders
// substituted, paths absolute.
type planned struct {
	spec manifest.Process
	role string
	argv []string
	env  []string
	cwd  string
	port int // 0 when the process declares no port
	// healthArgv is the substituted exec probe, when the probe is exec.
	healthArgv []string
}

// Studio root, data and weight paths until M3 records installations
// (docs/decisions.md, "M2 supervision"): a manifest's local_path, else
// <data>/studios/<id>/src, which is where install will clone to (R7).
func studioRoot(dirs *platform.Dirs, m *manifest.Manifest) string {
	if m.LocalPath != "" {
		return filepath.Clean(m.LocalPath)
	}
	return filepath.Join(dirs.Data(), "studios", m.ID, "src")
}

// studioData is {data}: the studio's persistent data root, outside its
// checkout so a reinstall cannot take the user's work with it
// (docs/design/08-h3-dry-run.md, change 2).
func studioData(dirs *platform.Dirs, m *manifest.Manifest) string {
	return filepath.Join(dirs.Data(), "studios", m.ID, "data")
}

// launchOrder returns the autostart processes in dependency order. Among
// processes whose dependencies are equally satisfied, declaration order wins,
// so the order is stable. The manifest has already been validated acyclic.
func launchOrder(m *manifest.Manifest) ([]manifest.Process, error) {
	procs := m.EffectiveProcesses()
	index := make(map[string]int, len(procs))
	for i, p := range procs {
		index[p.Name] = i
	}
	var order []manifest.Process
	done := make(map[string]bool, len(procs))
	for len(order) < len(procs) {
		progressed := false
		for _, p := range procs {
			if done[p.Name] {
				continue
			}
			ready := true
			for _, d := range p.DependsOn {
				if !done[d] {
					ready = false
					break
				}
			}
			if ready {
				done[p.Name] = true
				order = append(order, p)
				progressed = true
				break // restart the scan so declaration order decides ties
			}
		}
		if !progressed {
			return nil, fmt.Errorf("%s: processes have a dependency cycle; run helm validate", m.ID)
		}
	}
	started := make(map[string]bool, len(order))
	var out []manifest.Process
	for _, p := range order {
		if !p.EffectiveAutostart() {
			continue // started on demand, which M2 does not offer yet
		}
		for _, d := range p.DependsOn {
			if !started[d] {
				return nil, &Error{Kind: KindNotLaunchable, Message: fmt.Sprintf(
					"%s: process %q depends on %q, which has autostart: false; starting a process on demand is not available yet", m.ID, p.Name, d)}
			}
		}
		started[p.Name] = true
		out = append(out, p)
	}
	return out, nil
}

var placeholderRe = regexp.MustCompile(`\{([a-zA-Z0-9_.]+)\}`)

// substitute replaces every {name} in tmpl. quote is applied to each value
// before it is inserted (a shell-quoting function for commands, nil for
// environment values). An unknown placeholder is an error, matching
// helm validate's known-placeholder rule: a typo must not reach argv.
func substitute(tmpl string, values map[string]string, refuse map[string]string, quote func(string) (string, error)) (string, error) {
	var firstErr error
	out := placeholderRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		if firstErr != nil {
			return match
		}
		name := match[1 : len(match)-1]
		if why, ok := refuse[name]; ok {
			firstErr = errors.New(why)
			return match
		}
		v, ok := values[name]
		if !ok {
			firstErr = fmt.Errorf("{%s} is not a substitution this studio can resolve", name)
			return match
		}
		if quote != nil {
			q, err := quote(v)
			if err != nil {
				firstErr = err
				return match
			}
			return q
		}
		return v
	})
	return out, firstErr
}

// resolvePlan resolves every autostart process of a studio. assigned carries
// ports already bound to a process name (re-adopted processes keep theirs);
// every other port comes from alloc, and on error every port this call leased
// is released again.
func resolvePlan(dirs *platform.Dirs, st Studio, assigned map[string]int, alloc *allocator) (_ []*planned, err error) {
	m := st.Manifest
	notLaunchable := func(format string, args ...any) error {
		return &Error{Kind: KindNotLaunchable, Message: fmt.Sprintf("%s: ", m.ID) + fmt.Sprintf(format, args...)}
	}

	if m.LocalPath != "" && !filepath.IsAbs(m.LocalPath) {
		return nil, notLaunchable("local_path %q is not an absolute path", m.LocalPath)
	}
	root := studioRoot(dirs, m)
	if fi, statErr := os.Stat(root); statErr != nil || !fi.IsDir() {
		return nil, notLaunchable("no checkout at %s; install lands in a later milestone, so clone and build the studio there by hand, or set local_path in its manifest", root)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, notLaunchable("resolving the checkout at %s: %v", root, err)
	}
	order, err := launchOrder(m)
	if err != nil {
		return nil, err
	}

	var leased []int
	defer func() {
		if err != nil {
			for _, p := range leased {
				alloc.release(p)
			}
		}
	}()
	ports := make(map[string]int)
	for _, p := range order {
		if port, ok := assigned[p.Name]; ok && port != 0 {
			ports[p.Name] = port
			continue
		}
		if p.Port == nil {
			continue
		}
		port, err := alloc.allocate(fmt.Sprintf("%s (process %q)", m.ID, p.Name), p.Port.Prefer, p.Port.Fixed)
		if err != nil {
			return nil, err
		}
		leased = append(leased, port)
		ports[p.Name] = port
	}

	values := map[string]string{
		"root": root,
		"data": studioData(dirs, m),
	}
	for name, port := range ports {
		values["ports."+name] = strconv.Itoa(port)
	}
	for _, w := range m.Weights {
		path := filepath.Join(dirs.Models(), w.Dest)
		if real, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
			if fi, statErr := os.Stat(real); statErr == nil && fi.IsDir() {
				values["models."+w.Name] = real
			}
		}
	}
	refuse := map[string]string{
		"venv":            "{venv} needs a per-studio Python environment, which lands with the Python studios milestone",
		"models.selected": "{models.selected} needs a recorded weight selection, which lands with the library milestone",
	}
	for _, w := range m.Weights {
		if _, ok := values["models."+w.Name]; !ok {
			refuse["models."+w.Name] = fmt.Sprintf("weight %q is not at %s; weights download lands in a later milestone, so place or symlink the model directory there by hand",
				w.Name, filepath.Join(dirs.Models(), w.Dest))
		}
	}

	var out []*planned
	for _, p := range order {
		pv := make(map[string]string, len(values)+1)
		for k, v := range values {
			pv[k] = v
		}
		procRefuse := copyMap(refuse)
		if port, ok := ports[p.Name]; ok {
			pv["port"] = strconv.Itoa(port)
		} else {
			procRefuse["port"] = fmt.Sprintf("process %q uses {port} but declares no port block", p.Name)
		}
		for _, sib := range m.EffectiveProcesses() {
			if _, ok := ports[sib.Name]; ok {
				continue
			}
			if contains(order, sib.Name) {
				procRefuse["ports."+sib.Name] = fmt.Sprintf("{ports.%s}: process %q declares no port block", sib.Name, sib.Name)
			} else {
				procRefuse["ports."+sib.Name] = fmt.Sprintf("{ports.%s}: process %q has autostart: false, so it has no port when the group starts", sib.Name, sib.Name)
			}
		}
		shell := p.EffectiveShell()
		quote := func(v string) (string, error) { return platform.QuoteForShell(shell, v) }

		cmd, err := substitute(p.Cmd, pv, procRefuse, quote)
		if err != nil {
			return nil, notLaunchable("process %q: %v", p.Name, err)
		}
		argv, err := platform.ShellArgv(shell, cmd)
		if err != nil {
			return nil, notLaunchable("process %q: %v", p.Name, err)
		}

		cwd := root
		if p.Cwd != "" {
			cwd = filepath.Join(root, filepath.FromSlash(p.Cwd))
		}
		if fi, statErr := os.Stat(cwd); statErr != nil || !fi.IsDir() {
			return nil, notLaunchable("process %q: working directory %s does not exist", p.Name, cwd)
		}
		// helm validate checks cwd lexically; a symlink inside the checkout
		// can still point outside it, so check the resolved path too.
		if realCwd, evalErr := filepath.EvalSymlinks(cwd); evalErr != nil || !within(realRoot, realCwd) {
			return nil, notLaunchable("process %q: working directory %s resolves outside the checkout %s", p.Name, cwd, realRoot)
		}

		env := studioEnv(os.Environ(), filepath.Join(dirs.Data(), "studios", m.ID, "no-hf-token"))
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v, err := substitute(p.Env[k], pv, procRefuse, nil)
			if err != nil {
				return nil, notLaunchable("process %q, env %s: %v", p.Name, k, err)
			}
			env = append(env, k+"="+v)
		}

		pl := &planned{spec: p, role: p.EffectiveRole(), argv: argv, env: env, cwd: cwd, port: ports[p.Name]}
		if h := p.Health; h != nil {
			if (h.TCP || h.Path != "") && pl.port == 0 {
				return nil, notLaunchable("process %q: its health probe needs a port, and it declares no port block", p.Name)
			}
			if h.Exec != "" {
				probe, err := substitute(h.Exec, pv, procRefuse, quote)
				if err != nil {
					return nil, notLaunchable("process %q, health.exec: %v", p.Name, err)
				}
				if pl.healthArgv, err = platform.ShellArgv(shell, probe); err != nil {
					return nil, notLaunchable("process %q, health.exec: %v", p.Name, err)
				}
			}
		}
		out = append(out, pl)
	}
	return out, nil
}

// passedEnv names the daemon environment variables a studio process
// inherits; every other variable, a token or cloud key among them, is not
// passed (docs/design/05-sdk-and-custom-studios.md §10: "a restricted
// environment, no inherited secrets"). LC_* matches every locale category.
// Proxy settings pass so downloads work behind a proxy; SSH_AUTH_SOCK does
// not, because it would let any studio use the user's SSH keys.
var passedEnv = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"TMPDIR": true, "LANG": true, "TERM": true, "TZ": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true, "all_proxy": true,
}

// studioEnv filters an environment down to passedEnv, then closes the one
// ambient secret HOME would otherwise still hand over: the Hugging Face token
// `hf auth login` writes under ~/.cache/huggingface. HF_TOKEN_PATH points at a
// per-studio path that is never created, and implicit tokens are disabled;
// the Hugging Face download cache is left where it is. A manifest's own env,
// added after this, can override both — that is the manifest declaring it.
// Other secret files under HOME (~/.netrc, ~/.aws) remain readable; 05 §10
// says full sandboxing is not realistic, and the decision log records it.
func studioEnv(environ []string, noTokenPath string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if passedEnv[name] || strings.HasPrefix(name, "LC_") {
			out = append(out, kv)
		}
	}
	return append(out, "HF_TOKEN_PATH="+noTokenPath, "HF_HUB_DISABLE_IMPLICIT_TOKEN=1")
}

// within reports whether path is root or inside it. Both must be cleaned,
// resolved paths.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func contains(procs []manifest.Process, name string) bool {
	for _, p := range procs {
		if p.Name == name {
			return true
		}
	}
	return false
}

// heavyMember reports whether the studio's group holds a heavy process.
func heavyMember(m *manifest.Manifest) bool {
	for _, p := range m.EffectiveProcesses() {
		if p.Heavy {
			return true
		}
	}
	return false
}

// describe renders an argv for a person: the shell script when it is one.
func describe(argv []string) string {
	if len(argv) == 3 && argv[1] == "-c" {
		return argv[2]
	}
	return strings.Join(argv, " ")
}
