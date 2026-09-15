package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Root names one of the five independently resolved directory roots
// (docs/design/06-storage.md §4, "Where the root actually is").
type Root string

const (
	// RootData holds helm.db, studio checkouts and assets/blobs. Backed up.
	RootData Root = "data"
	// RootCache holds regenerable files only. The OS may purge it at any
	// moment and excludes it from backup, so nothing irreplaceable goes here.
	RootCache Root = "cache"
	// RootLogs holds build and run logs.
	RootLogs Root = "logs"
	// RootLibrary is the human-readable media tree. Chosen for
	// discoverability, not platform convention: it defaults to ~/helmstudio.
	RootLibrary Root = "library"
	// RootModels holds weights, often on an external drive.
	RootModels Root = "models"
)

// Roots lists every root in resolution order. RootModels comes after RootData
// because its default is derived from the resolved data root.
var Roots = []Root{RootData, RootCache, RootLogs, RootLibrary, RootModels}

// Environment variables that override the resolved roots. A specific variable
// wins over EnvHome; both win over a stored setting and the OS default.
const (
	EnvHome       = "HELMSTUDIO_HOME"
	EnvDataDir    = "HELMSTUDIO_DATA_DIR"
	EnvCacheDir   = "HELMSTUDIO_CACHE_DIR"
	EnvLogsDir    = "HELMSTUDIO_LOGS_DIR"
	EnvLibraryDir = "HELMSTUDIO_LIBRARY_DIR"
	EnvModelsDir  = "HELMSTUDIO_MODELS_DIR"
)

var envForRoot = map[Root]string{
	RootData:    EnvDataDir,
	RootCache:   EnvCacheDir,
	RootLogs:    EnvLogsDir,
	RootLibrary: EnvLibraryDir,
	RootModels:  EnvModelsDir,
}

// settable reports whether a root may come from a stored setting. The data
// root cannot: the settings live in helm.db, which lives in the data root.
func settable(r Root) bool { return r == RootLibrary || r == RootModels }

// StoredSetting looks up a user's stored override for a root. ok is false when
// the user has not overridden it, which means "use the default" — so a
// settings row copied between machines still resolves correctly.
type StoredSetting func(r Root) (path string, ok bool, err error)

// Options controls resolution. The zero value resolves from the process
// environment and the OS defaults, with no stored settings.
type Options struct {
	// LookupEnv reads an environment variable. Nil means os.LookupEnv.
	LookupEnv func(key string) (string, bool)
	// Stored is consulted for settable roots only. Nil means none stored.
	Stored StoredSetting
}

// osDefaults are the OS-convention locations for the three roots the
// operating system has an opinion about, plus the user's home directory.
type osDefaults struct {
	home, data, cache, logs string
}

// Dirs is the resolved set of roots and the only place a path is built.
type Dirs struct {
	roots map[Root]string
}

// Resolve resolves every root. For each: its specific environment variable,
// then HELMSTUDIO_HOME/<root>, then a stored setting (library and models
// only), then the OS default.
func Resolve(opts Options) (*Dirs, error) {
	return resolve(opts, currentDefaults)
}

// Under resolves every root beneath one directory, exactly as HELMSTUDIO_HOME
// would, ignoring the process environment and any stored setting. This is
// how tests, CI and `helm dev` get a tree that is not the user's.
func Under(home string) (*Dirs, error) {
	env := func(k string) (string, bool) {
		if k == EnvHome {
			return home, true
		}
		return "", false
	}
	return resolve(Options{LookupEnv: env}, func(func(string) (string, bool)) (osDefaults, error) {
		return osDefaults{}, errors.New("unreachable: every root is under HELMSTUDIO_HOME")
	})
}

func resolve(opts Options, defaults func(lookupEnv func(string) (string, bool)) (osDefaults, error)) (*Dirs, error) {
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	env := func(k string) string {
		v, _ := lookup(k)
		return v
	}

	home := env(EnvHome)
	if home != "" && !filepath.IsAbs(home) {
		return nil, fmt.Errorf("%s=%q is not an absolute path; set it to an absolute directory or unset it", EnvHome, home)
	}

	var def *osDefaults // resolved lazily: an all-override tree never asks the OS
	d := &Dirs{roots: make(map[Root]string, len(Roots))}
	for _, r := range Roots {
		if v := env(envForRoot[r]); v != "" {
			if !filepath.IsAbs(v) {
				return nil, fmt.Errorf("%s=%q is not an absolute path; set it to an absolute directory or unset it", envForRoot[r], v)
			}
			d.roots[r] = filepath.Clean(v)
			continue
		}
		if home != "" {
			d.roots[r] = filepath.Join(home, string(r))
			continue
		}
		if settable(r) && opts.Stored != nil {
			v, ok, err := opts.Stored(r)
			if err != nil {
				return nil, fmt.Errorf("reading the stored %s root setting: %w", r, err)
			}
			if ok {
				if !filepath.IsAbs(v) {
					return nil, fmt.Errorf("the stored %s root %q is not an absolute path; change it in Settings", r, v)
				}
				d.roots[r] = filepath.Clean(v)
				continue
			}
		}
		if def == nil {
			got, err := defaults(lookup)
			if err != nil {
				return nil, fmt.Errorf("resolving the default %s directory: %w", r, err)
			}
			def = &got
		}
		switch r {
		case RootData:
			d.roots[r] = def.data
		case RootCache:
			d.roots[r] = def.cache
		case RootLogs:
			d.roots[r] = def.logs
		case RootLibrary:
			d.roots[r] = filepath.Join(def.home, "helmstudio")
		case RootModels:
			d.roots[r] = filepath.Join(d.roots[RootData], "models")
		}
	}
	return d, nil
}

// Root returns the resolved path of one root.
func (d *Dirs) Root(r Root) string { return d.roots[r] }

// Data returns the data root.
func (d *Dirs) Data() string { return d.roots[RootData] }

// Cache returns the cache root. Only regenerable files belong here.
func (d *Dirs) Cache() string { return d.roots[RootCache] }

// Logs returns the logs root.
func (d *Dirs) Logs() string { return d.roots[RootLogs] }

// Library returns the human-readable library root.
func (d *Dirs) Library() string { return d.roots[RootLibrary] }

// Models returns the models root.
func (d *Dirs) Models() string { return d.roots[RootModels] }

// Stage returns the per-launch scratch root. It sits under the data root
// because adoption hardlinks staged files into assets/blobs, and a hardlink
// cannot cross a volume.
func (d *Dirs) Stage() string { return filepath.Join(d.roots[RootData], "stage") }

// DB returns the path of the metadata database.
func (d *Dirs) DB() string { return filepath.Join(d.roots[RootData], "helm.db") }

// DBLock returns the path of the file whose exclusive lock marks the one
// process allowed to open DB.
func (d *Dirs) DBLock() string { return d.DB() + ".lock" }

// DBBackup returns the path of the snapshot taken of DB at a schema version
// before migrating past it.
func (d *Dirs) DBBackup(version int) string { return d.DB() + ".bak." + strconv.Itoa(version) }

// Ensure creates every root that does not exist yet. The library and models
// roots are browsable by the user; the rest are private to them.
func (d *Dirs) Ensure() error {
	for _, r := range Roots {
		perm := os.FileMode(0o700)
		if r == RootLibrary || r == RootModels {
			perm = 0o755
		}
		if err := os.MkdirAll(d.roots[r], perm); err != nil {
			return fmt.Errorf("creating the %s directory: %w", r, err)
		}
	}
	return nil
}
