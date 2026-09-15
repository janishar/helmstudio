package supervisor

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/platform"
	"github.com/janishar/helmstudio/internal/platform/platformtest"
	"github.com/janishar/helmstudio/internal/store"
)

// The test binary doubles as the fake studio process and as a daemon that can
// be killed with SIGKILL: `<test binary> helper <mode> [flags]`.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "helper" {
		os.Exit(helperMain(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

const helperEnv = "SUPERVISOR_TEST_HELPER"

// withHelper puts the test binary's path where a manifest names
// "$SUPERVISOR_TEST_HELPER". Studios do not inherit the daemon's environment,
// so the variable itself would not reach them.
func withHelper(manifest string) string {
	return strings.ReplaceAll(manifest, `"$`+helperEnv+`"`, `"`+os.Args[0]+`"`)
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name {
			return true
		}
	}
	return false
}

func helperMain(mode string, args []string) int {
	switch mode {
	case "sleep":
		if f := flagValue(args, "term-file"); f != "" {
			terms := make(chan os.Signal, 1)
			signal.Notify(terms, syscall.SIGTERM)
			go func() {
				<-terms
				fh, _ := os.OpenFile(f, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
				suffix := "-grandchild"
				if hasFlag(args, "detached") {
					suffix = ""
				}
				fmt.Fprintf(fh, "%s%s %d\n", flagValue(args, "name"), suffix, time.Now().UnixNano())
				fh.Close()
				if !hasFlag(args, "ignore-term") {
					os.Exit(0)
				}
				select {}
			}()
		}
		time.Sleep(time.Hour)
		return 0
	case "exit":
		ms, _ := strconv.Atoi(flagValue(args, "after-ms"))
		code, _ := strconv.Atoi(flagValue(args, "code"))
		fmt.Println("helper exiting soon")
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return code
	case "serve":
		return helperServe(args)
	case "daemon":
		return helperDaemon(args)
	}
	fmt.Fprintln(os.Stderr, "unknown helper mode", mode)
	return 2
}

// helperServe listens on --port, answers /healthz from --health-file ("fail"
// means 500), optionally forks a grandchild that stays in its process group, logs a
// line every 50ms, records SIGTERM to --term-file, and ignores SIGTERM with
// --ignore-term. /busy answers from --busy-file. --detach starts a child in
// its own session, which records SIGTERM as "<name>-detached" and ignores it
// with --detached-ignores-term.
func helperServe(args []string) int {
	if hasFlag(args, "grandchild") {
		gc := exec.Command(os.Args[0], "helper", "sleep", "--name", flagValue(args, "name"), "--term-file", flagValue(args, "term-file"))
		if err := gc.Start(); err != nil { // no Setpgid: it stays in our group
			fmt.Println("grandchild:", err)
			return 3
		}
		fmt.Printf("grandchild pid %d\n", gc.Process.Pid)
	}
	if hasFlag(args, "detach") {
		// As ltx studio starts a render: in its own session, out of the
		// process group helmstudio signals (docs/decisions.md M5 Q11).
		dargs := []string{"helper", "sleep", "--detached", "--name", flagValue(args, "name") + "-detached", "--term-file", flagValue(args, "term-file")}
		if hasFlag(args, "detached-ignores-term") {
			dargs = append(dargs, "--ignore-term")
		}
		dc := exec.Command(os.Args[0], dargs...)
		dc.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := dc.Start(); err != nil {
			fmt.Println("detached child:", err)
			return 3
		}
		fmt.Printf("detached pid %d\n", dc.Process.Pid)
	}
	terms := make(chan os.Signal, 1)
	signal.Notify(terms, syscall.SIGTERM)
	go func() {
		for range terms {
			if f := flagValue(args, "term-file"); f != "" {
				fh, _ := os.OpenFile(f, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
				fmt.Fprintf(fh, "%s %d\n", flagValue(args, "name"), time.Now().UnixNano())
				fh.Close()
			}
			if !hasFlag(args, "ignore-term") {
				os.Exit(0)
			}
		}
	}()
	if p := flagValue(args, "port"); p != "" {
		l, err := net.Listen("tcp", "127.0.0.1:"+p)
		if err != nil {
			fmt.Println("listen:", err)
			return 4
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			if f := flagValue(args, "health-file"); f != "" {
				if b, _ := os.ReadFile(f); strings.TrimSpace(string(b)) == "fail" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
			w.WriteHeader(http.StatusOK)
		})
		// /busy answers with --busy-file's contents; "status NNN" answers
		// that status, and "hang" never answers in time.
		mux.HandleFunc("/busy", func(w http.ResponseWriter, r *http.Request) {
			b, _ := os.ReadFile(flagValue(args, "busy-file"))
			body := strings.TrimSpace(string(b))
			switch {
			case body == "hang":
				time.Sleep(5 * time.Second)
			case strings.HasPrefix(body, "status "):
				code, _ := strconv.Atoi(strings.TrimPrefix(body, "status "))
				w.WriteHeader(code)
				return
			}
			w.Write([]byte(body))
		})
		go http.Serve(l, mux)
	}
	fmt.Printf("helper %s up, pid %d\n", flagValue(args, "name"), os.Getpid())
	for i := 0; ; i++ {
		fmt.Printf("tick %d\n", i)
		time.Sleep(50 * time.Millisecond)
	}
}

// helperDaemon is a minimal daemon: open the store under --home, launch
// --manifest, print "running" once the group is up, then wait to be killed.
func helperDaemon(args []string) int {
	dirs, err := platform.Under(flagValue(args, "home"))
	if err != nil {
		fmt.Println(err)
		return 1
	}
	st, err := store.Open(context.Background(), dirs)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	m, res, err := manifest.Load(flagValue(args, "manifest"))
	if err != nil || !res.OK() {
		fmt.Println("manifest:", err, res.Errors)
		return 1
	}
	if err := recordInstall(context.Background(), st, dirs, m); err != nil {
		fmt.Println("install:", err)
		return 1
	}
	s := New(Config{Dirs: dirs, Store: st, Grace: 2 * time.Second})
	s.SetStudios([]Studio{{Manifest: m}})
	if _, err := s.Launch(context.Background(), m.ID, LaunchOptions{}); err != nil {
		fmt.Println("launch:", err)
		return 1
	}
	for {
		gs, _ := s.Status(context.Background(), m.ID)
		if gs.State == stateRunning {
			fmt.Println("running")
			break
		}
		if gs.State == stateFailed {
			fmt.Println("failed", gs.Failure)
			return 1
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {}
}

// ---- test fixtures ----

type env struct {
	t     *testing.T
	dirs  *platform.Dirs
	store *store.Store
	sup   *Supervisor
	root  string // a studio checkout
}

func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	t.Setenv(helperEnv, os.Args[0])
	d := platformtest.Dirs(t)
	st, err := store.Open(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Dirs, cfg.Store = d, st
	if cfg.Grace == 0 {
		cfg.Grace = 2 * time.Second
	}
	if cfg.PortMin == 0 {
		cfg.PortMin, cfg.PortMax = 30000, 39999
	}
	cfg.Logf = t.Logf
	e := &env{t: t, dirs: d, store: st, sup: New(cfg), root: filepath.Join(t.TempDir(), "checkout")}
	if err := os.MkdirAll(e.root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := e.sup.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		st.Close()
	})
	return e
}

// manifest writes a manifest with the given processes block, validates it
// through the real loader and registers it.
func (e *env) manifest(id string, extra, processes string) Studio {
	e.t.Helper()
	body := fmt.Sprintf(`id: %s
name: %s
kinds: [video]
local_path: %s
requires: { os: [darwin, linux], arch: [arm64, amd64] }
runtime: { framework: other, backends: [cpu] }
%s
processes:
%s`, id, strings.ReplaceAll(id, "-", " "), e.root, extra, processes)
	body = withHelper(body)
	file := filepath.Join(e.t.TempDir(), id+".yaml")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
	m, res, err := manifest.Load(file)
	if err != nil || !res.OK() {
		e.t.Fatalf("manifest %s: %v %v\n%s", id, err, res.Errors, body)
	}
	if err := recordInstall(context.Background(), e.store, e.dirs, m); err != nil {
		e.t.Fatalf("recording %s as installed: %v", id, err)
	}
	st := Studio{Manifest: m, File: file}
	studios := append(e.sup.Studios(), st)
	e.sup.SetStudios(studios)
	return st
}

// recordInstall records a manifest as install leaves a studio: ready, at its
// local_path. Since M3 a launch reads the installation, so these tests set one
// up rather than depend on the M2 stand-in paths (docs/decisions.md, "M3
// install and weights"). Each declared weight is bound to whatever is at
// <models>/<dest>: a symlink as a linked artifact, a directory as a complete
// download, nothing as one never downloaded. root_path is unique, so a second
// studio sharing a checkout is recorded at a symlink to it.
func recordInstall(ctx context.Context, st *store.Store, dirs *platform.Dirs, m *manifest.Manifest) error {
	return st.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM installations WHERE studio_id = ?`, m.ID).Scan(&n); err != nil || n > 0 {
			return err
		}
		root := m.LocalPath
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM installations WHERE root_path = ?`, root).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			alias := filepath.Join(dirs.Data(), "test-roots", m.ID)
			if err := os.MkdirAll(filepath.Dir(alias), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(root, alias); err != nil && !os.IsExist(err) {
				return err
			}
			root = alias
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO installations (studio_id, manifest_digest, root_path, install_state, created_at, updated_at)
			VALUES (?, ?, ?, 'ready', 1, 1)`, m.ID, m.Digest, root); err != nil {
			return err
		}
		for _, w := range m.Weights {
			rev := w.EffectiveRevision()
			path := filepath.Join(dirs.Models(), w.Dest)
			source, state, real := "managed", "declared", ""
			fi, err := os.Lstat(path)
			switch {
			case err == nil && fi.Mode()&os.ModeSymlink != 0:
				source, state = "linked", "linked"
				real, _ = filepath.EvalSymlinks(path)
			case err == nil && fi.IsDir():
				state = "ready"
			}
			id := store.NewID(time.Now())
			if _, err := tx.ExecContext(ctx, `INSERT INTO model_artifacts (id, hf_repo, revision, source, local_path, external_path, realpath, state, created_at)
				VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, 1) ON CONFLICT (hf_repo, revision) DO NOTHING`,
				id, w.Repo, rev, source, w.Dest, real, real, state); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT id FROM model_artifacts WHERE hf_repo = ? AND revision = ?`, w.Repo, rev).Scan(&id); err != nil {
				return err
			}
			if state == "ready" {
				err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
					if err != nil || !d.Type().IsRegular() {
						return err
					}
					rel, _ := filepath.Rel(path, p)
					_, err = tx.ExecContext(ctx, `INSERT INTO model_files (id, artifact_id, rel_path, size_bytes, state) VALUES (?, ?, ?, 0, 'complete')`,
						store.NewID(time.Now()), id, filepath.ToSlash(rel))
					return err
				})
				if err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO studio_model_bindings (studio_id, artifact_id, placeholder) VALUES (?, ?, ?)`, m.ID, id, w.Name); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *env) waitState(id, want string, within time.Duration) GroupStatus {
	e.t.Helper()
	deadline := time.Now().Add(within)
	var gs GroupStatus
	for time.Now().Before(deadline) {
		var err error
		gs, err = e.sup.Status(context.Background(), id)
		if err != nil {
			e.t.Fatal(err)
		}
		if gs.State == want {
			return gs
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("%s: state %q after %s, want %q: %+v", id, gs.State, within, want, gs)
	return gs
}

func (e *env) waitDone(id string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.sup.Wait(ctx, id); err != nil {
		e.t.Fatalf("%s did not finish tearing down: %v", id, err)
	}
}

type dbRow struct {
	name, state, health, reason string
	pid, pgid                   int
	start                       sql.NullInt64
	code                        sql.NullInt64
	startedAt, exitedAt         sql.NullInt64
}

func (e *env) rows(id string) map[string]dbRow {
	e.t.Helper()
	rs, err := e.store.Reader().Query(`SELECT spec_name, state, COALESCE(health_state,''), COALESCE(exit_reason,''),
		COALESCE(pid,0), COALESCE(pgid,0), pid_start_time, exit_code, started_at, exited_at
		FROM processes WHERE studio_id = ? AND group_run_id = (SELECT group_run_id FROM processes WHERE studio_id = ? ORDER BY rowid DESC LIMIT 1)`, id, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	out := map[string]dbRow{}
	for rs.Next() {
		var r dbRow
		if err := rs.Scan(&r.name, &r.state, &r.health, &r.reason, &r.pid, &r.pgid, &r.start, &r.code, &r.startedAt, &r.exitedAt); err != nil {
			e.t.Fatal(err)
		}
		out[r.name] = r
	}
	return out
}

func groupExists(t *testing.T, pgid int) bool {
	t.Helper()
	ok, err := platform.GroupExists(pgid)
	if err != nil {
		t.Fatalf("GroupExists(%d): %v", pgid, err)
	}
	return ok
}

// grandchildPIDs reads the pids a serve helper reported forking.
func grandchildPIDs(t *testing.T, logPath string) []int {
	return reportedPIDs(t, logPath, "grandchild pid ")
}

// reportedPIDs reads the pids a serve helper printed after prefix.
func reportedPIDs(t *testing.T, logPath, prefix string) []int {
	t.Helper()
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var pids []int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), prefix); ok {
			pid, _ := strconv.Atoi(rest)
			pids = append(pids, pid)
		}
	}
	return pids
}

func alive(pid int) bool {
	_, err := platform.IdentifyProcess(pid)
	return err == nil
}

func (e *env) logPath(id, process string) string {
	e.t.Helper()
	var rel string
	err := e.store.Reader().QueryRow(`SELECT l.path FROM log_files l JOIN processes p ON p.id = l.owner_id
		WHERE p.studio_id = ? AND p.spec_name = ? ORDER BY l.rowid DESC LIMIT 1`, id, process).Scan(&rel)
	if err != nil {
		e.t.Fatalf("log path of %s/%s: %v", id, process, err)
	}
	return filepath.Join(e.dirs.Logs(), rel)
}

func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
