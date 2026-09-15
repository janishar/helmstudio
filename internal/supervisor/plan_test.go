package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Substituted paths are absolute and reach the process as exactly one
// argument each, even with a space (macOS's data root is under
// "Application Support") or a quote in them.
func TestSubstitutedPathsArriveAsOneArgument(t *testing.T) {
	e := newEnv(t, Config{})
	models := filepath.Join(e.dirs.Models(), "Model Dir's")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.dirs.Models(), "linked")
	if err := os.Symlink(models, link); err != nil {
		t.Fatal(err)
	}
	st := e.manifest("args", `weights:
  - { name: base, repo: org/base, dest: linked }`, `
  - name: studio
    role: main
    cmd: printf '%s\n' --model {models.base} --data {data} --root {root} --port {port}
    port: {}
    env: { MODEL_DIR: "{models.base}", LITERAL: "a b" }
`)
	plan, err := resolvePlan(e.dirs, st, nil, e.sup.alloc)
	if err != nil {
		t.Fatal(err)
	}
	pl := plan[0]
	out, err := exec.Command(pl.argv[0], pl.argv[1:]...).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	realModels, _ := filepath.EvalSymlinks(models)
	want := []string{"--model", realModels, "--data", studioData(e.dirs, st.Manifest), "--root", e.root, "--port"}
	if len(got) != len(want)+1 {
		t.Fatalf("argv = %q, want %q plus a port", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("arg %d = %q, want %q", i, got[i], w)
		}
		if strings.HasPrefix(w, "/") && !filepath.IsAbs(got[i]) {
			t.Errorf("arg %d is not absolute: %q", i, got[i])
		}
	}
	if !contains2(pl.env, "MODEL_DIR="+realModels) || !contains2(pl.env, "LITERAL=a b") {
		t.Errorf("env lacks the substituted values: %q", pl.env[len(pl.env)-2:])
	}
	// Nothing was created inside the checkout.
	entries, _ := os.ReadDir(e.root)
	if len(entries) != 0 {
		t.Errorf("resolving a plan wrote into the checkout: %v", entries)
	}
}

func contains2(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// A launch that cannot resolve says what is missing and writes nothing.
func TestUnresolvableLaunchIsRefusedWithTheReason(t *testing.T) {
	cases := []struct {
		name, extra, cmd, want string
	}{
		{"missing weight", `weights:
  - { name: base, repo: org/base, dest: not-there }`, `run --model {models.base}`, "not-there"},
		{"venv", ``, `run {venv}/bin/python`, "{venv}"},
		{"port without a port block", ``, `run --port {port}`, "declares no port block"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, Config{})
			e.manifest("refused", c.extra, `
  - name: studio
    role: main
    cmd: `+c.cmd+`
`)
			_, err := e.sup.Launch(context.Background(), "refused", LaunchOptions{})
			var se *Error
			if !errors.As(err, &se) || se.Kind != KindNotLaunchable || !strings.Contains(se.Message, c.want) {
				t.Fatalf("err = %v; want not launchable, mentioning %q", err, c.want)
			}
			if rows := e.rows("refused"); len(rows) != 0 {
				t.Fatalf("a refused launch wrote rows: %v", rows)
			}
		})
	}
}
