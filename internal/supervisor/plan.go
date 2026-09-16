package supervisor

import (
	"context"
	"database/sql"
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

// launchPaths is what an installation contributes to a launch: the checkout
// it recorded, and the {models.<name>} values its weights resolve to.
type launchPaths struct {
	root string
	// models holds resolved real paths by placeholder ("models.fl2va");
	// modelRefusals says why any other declared weight cannot resolve.
	models        map[string]string
	modelRefusals map[string]string
	// data is {data}.
	data string
	// venv is {venv}, and pyEnv what the studio's members get besides
	// activation; both empty for a studio without python:.
	venv  string
	pyEnv []string
}

// launchable states are the install states a studio may launch from
// (docs/design/01-prd.md §5: ready, and update_available, which is still
// launchable).
var launchable = map[string]bool{"ready": true, "update_available": true}

// installed reads a studio's installation and resolves its weights. A studio
// with no installation, or one mid-install, failed or being removed, is not
// launchable, and the refusal says what to do.
func (s *Supervisor) installed(ctx context.Context, st Studio) (launchPaths, error) {
	m := st.Manifest
	var root, state string
	err := s.store.Reader().QueryRowContext(ctx, `SELECT root_path, install_state FROM installations WHERE studio_id = ?`, m.ID).Scan(&root, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return launchPaths{}, &Error{Kind: KindNotLaunchable, Message: fmt.Sprintf("%s is not installed; install it before launching", m.ID)}
	}
	if err != nil {
		return launchPaths{}, fmt.Errorf("%s: reading its installation: %w", m.ID, err)
	}
	if !launchable[state] {
		return launchPaths{}, &Error{Kind: KindNotLaunchable, Message: fmt.Sprintf("%s cannot launch while its install state is %s; finish or retry the install first", m.ID, state)}
	}
	models, refuse, err := s.weights.Launch(ctx, m.ID, m.Weights)
	if err != nil {
		return launchPaths{}, fmt.Errorf("%s: resolving its weights: %w", m.ID, err)
	}
	lp := launchPaths{root: root, models: models, modelRefusals: refuse, data: s.studioData(m)}
	if m.Python != nil {
		if lp.venv, lp.pyEnv, err = s.python(m); err != nil {
			return launchPaths{}, &Error{Kind: KindNotLaunchable, Message: fmt.Sprintf("%s cannot launch: %v", m.ID, err)}
		}
	}
	return lp, nil
}

// studioData is {data}: the studio's persistent data root, outside its
// checkout so a reinstall cannot take the user's work with it
// (docs/design/08, change 2).
func studioData(dirs *platform.Dirs, m *manifest.Manifest) string {
	return filepath.Join(dirs.Data(), "studios", m.ID, "data")
}

// studioData is {data} for this supervisor: Config.StudioData when set.
func (s *Supervisor) studioData(m *manifest.Manifest) string {
	if s.cfg.StudioData != nil {
		return s.cfg.StudioData(m)
	}
	return studioData(s.dirs, m)
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
func resolvePlan(dirs *platform.Dirs, st Studio, lp launchPaths, assigned map[string]int, alloc *allocator) (_ []*planned, err error) {
	m := st.Manifest
	notLaunchable := func(format string, args ...any) error {
		return &Error{Kind: KindNotLaunchable, Message: fmt.Sprintf("%s: ", m.ID) + fmt.Sprintf(format, args...)}
	}

	if m.LocalPath != "" && !filepath.IsAbs(m.LocalPath) {
		return nil, notLaunchable("local_path %q is not an absolute path", m.LocalPath)
	}
	root := lp.root
	if fi, statErr := os.Stat(root); statErr != nil || !fi.IsDir() {
		return nil, notLaunchable("its installation records a checkout at %s, which is not there; uninstall and install it again", root)
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
		"data": lp.data,
	}
	for name, port := range ports {
		values["ports."+name] = strconv.Itoa(port)
	}
	for k, v := range lp.models {
		values[k] = v
	}
	// {models.selected} resolves through the weights service now (M7 Q21); a
	// studio with selectable weights and no choice is refused there, with the
	// choices named.
	refuse := map[string]string{}
	if lp.venv != "" {
		values["venv"] = lp.venv
	} else {
		refuse["venv"] = "{venv} names a Python environment, and this studio declares no python block"
	}
	for k, why := range lp.modelRefusals {
		refuse[k] = why
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

		env := RestrictedEnv(dirs, m.ID, os.Environ())
		if lp.venv != "" {
			env = Activate(append(env, lp.pyEnv...), lp.venv)
		}
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			// HELM_* is the platform's (docs/decisions.md M4 defaults): a
			// manifest must not be able to point a studio at another API or
			// hand it a token.
			if strings.HasPrefix(k, "HELM_") {
				return nil, notLaunchable("process %q sets %s in env; HELM_* variables are set by helmstudio", p.Name, k)
			}
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

// RestrictedEnv is the environment everything helmstudio runs on a studio's
// behalf gets — its processes, and install's git and build steps: studioEnv
// applied to environ, with the studio's never-created token path.
func RestrictedEnv(dirs *platform.Dirs, studioID string, environ []string) []string {
	return studioEnv(environ, filepath.Join(dirs.Data(), "studios", studioID, "no-hf-token"))
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
