package library

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The three stores, and the rule that a file is named for the id it declares.

// dirStore reads <dir>/<id>.yaml. It is the local store, and also the -studios
// development override.
type dirStore struct {
	kind Source
	dir  string
}

// Dir returns a store over a directory of documents named for their ids.
func Dir(kind Source, dir string) Store { return &dirStore{kind: kind, dir: dir} }

func (d *dirStore) Kind() Source { return d.kind }

// List returns the ids the directory has files for. Directories are skipped:
// <data>/studios/ holds both <id>.yaml files and <id>/ checkout directories,
// and `_reverted/` lives there too — which cannot collide with a studio,
// because an id may not begin with an underscore (Q9).
func (d *dirStore) List() ([]string, error) {
	des, err := os.ReadDir(d.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range des {
		if e.IsDir() || !isDocument(e.Name()) {
			continue
		}
		ids = append(ids, idOf(e.Name()))
	}
	sort.Strings(ids)
	return ids, nil
}

func (d *dirStore) Read(id string) ([]byte, string, error) {
	for _, ext := range []string{".yaml", ".yml"} {
		p := filepath.Join(d.dir, id+ext)
		if b, err := os.ReadFile(p); err == nil {
			return b, p, nil
		} else if !os.IsNotExist(err) {
			return nil, p, err
		}
	}
	return nil, filepath.Join(d.dir, id+".yaml"), fmt.Errorf("no manifest for %q in %s", id, d.dir)
}

// fsStore reads an fs.FS, which is how the bundled registry is carried into
// the binary.
type fsStore struct {
	kind Source
	fsys fs.FS
	name string // for error messages, since an embed.FS has no path
}

// FS returns a store over an embedded or in-memory set of documents.
func FS(kind Source, fsys fs.FS, name string) Store {
	return &fsStore{kind: kind, fsys: fsys, name: name}
}

func (f *fsStore) Kind() Source { return f.kind }

func (f *fsStore) List() ([]string, error) {
	des, err := fs.ReadDir(f.fsys, ".")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range des {
		if e.IsDir() || !isDocument(e.Name()) {
			continue
		}
		ids = append(ids, idOf(e.Name()))
	}
	sort.Strings(ids)
	return ids, nil
}

func (f *fsStore) Read(id string) ([]byte, string, error) {
	for _, ext := range []string{".yaml", ".yml"} {
		if b, err := fs.ReadFile(f.fsys, id+ext); err == nil {
			return b, f.name + "/" + id + ext, nil
		}
	}
	return nil, f.name + "/" + id + ".yaml", fmt.Errorf("no entry for %q in %s", id, f.name)
}

// checkoutStore reads helmstudio.yaml out of installed checkouts, which is
// what keeps an installed studio launchable when the cache has been purged and
// the Mac is offline (Q7).
type checkoutStore struct {
	// root is <data>/studios; each installed studio has <root>/<id>/src.
	root string
}

// Checkouts returns a store over installed studios' own manifests.
func Checkouts(root string) Store { return &checkoutStore{root: root} }

func (c *checkoutStore) Kind() Source { return SourceRepo }

func (c *checkoutStore) List() ([]string, error) {
	des, err := os.ReadDir(c.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range des {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		if _, err := os.Stat(c.manifestPath(e.Name())); err == nil {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (c *checkoutStore) manifestPath(id string) string {
	return filepath.Join(c.root, id, "src", "helmstudio.yaml")
}

func (c *checkoutStore) Read(id string) ([]byte, string, error) {
	p := c.manifestPath(id)
	b, err := os.ReadFile(p)
	return b, p, err
}

// cacheStore reads <cache>/manifests/<id>@<commit>.yaml — manifests fetched
// from repositories, keyed by the commit they were read at, because <id>@<ref>
// goes stale the moment a branch moves (Q5).
type cacheStore struct{ dir string }

// Cache returns a store over fetched manifests.
func Cache(dir string) Store { return &cacheStore{dir: dir} }

func (c *cacheStore) Kind() Source { return SourceRepo }

func (c *cacheStore) List() ([]string, error) {
	des, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, e := range des {
		if e.IsDir() || !isDocument(e.Name()) {
			continue
		}
		id, _, ok := strings.Cut(idOf(e.Name()), "@")
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// Read returns the most recently modified cached manifest for id. A cache
// holding two commits' manifests is normal — the ref moved — and the newest is
// the one the last fetch produced.
func (c *cacheStore) Read(id string) ([]byte, string, error) {
	des, err := os.ReadDir(c.dir)
	if err != nil {
		return nil, c.dir, err
	}
	var newest string
	var newestAt int64 = -1
	for _, e := range des {
		if e.IsDir() || !isDocument(e.Name()) || !strings.HasPrefix(e.Name(), id+"@") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if at := info.ModTime().UnixNano(); at > newestAt {
			newest, newestAt = e.Name(), at
		}
	}
	if newest == "" {
		return nil, c.dir, fmt.Errorf("no cached manifest for %q", id)
	}
	p := filepath.Join(c.dir, newest)
	b, err := os.ReadFile(p)
	return b, p, err
}

// counting wraps a store and counts how many documents were read through it.
// It exists for the test that proves a lower source is never read when a
// higher one has the id — the claim the whole precedence design rests on.
type counting struct {
	Store
	reads *int
}

// Counting returns s wrapped so that every Read increments n.
func Counting(s Store, n *int) Store { return &counting{Store: s, reads: n} }

func (c *counting) Read(id string) ([]byte, string, error) {
	*c.reads++
	return c.Store.Read(id)
}

func isDocument(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

func idOf(name string) string {
	return strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
}
