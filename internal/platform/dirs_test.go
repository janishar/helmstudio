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

var testDefaults = osDefaults{home: "/home/u", data: "/os/data", cache: "/os/cache", logs: "/os/logs"}

func fixedDefaults(func(string) (string, bool)) (osDefaults, error) { return testDefaults, nil }

func failingDefaults(func(string) (string, bool)) (osDefaults, error) {
	return osDefaults{}, errors.New("defaults must not be consulted")
}

func mustResolve(t *testing.T, opts Options, defaults func(func(string) (string, bool)) (osDefaults, error)) *Dirs {
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

func TestOSDefaultsWhenNothingIsOverridden(t *testing.T) {
	d := mustResolve(t, Options{LookupEnv: fakeEnv(nil)}, fixedDefaults)
	assertRoots(t, d, map[Root]string{
		RootData:    "/os/data",
		RootCache:   "/os/cache",
		RootLogs:    "/os/logs",
		RootLibrary: "/home/u/helmstudio", // discoverability over convention
		RootModels:  "/os/data/models",
	})
}

func TestHomePutsEveryRootUnderIt(t *testing.T) {
	d := mustResolve(t, Options{LookupEnv: fakeEnv(map[string]string{EnvHome: "/iso"})}, failingDefaults)
	assertRoots(t, d, map[Root]string{
		RootData:    "/iso/data",
		RootCache:   "/iso/cache",
		RootLogs:    "/iso/logs",
		RootLibrary: "/iso/library",
		RootModels:  "/iso/models",
	})
}

func TestSpecificVariableWinsOverHome(t *testing.T) {
	env := fakeEnv(map[string]string{EnvHome: "/iso", EnvModelsDir: "/Volumes/ext/models/", EnvCacheDir: "/fast/cache"})
	d := mustResolve(t, Options{LookupEnv: env}, failingDefaults)
	assertRoots(t, d, map[Root]string{
		RootData:    "/iso/data",
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
		RootData:    "/os/data",
		RootCache:   "/os/cache",
		RootLogs:    "/os/logs",
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
	if got := d.Library(); got != "/home/u/helmstudio" {
		t.Errorf("library = %q, want the default", got)
	}
}

func TestModelsDefaultFollowsAnOverriddenDataRoot(t *testing.T) {
	d := mustResolve(t, Options{LookupEnv: fakeEnv(map[string]string{EnvDataDir: "/elsewhere"})}, fixedDefaults)
	if got := d.Models(); got != "/elsewhere/models" {
		t.Errorf("models = %q, want /elsewhere/models", got)
	}
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
		{d.DB(), "/iso/data/helm.db"},
		{d.DBLock(), "/iso/data/helm.db.lock"},
		{d.DBBackup(7), "/iso/data/helm.db.bak.7"},
		{d.Stage(), "/iso/data/stage"}, // same volume as assets/blobs
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestDarwinDefaults(t *testing.T) {
	got := darwinDefaults("/Users/u")
	want := osDefaults{
		home:  "/Users/u",
		data:  "/Users/u/Library/Application Support/helmstudio",
		cache: "/Users/u/Library/Caches/helmstudio",
		logs:  "/Users/u/Library/Logs/helmstudio",
	}
	if got != want {
		t.Errorf("darwinDefaults = %+v, want %+v", got, want)
	}
}

func TestLinuxDefaults(t *testing.T) {
	t.Run("xdg set", func(t *testing.T) {
		got := linuxDefaults("/home/u", fakeEnv(map[string]string{
			"XDG_DATA_HOME": "/xd", "XDG_CACHE_HOME": "/xc", "XDG_STATE_HOME": "/xs",
		}))
		want := osDefaults{home: "/home/u", data: "/xd/helmstudio", cache: "/xc/helmstudio", logs: "/xs/helmstudio"}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
	t.Run("xdg unset, empty or relative uses the spec fallbacks", func(t *testing.T) {
		got := linuxDefaults("/home/u", fakeEnv(map[string]string{"XDG_DATA_HOME": "", "XDG_CACHE_HOME": "relative"}))
		want := osDefaults{
			home:  "/home/u",
			data:  "/home/u/.local/share/helmstudio",
			cache: "/home/u/.cache/helmstudio",
			logs:  "/home/u/.local/state/helmstudio",
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
}

func TestUnderIgnoresTheProcessEnvironment(t *testing.T) {
	t.Setenv(EnvDataDir, "/must/not/be/used")
	root := t.TempDir()
	d, err := Under(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Roots {
		if !strings.HasPrefix(d.Root(r), root+string(filepath.Separator)) {
			t.Errorf("%s root %q escapes %q", r, d.Root(r), root)
		}
	}
}

func TestEnsureCreatesEveryRoot(t *testing.T) {
	d, err := Under(t.TempDir())
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
