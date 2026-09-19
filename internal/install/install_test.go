package install

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/janishar/helmstudio/internal/manifest"
	"github.com/janishar/helmstudio/internal/supervisor"
	"github.com/janishar/helmstudio/internal/weights/hubtest"
)

// A studio goes from listed to launchable: cloned at its pinned commit,
// every step run once in order, its weight downloaded and bound. Installing
// again finds nothing to do: no step runs and nothing is fetched.
func TestInstallFromCleanStateIsIdempotent(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	if got := f.info("toy-studio"); got.State != StateListed {
		t.Fatalf("before install: %+v, want listed", got)
	}
	if _, err := f.sup.Launch(context.Background(), "toy-studio", supervisor.LaunchOptions{}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("launch before install: err = %v, want a refusal", err)
	}

	j := f.install("toy-studio")
	if j.State != JobSucceeded || len(j.Steps) != 3 {
		t.Fatalf("install job = %+v", j)
	}
	info := f.info("toy-studio")
	root := filepath.Join(f.dirs.Data(), "studios", "toy-studio", "src")
	if info.State != StateReady || info.Root != root || info.CommitSHA != f.commit || info.RebuildNeeded || info.LastFailure != nil {
		t.Fatalf("after install: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(root, "built")); err != nil {
		t.Fatalf("the last step did not run in the checkout: %v", err)
	}
	if f.steps() != "one,two,three" {
		t.Fatalf("steps ran: %s", f.steps())
	}
	if !strings.Contains(string(info.RuntimeEnv), `"git"`) {
		t.Errorf("runtime_env = %s; want the resolved tools", info.RuntimeEnv)
	}
	if _, err := os.Stat(filepath.Join(f.dirs.Models(), "Toy", "model.bin")); err != nil {
		t.Fatalf("the weight was not downloaded: %v", err)
	}
	if n := len(f.hub.Requests("config.json")); n != 0 {
		t.Errorf("a file outside the weight's allow-list was fetched: %d requests", n)
	}

	requests := len(f.hub.Requests("/"))
	again := f.install("toy-studio")
	if again.State != JobSucceeded || f.steps() != "one,two,three" || len(again.Steps) != 0 {
		t.Fatalf("second install: job %+v, steps %s; want success with nothing re-run", again, f.steps())
	}
	if n := len(f.hub.Requests("/")); n != requests {
		t.Fatalf("a second install contacted Hugging Face (%d → %d requests)", requests, n)
	}

	gs, err := f.sup.Launch(context.Background(), "toy-studio", supervisor.LaunchOptions{})
	if err != nil {
		t.Fatalf("launch after install: %v", err)
	}
	if !strings.Contains(gs.Processes[0].Command, filepath.Join("models", "Toy")) {
		t.Fatalf("the process was not given the weight: %q", gs.Processes[0].Command)
	}
	if _, err := f.in.Install(context.Background(), "toy-studio"); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("install while running: err = %v, want a refusal", err)
	}
}

// R8, R10: a failing step stops the sequence with its index, exit code, log
// and last lines; retry resumes at that step and never re-runs the ones
// before it.
func TestFailedStepSurfacesItsOutputAndRetryResumesThere(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.failStepTwo(true)

	j := f.install("toy-studio")
	info := f.info("toy-studio")
	lf := info.LastFailure
	if j.State != JobFailed || info.State != StateFailedBuild || lf == nil {
		t.Fatalf("job %+v, info %+v; want a failed build", j, info)
	}
	if lf.Phase != "build" || lf.StepIndex == nil || *lf.StepIndex != 1 || lf.ExitCode == nil || *lf.ExitCode != 3 || lf.LogFileID == "" || lf.Code != "step_failed" {
		t.Fatalf("last_failure = %+v", lf)
	}
	if !strings.Contains(lf.Message, "boom from step two") || !strings.Contains(lf.Message, "exited 3") {
		t.Fatalf("the failure does not carry the step's output: %q", lf.Message)
	}
	if got := j.Steps; len(got) != 3 || got[0].State != "succeeded" || got[1].State != "failed" || got[2].State != "pending" {
		t.Fatalf("steps = %+v", got)
	}
	logPath := filepath.Join(f.dirs.Logs(), j.Steps[1].LogPath)
	if b, _ := os.ReadFile(logPath); !strings.Contains(string(b), "boom from step two") {
		t.Fatalf("the step's log %s lacks its output: %q", logPath, b)
	}
	if f.steps() != "one" {
		t.Fatalf("steps ran: %s", f.steps())
	}
	if _, err := f.sup.Launch(context.Background(), "toy-studio", supervisor.LaunchOptions{}); err == nil || !strings.Contains(err.Error(), "failed_build") {
		t.Fatalf("launch after a failed build: err = %v; want a refusal naming the state", err)
	}

	f.failStepTwo(false)
	retry := f.install("toy-studio")
	if retry.State != JobSucceeded || f.info("toy-studio").State != StateReady {
		t.Fatalf("retry: %+v", retry)
	}
	if f.steps() != "one,two,three" {
		t.Fatalf("steps after retry: %s; want step one not re-run", f.steps())
	}
	if len(retry.Steps) != 2 || retry.Steps[0].Index != 1 {
		t.Fatalf("retry ran steps %+v; want 1 and 2", retry.Steps)
	}
}

// A changed manifest rebuilds from the start.
func TestChangedManifestRebuildsEveryStep(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.install("toy-studio")
	f.studio("toy-studio", f.repoLine(), f.defaultBuild()+`  - { name: four, run: "echo four >> '`+f.counter+`'" }
`)
	if !f.info("toy-studio").RebuildNeeded {
		t.Fatal("a changed manifest is not reported as needing a rebuild")
	}
	f.install("toy-studio")
	if f.steps() != "one,two,three,one,two,three,four" {
		t.Fatalf("steps: %s", f.steps())
	}
}

// R11: a missing tool fails before the first step, naming it and how to get
// it.
func TestMissingToolFailsBeforeTheFirstStep(t *testing.T) {
	f := newFixture(t, func(c *Config) {
		c.LookPath = func(name string) (string, error) {
			if name == "make" {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + name, nil
		}
	})
	f.studio("toy-studio", f.repoLine()+"\n", f.defaultBuild())
	m, _ := f.sup.Studio("toy-studio")
	m.Manifest.Requires.Tools = []string{"git", "make"}
	j := f.install("toy-studio")
	lf := f.info("toy-studio").LastFailure
	if j.State != JobFailed || lf == nil || lf.Code != "tool_missing" || !strings.Contains(lf.Message, `"make"`) || !strings.Contains(lf.Message, "xcode-select --install") {
		t.Fatalf("job %+v, failure %+v", j, lf)
	}
	if f.steps() != "" {
		t.Fatalf("steps ran despite a missing tool: %s", f.steps())
	}
}

// R5, R5a: os, arch and backend mismatches block before anything is
// recorded; memory and disk shortfalls only warn.
func TestHostMismatchBlocksAndShortfallsWarn(t *testing.T) {
	f := newFixture(t, func(c *Config) {
		c.HostBackends = func() []string { return []string{"cpu"} }
		c.HostMemory = func() (uint64, error) { return 16 << 30, nil }
	})
	f.studio("metal-only", f.repoLine(), f.defaultBuild())
	st, _ := f.sup.Studio("metal-only")
	st.Manifest.Runtime.Backends = []string{"metal"}
	_, err := f.in.Install(context.Background(), "metal-only")
	var ie *Error
	if !errors.As(err, &ie) || ie.Kind != KindBlocked || !strings.Contains(ie.Message, "metal") {
		t.Fatalf("err = %v, want blocked naming metal", err)
	}
	if n := f.count(`SELECT count(*) FROM installations`); n != 0 {
		t.Fatalf("a blocked install recorded %d installations", n)
	}

	// Since M5 a python: manifest is blocked only when uv is missing (M5 Q3,
	// Q7 supersedes M3 Q15); still before anything is recorded.
	f.studio("python-studio", f.repoLine(), f.defaultBuild())
	st, _ = f.sup.Studio("python-studio")
	st.Manifest.Python = &manifest.Python{Version: "3.11"}
	f.in.cfg.LookPath = func(name string) (string, error) {
		if name == "uv" {
			return "", exec.ErrNotFound
		}
		return exec.LookPath(name)
	}
	if _, err := f.in.Install(context.Background(), "python-studio"); !errors.As(err, &ie) || ie.Kind != KindBlocked || !strings.Contains(ie.Message, "Python") {
		t.Fatalf("python manifest: err = %v, want blocked", err)
	}
	f.in.cfg.LookPath = exec.LookPath

	f.studio("hungry", f.repoLine(), f.defaultBuild())
	st, _ = f.sup.Studio("hungry")
	st.Manifest.Requires.RAMGB = 64
	j, err := f.in.Install(context.Background(), "hungry")
	if err != nil || len(j.Warnings) != 1 || !strings.Contains(j.Warnings[0], "64 GB") || !strings.Contains(j.Warnings[0], "16 GB") {
		t.Fatalf("job %+v, err %v; want one warning with both numbers", j, err)
	}
	f.wait(j)
}

// R13: cancelling kills the running step's whole process group, records the
// phase failed and keeps completed work; retry resumes.
func TestCancelKillsTheStepGroupAndKeepsWork(t *testing.T) {
	f := newFixture(t, nil)
	pidFile := filepath.Join(f.work, "child.pid")
	started := filepath.Join(f.work, "started")
	f.studio("toy-studio", f.repoLine(), `
  - { name: one, run: "echo one >> '`+f.counter+`'" }
  - { name: slow, run: "sleep 60 & echo $! > '`+pidFile+`'; touch '`+started+`'; wait" }
`)
	j, err := f.in.Install(context.Background(), "toy-studio")
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, started)
	pidText, _ := os.ReadFile(pidFile)
	child, _ := strconv.Atoi(strings.TrimSpace(string(pidText)))
	if err := f.in.Cancel(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	got := f.wait(j)
	info := f.info("toy-studio")
	if got.State != JobCancelled || info.State != StateFailedBuild || info.LastFailure == nil || info.LastFailure.Code != "cancelled" {
		t.Fatalf("job %+v, info %+v", got, info)
	}
	if got.Steps[0].State != "succeeded" || got.Steps[1].State != "cancelled" {
		t.Fatalf("steps = %+v", got.Steps)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the step's child %d survived cancellation", child)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.steps() != "one" {
		t.Fatalf("steps: %s", f.steps())
	}
}

// 02 §7: a daemon killed mid-build leaves a running step; the next daemon's
// sweep records it interrupted — not failed — and the build failed_build, and
// retry resumes at that step.
func TestSweepRecordsAnInterruptedBuildAndRetryResumes(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.failStepTwo(true)
	j := f.install("toy-studio")
	// Make it look as a SIGKILLed daemon leaves it: step two running, the job
	// running, the installation building.
	exec := func(q string, args ...any) {
		if err := f.st.Update(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, q, args...)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE step_runs SET state = 'running', exit_code = NULL WHERE job_id = ? AND step_index = 1`, j.ID)
	exec(`UPDATE jobs SET state = 'running' WHERE id = ?`, j.ID)
	exec(`UPDATE installations SET install_state = 'building', last_failure = NULL`)

	next := New(f.cfg)
	if err := next.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.in = next
	info := f.info("toy-studio")
	swept, _ := next.Job(context.Background(), j.ID)
	if info.State != StateFailedBuild || info.LastFailure == nil || info.LastFailure.Code != "interrupted" || swept.State != JobInterrupted || swept.Steps[1].State != "interrupted" {
		t.Fatalf("after sweep: info %+v, job %+v", info, swept)
	}
	f.failStepTwo(false)
	f.install("toy-studio")
	if f.steps() != "one,two,three" {
		t.Fatalf("steps after retry: %s", f.steps())
	}
}

// A clone that fails leaves state a retry can continue from.
func TestFailedCloneDoesNotBlockRetry(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", "repo: file://"+filepath.Join(f.work, "no-such-repo")+"\nref: main", f.defaultBuild())
	j := f.install("toy-studio")
	info := f.info("toy-studio")
	if j.State != JobFailed || info.State != StateFailedClone || info.LastFailure == nil || !strings.Contains(info.LastFailure.Message, "no-such-repo") {
		t.Fatalf("job %+v, info %+v", j, info)
	}
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	if j := f.install("toy-studio"); j.State != JobSucceeded || f.info("toy-studio").State != StateReady {
		t.Fatalf("retry after fixing the repo: %+v %+v", j, f.info("toy-studio"))
	}
}

// R18: a gated weight asks for a token; the install is not failed, and
// stays that way across a restart (M3 review #5: the human chose a state).
func TestGatedWeightAsksForATokenWithoutFailingTheInstall(t *testing.T) {
	f := newFixture(t, nil)
	f.hub.NeedToken = true
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	j := f.install("toy-studio")
	info := f.info("toy-studio")
	if info.State != StateAuthRequired || info.LastFailure == nil || info.LastFailure.Code != "auth_required" || j.LastError == nil {
		t.Fatalf("job %+v, info %+v; want auth_required", j, info)
	}
	if err := New(f.cfg).Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.info("toy-studio"); got.State != StateAuthRequired || got.LastFailure == nil || got.LastFailure.Code != "auth_required" {
		t.Fatalf("after a restart's sweep: %+v; want auth_required kept", got)
	}
	f.hub.NeedToken = false
	if j := f.install("toy-studio"); j.State != JobSucceeded || f.steps() != "one,two,three" {
		t.Fatalf("retry with access: %+v, steps %s", j, f.steps())
	}
}

// Review M3 #5: once a token works, fetching the weight the install was
// waiting on finishes the install — the studio does not stay
// fetching_weights with Launch refused.
func TestFetchingTheWaitedOnWeightFinishesTheInstall(t *testing.T) {
	f := newFixture(t, nil)
	f.hub.NeedToken = true
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.install("toy-studio")
	if got := f.info("toy-studio"); got.State != StateAuthRequired {
		t.Fatalf("before the token: %+v", got)
	}
	f.hub.NeedToken = false
	j, err := f.in.FetchWeight(context.Background(), "toy-studio", "base")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.wait(j); got.State != JobSucceeded {
		t.Fatalf("fetch = %+v", got)
	}
	if got := f.info("toy-studio"); got.State != StateReady || got.LastFailure != nil {
		t.Fatalf("after fetching the weight: %+v; want ready", got)
	}
	if _, err := f.sup.Launch(context.Background(), "toy-studio", supervisor.LaunchOptions{}); err != nil {
		t.Fatalf("launch: %v", err)
	}
}

// Optional weights are not downloaded by install; FetchWeight gets one.
func TestOptionalWeightIsFetchedOnlyWhenAskedFor(t *testing.T) {
	f := newFixture(t, nil)
	f.hub.Add("org/extra", "extra.bin", []byte("extra"), true)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	st, _ := f.sup.Studio("toy-studio")
	extra := st.Manifest.Weights[0]
	extra.Name, extra.Repo, extra.Dest, extra.Files, extra.Optional = "extra", "org/extra", "Extra", nil, true
	st.Manifest.Weights = append(st.Manifest.Weights, extra)
	f.install("toy-studio")
	if n := len(f.hub.Requests("org/extra")); n != 0 {
		t.Fatalf("install fetched an optional weight: %d requests", n)
	}
	j, err := f.in.FetchWeight(context.Background(), "toy-studio", "extra")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.wait(j); got.State != JobSucceeded || got.ProgressNum != 5 || got.ProgressDen != 5 {
		t.Fatalf("fetch job = %+v", got)
	}
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Review M3 #8: a ref (or repo) that git would read as an option is refused
// before anything is recorded, and even past that check git is told where
// options end, so --upload-pack cannot run a command during the clone.
func TestRefCannotBeAGitOption(t *testing.T) {
	f := newFixture(t, nil)
	pwned := filepath.Join(f.work, "PWNED")
	evil := "--upload-pack=touch " + pwned + "; git-upload-pack"
	f.studio("toy-studio", "repo: file://"+f.repo+"\nref: \""+evil+"\"", f.defaultBuild())
	_, err := f.in.Install(context.Background(), "toy-studio")
	var ie *Error
	if !errors.As(err, &ie) || ie.Kind != KindBlocked {
		t.Fatalf("err = %v, want blocked", err)
	}
	if n := f.count(`SELECT count(*) FROM installations`); n != 0 {
		t.Fatalf("a refused ref recorded %d installations", n)
	}

	st, _ := f.sup.Studio("toy-studio")
	root := filepath.Join(t.TempDir(), "src")
	if _, fail := f.in.checkout(context.Background(), st.Manifest, root, os.Environ()); fail == nil {
		t.Fatal("checking out an option-shaped ref succeeded")
	}
	if _, err := os.Stat(pwned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("git ran the manifest's --upload-pack command: %v", err)
	}
}

// Review M3 #9: a git lock left by a git command stopped mid-way makes the
// clone phase fail; the failure names the file and how to recover, and
// helmstudio does not delete it.
func TestStaleGitLockNamesTheRecovery(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	f.install("toy-studio")
	lock := filepath.Join(f.dirs.Data(), "studios", "toy-studio", "src", ".git", "index.lock")
	os.WriteFile(lock, nil, 0o644)
	// A changed manifest sends the install back through the clone phase.
	f.studio("toy-studio", f.repoLine(), f.defaultBuild()+`  - { run: "true" }
`)
	j := f.install("toy-studio")
	lf := f.info("toy-studio").LastFailure
	if j.State != JobFailed || lf == nil || lf.Code != "clone_failed" || !strings.Contains(lf.Message, "index.lock") || !strings.Contains(lf.Message, "delete that file, then retry") {
		t.Fatalf("job %+v, failure %+v", j, lf)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("helmstudio removed the lock itself: %v", err)
	}
	os.Remove(lock)
	if j := f.install("toy-studio"); j.State != JobSucceeded {
		t.Fatalf("retry after removing the lock: %+v", j)
	}
}

// 02 §7: a process going running touches its studio's weights, which is what
// Models & disk reads as "Last used". Before any launch there is nothing to
// say, and the field is absent rather than a made-up time.
func TestALaunchRecordsWhenItsWeightsWereUsed(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	if j := f.install("toy-studio"); j.State != JobSucceeded {
		t.Fatalf("install job = %+v", j)
	}
	ctx := context.Background()
	lastUsed := func() *time.Time {
		t.Helper()
		arts, err := f.w.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(arts) != 1 {
			t.Fatalf("got %d artifacts, want 1", len(arts))
		}
		return arts[0].LastUsedAt
	}
	if got := lastUsed(); got != nil {
		t.Fatalf("last used %v before anything launched", got)
	}

	before := time.Now().Add(-time.Second)
	if _, err := f.sup.Launch(ctx, "toy-studio", supervisor.LaunchOptions{}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		gs, err := f.sup.Status(ctx, "toy-studio")
		if err != nil {
			t.Fatal(err)
		}
		if gs.State == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the group never went running: %+v", gs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := lastUsed()
	if got == nil || got.Before(before) {
		t.Fatalf("after the studio went running, last used = %v; want a time after %v", got, before)
	}
}

// An optional weight with a local_path is linked at install. Optional says
// "do not download a hundred gigabytes nobody asked for", not "ignore files
// already on this disk": h3 studio's Ref2VA is optional and shares its folder
// with the required FL2VA, and skipping it left References switched off beside
// a directory that held it.
func TestOptionalWeightWithALocalPathIsLinked(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	user := filepath.Join(t.TempDir(), "MiniMax-H3")
	for _, rel := range []string{"FL2VA/dit.safetensors", "Ref2VA/dit.safetensors"} {
		p := filepath.Join(user, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := f.sup.Studio("toy-studio")
	st.Manifest.Weights = []manifest.Weight{
		{Name: "fl2va", Repo: "org/h3", Dest: "MiniMax-H3", Files: []string{"FL2VA/**"}, LocalPath: user},
		{Name: "ref2va", Repo: "org/h3", Dest: "MiniMax-H3", Files: []string{"Ref2VA/**"}, LocalPath: user, Optional: true},
	}
	f.install("toy-studio")

	link := filepath.Join(f.dirs.Models(), "MiniMax-H3", "Ref2VA", "dit.safetensors")
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the optional weight was not linked: %v", err)
	}
	if n := len(f.hub.Requests("org/h3")); n != 0 {
		t.Errorf("a local_path weight contacted Hugging Face: %d requests", n)
	}
	if got := f.info("toy-studio"); got.State != StateReady {
		t.Errorf("after install: %+v, want ready", got)
	}
}

// An optional weight the directory does not hold leaves the install ready.
// The studio starts without that pipeline and says so, which is what optional
// means; a required one missing is still a refused install.
func TestOptionalWeightMissingFromTheDirectoryStillInstalls(t *testing.T) {
	f := newFixture(t, nil)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	user := filepath.Join(t.TempDir(), "MiniMax-H3")
	p := filepath.Join(user, "FL2VA", "dit.safetensors")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := f.sup.Studio("toy-studio")
	st.Manifest.Weights = []manifest.Weight{
		{Name: "fl2va", Repo: "org/h3", Dest: "MiniMax-H3", Files: []string{"FL2VA/**"}, LocalPath: user},
		{Name: "ref2va", Repo: "org/h3", Dest: "MiniMax-H3", Files: []string{"Ref2VA/**"}, LocalPath: user, Optional: true},
	}
	f.install("toy-studio")

	if got := f.info("toy-studio"); got.State != StateReady {
		t.Fatalf("an optional weight the folder lacks failed the install: %+v", got)
	}
	if _, err := os.Lstat(filepath.Join(f.dirs.Models(), "MiniMax-H3", "FL2VA", "dit.safetensors")); err != nil {
		t.Errorf("the required weight was not linked: %v", err)
	}
}

// Cancel stops an install that is downloading a weight, not only one running a
// build step. TestCancelKillsTheStepGroupAndKeepsWork covers the step; this is
// the other half, and it is the state a person actually sits in front of —
// gigabytes, for hours, with one button.
func TestCancelStopsAWeightDownload(t *testing.T) {
	f := newFixture(t, nil)
	const size = 4 << 20
	f.hub.Add("org/big", "model.safetensors", hubtest.Content(size, 7), true)
	f.hub.StallAt["model.safetensors"] = int64(size * 6 / 10)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	st, _ := f.sup.Studio("toy-studio")
	st.Manifest.Weights = []manifest.Weight{{Name: "big", Repo: "org/big", Dest: "Big"}}

	j, err := f.in.Install(context.Background(), "toy-studio")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.hub.Stalled:
	case <-time.After(30 * time.Second):
		t.Fatal("the download never started, so there was nothing to cancel")
	}

	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	began := time.Now()
	if err := f.in.Cancel(ctx, j.ID); err != nil {
		t.Fatalf("cancelling a download: %v", err)
	}
	t.Logf("cancel returned in %s", time.Since(began).Round(time.Millisecond))

	got, err := f.in.Job(context.Background(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobCancelled {
		t.Fatalf("job = %+v, want cancelled", got)
	}
	if s := f.info("toy-studio"); s.State == StateReady {
		t.Errorf("a cancelled install reports ready: %+v", s)
	}
}

// A studio with selectable weights installs with one checkpoint, not with all
// of them: schema/manifest.json says the chosen one "is the only one
// downloaded; the others are fetched later if the choice changes", and M7 Q21
// amended M3 Q6 to say so. With nothing chosen yet the first declared is the
// one, which is what the approval screen offers by default (03 §5).
func TestInstallDownloadsOnlyTheChosenCheckpoint(t *testing.T) {
	f := newFixture(t, nil)
	f.hub.Add("org/small", "small.bin", []byte("small"), true)
	f.hub.Add("org/large", "large.bin", []byte("large"), true)
	f.studio("toy-studio", f.repoLine(), f.defaultBuild())
	checkpoints(f, "toy-studio")

	f.install("toy-studio")
	if n := len(f.hub.Requests("org/toy")); n == 0 {
		t.Error("the weight that is not selectable was not downloaded")
	}
	if n := len(f.hub.Requests("org/small")); n == 0 {
		t.Error("the first declared checkpoint was not downloaded, and nothing else was chosen")
	}
	if n := len(f.hub.Requests("org/large")); n != 0 {
		t.Fatalf("install downloaded a checkpoint nobody chose: %d requests for org/large", n)
	}
}

// The other half of what the schema promises — "the others are fetched later
// if the choice changes" — has no test here, because it cannot be reached.
// weights.Select needs a studio_model_bindings row, and Link only writes one
// when the studio is already installed (internal/weights/weights.go, "if
// installed > 0"), so before an install every checkpoint but the default is
// refused with "is not a weight this installation has". That is a separate
// defect, recorded in docs/decisions.md; this change does not touch it.

// checkpoints gives a studio two selectable weights beside the plain one the
// fixture declares, so a test can tell "the chosen one" from "all of them".
func checkpoints(f *fixture, id string) {
	f.t.Helper()
	st, _ := f.sup.Studio(id)
	base := st.Manifest.Weights[0]
	add := func(name, repo, dest string) manifest.Weight {
		w := base
		w.Name, w.Repo, w.Dest, w.Files, w.Selectable = name, repo, dest, nil, true
		return w
	}
	st.Manifest.Weights = append(st.Manifest.Weights,
		add("small", "org/small", "Small"), add("large", "org/large", "Large"))
}
