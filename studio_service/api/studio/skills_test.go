package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZip(t *testing.T, file string, entries map[string]string, link string) {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range entries {
		w, _ := z.Create(name)
		_, _ = w.Write([]byte(body))
	}
	if link != "" {
		h := &zip.FileHeader{Name: link}
		h.SetMode(os.ModeSymlink | 0o777)
		w, _ := z.CreateHeader(h)
		_, _ = w.Write([]byte("/etc/passwd"))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const skillJSON = `{"name":"%s","title":"T","version":"0.2.0","kind":"skill","format":1,"description":"d","author":"a",
"may_do":["m"],"when":"w"}`

// API 1.64: yours (folders and .zip) win over built-in ones; a newer format is needs_newer; a .zip with a path out of
// its folder or a symlink, or a skill without SKILL.md, is broken; a good .zip is read from its unpacked copy.
func TestUserPlugins(t *testing.T) {
	user, builtIn := t.TempDir(), t.TempDir()
	put := func(dir string, files map[string]string) {
		_ = os.MkdirAll(dir, 0o755)
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	js := func(name string) string { return strings.Replace(skillJSON, "%s", name, 1) }
	put(filepath.Join(builtIn, "animator"), map[string]string{"plugin.json": strings.Replace(js("animator"), "0.2.0", "0.1.0", 1), "SKILL.md": "x"})
	put(filepath.Join(user, "animator"), map[string]string{"plugin.json": js("animator"), "SKILL.md": "mine", "more.md": "y"})
	put(filepath.Join(user, "future"), map[string]string{"plugin.json": strings.Replace(strings.Replace(js("future"),
		`"format":1`, `"format":2,"sounds":[1]`, 1), "", "", 1)})
	put(filepath.Join(user, "no-guide"), map[string]string{"plugin.json": js("no-guide")})
	writeZip(t, filepath.Join(user, "zipped.zip"), map[string]string{"zipped/plugin.json": js("zipped"), "zipped/SKILL.md": "z"}, "")
	writeZip(t, filepath.Join(user, "escape.zip"), map[string]string{"plugin.json": js("escape"), "SKILL.md": "e", "../x.md": "x"}, "")
	writeZip(t, filepath.Join(user, "linked.zip"), map[string]string{"plugin.json": js("linked"), "SKILL.md": "l"}, "link.md")

	s := &Service{}
	s.SetPluginDirs([]string{builtIn})
	s.SetUserPluginDir(user)
	got := s.readManifests(context.Background())
	check := func(name, origin, blocked, problem string) foundPlugin {
		t.Helper()
		f, ok := got[name]
		if !ok || f.Origin != origin || f.Blocked != blocked || problem != "" && !strings.Contains(*f.Problem, problem) {
			t.Fatalf("%s: %+v", name, f)
		}
		return f
	}
	if f := check("animator", "yours", "", ""); f.Replaces == nil || *f.Replaces != "0.1.0" || f.M.Version != "0.2.0" {
		t.Fatalf("yours replaces the built-in: %+v", f)
	}
	if f := check("future", "yours", "needs_newer", "format 2"); f.M.Title != "T" {
		t.Fatal(f)
	}
	check("no-guide", "yours", "broken", "no SKILL.md")
	check("escape", "yours", "broken", "outside its folder")
	check("linked", "yours", "broken", "outside its folder")
	if f := check("zipped", "yours", "", ""); skillFiles(f.Dir) == nil || filepath.Base(f.Dir) != "zipped" {
		t.Fatal(f)
	}
	if got := skillFiles(got["animator"].Dir); len(got) != 1 || got[0] != "more.md" {
		t.Fatal(got)
	}
}
