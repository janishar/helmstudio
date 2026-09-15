package supervisor

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
)

// A studio that declares python: gets a uv environment of its own
// (docs/design/01-prd.md R12). Where it lives, how its build steps and
// processes see it, and which uv settings apply are docs/decisions.md M5 Q4–Q6.
// Nothing here reads a studio's pyproject.toml or uv.lock: what to install
// is the manifest's build steps.

// VenvDir is where helmstudio keeps a studio's environment: beside its
// checkout and data, never inside a checkout, so an author's own .venv is
// never touched (Q4).
func VenvDir(dirs *platform.Dirs, studioID string) string {
	return filepath.Join(dirs.Data(), "studios", studioID, "venv")
}

// UVEnv is what every uv command helmstudio runs for an environment it owns
// is given (Q6): a managed interpreter only, installed under the data root
// (a purged interpreter would break every environment linked to it), one uv
// cache shared by studios under the cache root, and no user uv configuration,
// so what installs follows from the manifest alone.
func UVEnv(dirs *platform.Dirs) []string {
	return []string{
		"UV_PYTHON_PREFERENCE=only-managed",
		"UV_PYTHON_INSTALL_DIR=" + filepath.Join(dirs.Data(), "python"),
		"UV_CACHE_DIR=" + filepath.Join(dirs.Cache(), "uv"),
		"UV_NO_CONFIG=1",
	}
}

// Activate returns env with a Python environment active, as a shell's
// `source bin/activate` would leave it, plus uv's own project-environment
// variable (Q5): <venv>/bin first on PATH, VIRTUAL_ENV, and
// UV_PROJECT_ENVIRONMENT. Any earlier value of those variables, and
// PYTHONHOME, is dropped.
func Activate(env []string, venv string) []string {
	path := ""
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		switch name {
		case "PATH":
			path = value
		case "VIRTUAL_ENV", "UV_PROJECT_ENVIRONMENT", "PYTHONHOME":
		default:
			out = append(out, kv)
		}
	}
	bin := filepath.Join(venv, "bin")
	if path != "" {
		bin += string(os.PathListSeparator) + path
	}
	return append(out, "PATH="+bin, "VIRTUAL_ENV="+venv, "UV_PROJECT_ENVIRONMENT="+venv)
}

// VenvVersion reads the Python version an environment was made with from its
// pyvenv.cfg: version_info as uv writes it, or version as python -m venv
// does. uv 0.12 writes only the minor ("3.11") when it links a managed
// interpreter by its minor directory; the full version is the interpreter's
// own (M5 review #2).
func VenvVersion(venv string) (string, error) {
	f, err := os.Open(filepath.Join(venv, "pyvenv.cfg"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	found := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok {
			found[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	for _, k := range []string{"version_info", "version"} {
		if v := found[k]; v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s names no Python version", filepath.Join(venv, "pyvenv.cfg"))
}

// VersionMatches reports whether a full version ("3.11.13") is the declared
// minor ("3.11").
func VersionMatches(full, minor string) bool {
	return full == minor || strings.HasPrefix(full, minor+".")
}

// CheckVenv reports why venv cannot serve a studio that declares python:
// version, or nil when it can: its pyvenv.cfg names that version, and its
// interpreter resolves — a link into a deleted managed Python, or a uv killed
// after writing pyvenv.cfg, leaves an environment that cannot run (M5 review
// #10).
func CheckVenv(venv, version string) error {
	got, err := VenvVersion(venv)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("there is no Python environment at %s", venv)
	case err != nil:
		return fmt.Errorf("reading the Python environment at %s: %w", venv, err)
	case !VersionMatches(got, version):
		return fmt.Errorf("the Python environment at %s is Python %s; the manifest declares %s", venv, got, version)
	}
	python := filepath.Join(venv, "bin", "python")
	if fi, err := os.Stat(python); err != nil || fi.IsDir() {
		return fmt.Errorf("the Python environment at %s has no usable interpreter (%s does not resolve)", venv, python)
	}
	return nil
}

// python resolves a studio's environment for a launch: the directory {venv}
// names and the variables its members get besides activation.
func (s *Supervisor) python(m *manifest.Manifest) (string, []string, error) {
	if s.cfg.Python != nil {
		return s.cfg.Python(m)
	}
	venv := VenvDir(s.dirs, m.ID)
	if err := CheckVenv(venv, m.Python.Version); err != nil {
		return "", nil, fmt.Errorf("%w; install %s again to recreate it", err, m.ID)
	}
	return venv, UVEnv(s.dirs), nil
}
