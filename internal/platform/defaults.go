package platform

import "path/filepath"

// The OS conventions themselves, as pure functions so both are tested on
// whichever machine runs the tests. The build-constrained files only choose
// between them.

// darwinDefaults follows docs/design/06-storage.md §4's macOS column.
func darwinDefaults(home string) osDefaults {
	return osDefaults{
		home:  home,
		data:  filepath.Join(home, "Library", "Application Support", "helmstudio"),
		cache: filepath.Join(home, "Library", "Caches", "helmstudio"),
		logs:  filepath.Join(home, "Library", "Logs", "helmstudio"),
	}
}

// linuxDefaults follows docs/design/06-storage.md §4's Linux column. Where an
// XDG variable is unset, empty or relative, the XDG Base Directory
// specification says to ignore it and use its documented fallback.
func linuxDefaults(home string, lookupEnv func(string) (string, bool)) osDefaults {
	xdg := func(key string, fallback ...string) string {
		if v, _ := lookupEnv(key); v != "" && filepath.IsAbs(v) {
			return filepath.Join(v, "helmstudio")
		}
		return filepath.Join(append(append([]string{home}, fallback...), "helmstudio")...)
	}
	return osDefaults{
		home:  home,
		data:  xdg("XDG_DATA_HOME", ".local", "share"),
		cache: xdg("XDG_CACHE_HOME", ".cache"),
		logs:  xdg("XDG_STATE_HOME", ".local", "state"),
	}
}
