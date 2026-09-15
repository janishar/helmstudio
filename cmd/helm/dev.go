package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/janishar/helmstudio/internal/api"
	"github.com/janishar/helmstudio/internal/api/studioapi"
	"github.com/janishar/helmstudio/internal/install"
	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/store"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights"
	"github.com/janishar/helmstudio/web"
)

// helm dev is the daemon restricted to one studio (docs/design/05 §5a,
// docs/decisions.md M4 Q26 and first review #6): the same supervisor,
// manifest loader, substitution and studio API, over ./.helm instead of the
// user's data, and never a download.

type linkFlags []string

func (l *linkFlags) String() string     { return strings.Join(*l, ",") }
func (l *linkFlags) Set(v string) error { *l = append(*l, v); return nil }

// devOptions is what runDev needs; tests set stop and ready.
type devOptions struct {
	manifest string
	addr     string
	links    []string
	// venv is the author's own Python environment, for a manifest that
	// declares python: (docs/decisions.md M5 Q15).
	venv   string
	stdout io.Writer
	stderr io.Writer
	// stop ends the run as SIGINT does. Nil means signals only.
	stop <-chan struct{}
	// ready receives the API base URL once the studio is launched.
	ready chan<- string
}

func runDevCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("f", "helmstudio.yaml", "the studio's manifest")
	addr := fs.String("addr", "127.0.0.1:0", "loopback address for the studio API (port 0 picks a free one)")
	var links linkFlags
	fs.Var(&links, "link", "use an existing weights directory: -link <weight>=<directory> (repeatable)")
	venv := fs.String("venv", os.Getenv("VIRTUAL_ENV"), "the Python environment a studio that declares python: runs in (default: the active one, $VIRTUAL_ENV)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: helm dev [-f helmstudio.yaml] [-addr 127.0.0.1:0] [-link weight=dir]... [-venv dir]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := runDev(devOptions{manifest: *file, addr: *addr, links: links, venv: *venv, stdout: stdout, stderr: stderr}); err != nil {
		fmt.Fprintf(stderr, "helm dev: %v\n", err)
		return 1
	}
	return 0
}

// devDirs resolves every root under <studio>/.helm: helm.db, assets, stage
// and logs as the daemon lays them out, and models in ./.helm/models, never
// the daemon's models root (first review #6).
func devDirs(helmDir string) (*platform.Dirs, error) {
	env := map[string]string{
		platform.EnvDataDir:    helmDir,
		platform.EnvCacheDir:   filepath.Join(helmDir, "cache"),
		platform.EnvLogsDir:    filepath.Join(helmDir, "logs"),
		platform.EnvLibraryDir: filepath.Join(helmDir, "library"),
		platform.EnvModelsDir:  filepath.Join(helmDir, "models"),
	}
	return platform.Resolve(platform.Options{LookupEnv: func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}})
}

func runDev(o devOptions) error {
	logf := func(format string, args ...any) { fmt.Fprintf(o.stderr, "helm dev: "+format+"\n", args...) }
	m, res, err := manifest.Load(o.manifest)
	if err != nil {
		return err
	}
	if !res.OK() {
		for _, e := range res.Errors {
			fmt.Fprintf(o.stderr, "%s\n", e)
		}
		return fmt.Errorf("%s is not a valid manifest", o.manifest)
	}
	manifestPath, err := filepath.Abs(o.manifest)
	if err != nil {
		return err
	}
	venv, err := devVenv(m, o.venv)
	if err != nil {
		return err
	}
	root := filepath.Dir(manifestPath)
	helmDir := filepath.Join(root, ".helm")
	dirs, err := devDirs(helmDir)
	if err != nil {
		return err
	}
	if err := dirs.Ensure(); err != nil {
		return err
	}
	// The studio's root is the directory holding its manifest; a local_path in
	// the manifest is what the daemon would use, so honour it when it is set.
	if m.LocalPath != "" {
		root = m.LocalPath
	}

	ctx := context.Background()
	st, err := store.Open(ctx, dirs)
	if err != nil {
		return err
	}
	defer st.Close()

	// helm dev runs no build[] and downloads nothing: the checkout is the
	// author's, and weights come from -link.
	if err := st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		_, err := tx.ExecContext(ctx, `INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at)
			VALUES (?, ?, ?, 'ready', ?, ?)
			ON CONFLICT (studio_id) DO UPDATE SET manifest_digest = excluded.manifest_digest, root_path = excluded.root_path, install_state = 'ready', updated_at = excluded.updated_at`,
			m.ID, m.Digest, root, now, now)
		return err
	}); err != nil {
		return fmt.Errorf("recording %s's checkout at %s: %w", m.ID, root, err)
	}

	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", o.addr, err)
	}
	defer ln.Close()
	addr := ln.Addr().String()
	base := "http://" + addr + studioapi.Base

	dataDir := filepath.Join(helmDir, "data")
	plat := studioapi.NewPlatform(studioapi.PlatformConfig{Store: st, Dirs: dirs, API: base,
		StudioData: func(string) string { return dataDir }, Provider: "embedded", Version: "helm dev", Logf: logf})
	w := weights.New(weights.Config{Store: st, Dirs: dirs, Logf: logf})
	sup := supervisor.New(supervisor.Config{Dirs: dirs, Store: st, Weights: w, Logf: logf, Platform: plat.Launches,
		StudioData: func(*manifest.Manifest) string { return dataDir },
		// The author's environment, as it is: no uv settings of helmstudio's,
		// and checked again at every launch.
		Python: func(m *manifest.Manifest) (string, []string, error) {
			if err := supervisor.CheckVenv(venv, m.Python.Version); err != nil {
				return "", nil, err
			}
			return venv, nil, nil
		}})
	studio := supervisor.Studio{Manifest: m, File: manifestPath}
	sup.SetStudios([]supervisor.Studio{studio})
	in := install.New(install.Config{Store: st, Dirs: dirs, Supervisor: sup, Weights: w, Logf: logf})
	for _, l := range o.links {
		name, dir, ok := strings.Cut(l, "=")
		if !ok || name == "" || dir == "" {
			return fmt.Errorf("-link %q: use -link <weight>=<directory>", l)
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		a, err := in.LinkWeight(ctx, m.ID, name, abs)
		if err != nil {
			return fmt.Errorf("-link %s: %w", l, err)
		}
		logf("weight %s → %s (linked, read-only)", name, a.Realpath)
	}
	if _, err := sup.Readopt(ctx); err != nil {
		return fmt.Errorf("re-adopting what the last helm dev left running: %w", err)
	}
	live := studioapi.LiveGroups(ctx, sup)
	if _, _, err := plat.Readopted(ctx, sup); err != nil {
		return err
	}
	svc, studioAPI := plat.Serve(studioapi.SupervisorStudios{Sup: sup})
	handler, err := api.New(sup, web.Shelf, addr, logf, api.WithStudioAPI(studioAPI, svc))
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return runCtx }}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	go svc.WatchJobs(runCtx, 500*time.Millisecond)

	var gs supervisor.GroupStatus
	if len(live) > 0 {
		logf("%s is still running from the last helm dev; re-adopted it rather than launching a second copy", m.ID)
		gs, err = sup.Status(ctx, m.ID)
	} else {
		gs, err = sup.Launch(ctx, m.ID, supervisor.LaunchOptions{})
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(o.stdout, "helm dev: %s is starting; studio API %s, data in %s\n", m.ID, base, helmDir)
	for _, p := range gs.Processes {
		go followLog(runCtx, sup, m.ID, p.Name, o.stdout)
	}
	if o.ready != nil {
		o.ready <- base
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	ended := make(chan struct{})
	go func() {
		_ = sup.Wait(runCtx, m.ID)
		close(ended)
	}()
	var runErr error
	select {
	case s := <-sig:
		logf("%v: stopping %s", s, m.ID)
	case <-o.stop:
	case <-ended:
		if gs, err := sup.Status(ctx, m.ID); err == nil && gs.State == "failed" && gs.Failure != nil {
			runErr = fmt.Errorf("%s failed: %s %s", m.ID, gs.Failure.Process, gs.Failure.ExitReason)
		}
	case err := <-serveErr:
		runErr = err
	}
	shutdown, stop := context.WithTimeout(ctx, time.Minute)
	defer stop()
	svc.Events().Shutdown("helm dev is stopping")
	cancel()
	_ = srv.Shutdown(shutdown)
	if err := sup.Shutdown(shutdown); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

// devVenv resolves the environment helm dev runs a python: studio in: the
// author's own, from -venv or the active $VIRTUAL_ENV, checked against
// python.version. helm dev runs no build[], so it never creates or changes an
// environment; without one it refuses and says how to make one (Q15).
func devVenv(m *manifest.Manifest, dir string) (string, error) {
	if m.Python == nil {
		return "", nil
	}
	howTo := fmt.Sprintf("create one with `uv venv --python %s`, install the studio's dependencies into it, then activate it or pass -venv <dir>", m.Python.Version)
	if dir == "" {
		return "", fmt.Errorf("%s declares Python %s, and no environment is active; helm dev runs a studio in your own environment and never makes one: %s", m.ID, m.Python.Version, howTo)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := supervisor.CheckVenv(abs, m.Python.Version); err != nil {
		return "", fmt.Errorf("%s: %w; %s", m.ID, err, howTo)
	}
	return abs, nil
}

// followLog prints a process's output, prefixed with its name.
func followLog(ctx context.Context, sup *supervisor.Supervisor, studioID, process string, out io.Writer) {
	// A member still queued behind its dependencies has no log yet.
	var sub supervisor.LogSubscription
	var err error
	for {
		if sub, err = sup.SubscribeLogs(ctx, studioID, process, 0); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer sub.Close()
	for _, l := range sub.History {
		fmt.Fprintf(out, "%s | %s\n", process, l.Text)
	}
	if sub.Lines == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case l, ok := <-sub.Lines:
			if !ok {
				return
			}
			if l.Gap == 0 {
				fmt.Fprintf(out, "%s | %s\n", process, l.Text)
			}
		}
	}
}
