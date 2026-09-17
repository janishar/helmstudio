package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv is a hermetic environment: tests never read the real one.
func fakeEnv(kv map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := kv[k]
		return v, ok
	}
}

// fixedDefaults is the default data root as defaultData gives it for a home
// directory of /home/u.
func fixedDefaults() (string, error) { return "/home/u/.helmstudio", nil }

func failingDefaults() (string, error) {
	return "", errors.New("defaults must not be consulted")
}

func mustResolve(t *testing.T, opts Options, defaults func() (string, error)) *Dirs {
	t.Helper()
	d, err := resolve(opts, defaults)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return d
}

func assertRoots(t *testing.T, d *Dirs, want map[Root]string) {
	t.Helper()
	for _, r := range Roots {
		if got := d.Root(r); got != want[r] {
			t.Errorf("%s root = %q, want %q", r, got, want[r])
		}
	}
}

// With nothing overridden every root is in one tree, ~/.helmstudio, as 06 §4
// draws it and as `helm dev` keeps it in ./.helm.
func TestEveryRootDefaultsInOneTree(t *testing.T) {
	d := mustResolve(t, Options{LookupEnv: fakeEnv(nil)}, fixedDefaults)
	assertRoots(t, d, map[Root]string{
		RootData:    "/home/u/.helmstudio",
		RootCache:   "/home/u/.helmstudio/cache",
		RootLogs:    "/home/u/.helmstudio/logs",
		RootLibrary: "/home/u/.helmstudio/library",
		RootModels:  "/home/u/.helmstudio/models",
	})
}

// HELMSTUDIO_HOME is that tree somewhere else, so pointing it at ~/.helmstudio
// moves nothing.
func TestHomeIsTheSameTreeElsewhere(t *testing.T) {
	d := mustResolve(t, Options{LookupEnv: fakeEnv(map[string]string{EnvHome: "/iso"})}, failingDefaults)
	assertRoots(t, d, map[Root]string{
		RootData:    "/iso",
		RootCache:   "/iso/cache",
		RootLogs:    "/iso/logs",
		RootLibrary: "/iso/library",
		RootModels:  "/iso/models",
	})
	home := mustResolve(t, Options{LookupEnv: fakeEnv(map[string]string{EnvHome: "/home/u/.helmstudio"})}, failingDefaults)
	defaults := mustResolve(t, Options{LookupEnv: fakeEnv(nil)}, fixedDefaults)
	for _, r := range Roots {
		if home.Root(r) != defaults.Root(r) {
			t.Errorf("%s: HELMSTUDIO_HOME=~/.helmstudio gives %q, and the default is %q", r, home.Root(r), defaults.Root(r))
		}
	}
}

func TestSpecificVariableWinsOverHome(t *testing.T) {
	env := fakeEnv(map[string]string{EnvHome: "/iso", EnvModelsDir: "/Volumes/ext/models/", EnvCacheDir: "/fast/cache"})
	d := mustResolve(t, Options{LookupEnv: env}, failingDefaults)
	assertRoots(t, d, map[Root]string{
		RootData:    "/iso",
		RootCache:   "/fast/cache",
		RootLogs:    "/iso/logs",
		RootLibrary: "/iso/library",
		RootModels:  "/Volumes/ext/models", // cleaned
	})
}

func TestEnvironmentWinsOverStoredSetting(t *testing.T) {
	stored := func(r Root) (string, bool, error) { return "/stored/" + string(r), true, nil }
	env := fakeEnv(map[string]string{EnvLibraryDir: "/env/library"})
	d := mustResolve(t, Options{LookupEnv: env, Stored: stored}, fixedDefaults)
	if got := d.Library(); got != "/env/library" {
		t.Errorf("library = %q, want the environment's /env/library", got)
	}
	if got := d.Models(); got != "/stored/models" {
		t.Errorf("models = %q, want the stored /stored/models", got)
	}
}

func TestStoredSettingOnlyForLibraryAndModels(t *testing.T) {
	var asked []Root
	stored := func(r Root) (string, bool, error) {
		asked = append(asked, r)
		return "/stored/" + string(r), true, nil
	}
	d := mustResolve(t, Options{LookupEnv: fakeEnv(nil), Stored: stored}, fixedDefaults)
	assertRoots(t, d, map[Root]string{
		RootData:    "/home/u/.helmstudio",
		RootCache:   "/home/u/.helmstudio/cache",
		RootLogs:    "/home/u/.helmstudio/logs",
		RootLibrary: "/stored/library",
		RootModels:  "/stored/models",
	})
	if len(asked) != 2 || asked[0] != RootLibrary || asked[1] != RootModels {
		t.Errorf("stored setting consulted for %v, want only [library models]", asked)
	}
}

func TestAbsentStoredSettingFallsBackToDefault(t *testing.T) {
	stored := func(Root) (string, bool, error) { return "", false, nil }
	d := mustResolve(t, Options{LookupEnv: fakeEnv(nil), Stored: stored}, fixedDefaults)
	if got := d.Library(); got != "/home/u/.helmstudio/library" {
		t.Errorf("library = %q, want the default", got)
	}
}

// Moving the data root moves the whole tree, without asking for a home
// directory; a root with its own variable stays where it was put.
func TestTheTreeFollowsAnOverriddenDataRoot(t *testing.T) {
	env := fakeEnv(map[string]string{EnvDataDir: "/elsewhere", EnvLogsDir: "/var/log/helmstudio"})
	d := mustResolve(t, Options{LookupEnv: env}, failingDefaults)
	assertRoots(t, d, map[Root]string{
		RootData:    "/elsewhere",
		RootCache:   "/elsewhere/cache",
		RootLogs:    "/var/log/helmstudio",
		RootLibrary: "/elsewhere/library",
		RootModels:  "/elsewhere/models",
	})
}

func TestRelativePathsAreRejected(t *testing.T) {
	cases := map[string]Options{
		"home":     {LookupEnv: fakeEnv(map[string]string{EnvHome: "rel/home"})},
		"specific": {LookupEnv: fakeEnv(map[string]string{EnvLogsDir: "./logs"})},
		"stored": {LookupEnv: fakeEnv(nil), Stored: func(Root) (string, bool, error) {
			return "library", true, nil
		}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := resolve(opts, fixedDefaults); err == nil || !strings.Contains(err.Error(), "not an absolute path") {
				t.Fatalf("err = %v, want a not-an-absolute-path error", err)
			}
		})
	}
}

func TestStoredSettingErrorIsReported(t *testing.T) {
	boom := errors.New("settings table unreadable")
	stored := func(Root) (string, bool, error) { return "", false, boom }
	if _, err := resolve(Options{LookupEnv: fakeEnv(nil), Stored: stored}, fixedDefaults); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestDerivedPathsLiveInTheirRoots(t *testing.T) {
	d := mustResolve(t, Options{LookupEnv: fakeEnv(map[string]string{EnvHome: "/iso"})}, failingDefaults)
	checks := []struct{ got, want string }{
		{d.DB(), "/iso/helm.db"},
		{d.DBLock(), "/iso/helm.db.lock"},
		{d.DBBackup(7), "/iso/helm.db.bak.7"},
		{d.Stage(), "/iso/stage"}, // same volume as assets/blobs
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestTheDefaultDataRootIsDotHelmstudioInTheHomeDirectory(t *testing.T) {
	if got := defaultDataRoot("/Users/u"); got != "/Users/u/.helmstudio" {
		t.Errorf("defaultDataRoot(/Users/u) = %q, want /Users/u/.helmstudio", got)
	}
}

func TestUnderIgnoresTheProcessEnvironment(t *testing.T) {
	t.Setenv(EnvDataDir, "/must/not/be/used")
	root := t.TempDir()
	d, err := Under(root)
	if err != nil {
		t.Fatal(err)
	}
	if d.Data() != root {
		t.Errorf("data root = %q, want %q", d.Data(), root)
	}
	for _, r := range Roots[1:] {
		if !strings.HasPrefix(d.Root(r), root+string(filepath.Separator)) {
			t.Errorf("%s root %q escapes %q", r, d.Root(r), root)
		}
	}
}

func TestEnsureCreatesEveryRoot(t *testing.T) {
	// A tree that does not exist yet, as ~/.helmstudio does not on first run.
	d, err := Under(filepath.Join(t.TempDir(), "tree"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, r := range Roots {
		fi, err := os.Stat(d.Root(r))
		if err != nil || !fi.IsDir() {
			t.Errorf("%s root not created: %v", r, err)
		}
	}
	if fi, _ := os.Stat(d.Data()); fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("data root mode %v is readable by others", fi.Mode().Perm())
	}
}
