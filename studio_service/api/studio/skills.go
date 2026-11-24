package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/common"
	"github.com/helmstudio/studio_service/registry"
)

// Skills and plugins of yours (API 1.64; contracts/README.md "Skills"): built-in plugins come from the app's plugins/
// (s.pluginDirs), yours from ~/.helm/plugins (s.userPluginDir, read-only): a folder, or a .zip unpacked safely into a
// cache. Yours wins over a built-in one of the same name. A skill is text: SKILL.md and a few small files beside its
// manifest; a skill of a newer format is listed needs_newer, one of yours that can't be read broken. Studio scans on
// start, at Rescan and when ~/.helm/plugins changes (WatchPlugins).

// SetUserPluginDir sets ~/.helm/plugins as mounted (STUDIO_USER_PLUGINS_DIR; "" = none).
func (s *Service) SetUserPluginDir(dir string) { s.userPluginDir = dir }

const (
	skillFileMax = 64 << 10 // each skill file
	skillExtra   = 15       // files besides SKILL.md
	zipMax       = 5 << 20  // a .zip of yours
	zipEntries   = 64
	zipUnpacked  = 2 << 20
)

var skillFileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}\.(md|txt|json)$`)

// foundPlugin is one plugin a scan found.
type foundPlugin struct {
	M        manifest
	Origin   string // built_in | yours
	Dir      string
	Replaces *string
	Blocked  string // '' | needs_newer | broken
	Problem  *string
}

func blocked(name, origin, dir, kind, why string) foundPlugin {
	if kind == "" {
		kind = "skill"
	}
	return foundPlugin{M: manifest{Name: name, Title: name, Version: "0.0.0", Kind: kind, Description: why, Author: "?",
		MayDo: []string{"nothing: it can't be read"}}, Origin: origin, Dir: dir, Blocked: "broken", Problem: &why}
}

// readPlugin reads the plugin in dir (its folder name is the plugin's); ok false: skip it (logged).
func (s *Service) readPlugin(ctx context.Context, dir, origin string) (foundPlugin, bool) {
	folder := filepath.Base(dir)
	raw, err := readLimited(filepath.Join(dir, "plugin.json"), skillFileMax)
	if err != nil || len(raw) > skillFileMax {
		return blocked(folder, origin, dir, "", "its plugin.json is missing or over 64 KB"), origin == "yours"
	}
	var head struct {
		Kind   string `json:"kind"`
		Format *int   `json:"format"`
	}
	if json.Unmarshal(raw, &head) == nil && head.Kind == "skill" && head.Format != nil && *head.Format > 1 {
		// a newer format: only what every format has is read, and it is never used
		var m manifest
		_ = json.Unmarshal(raw, &struct {
			Name, Title, Version, Description, Author *string
		}{&m.Name, &m.Title, &m.Version, &m.Description, &m.Author})
		if m.Name != folder || !pluginNameRe.MatchString(m.Name) {
			return blocked(folder, origin, dir, "skill", "its name is not its folder's"), origin == "yours"
		}
		m.Kind, m.Format, m.MayDo = "skill", head.Format, []string{"nothing until helmstudio is updated"}
		m.Title, m.Version, m.Description, m.Author = cutTo(m.Title, m.Name, 60), cutTo(m.Version, "?", 40),
			cutTo(m.Description, "a newer skill", 300), cutTo(m.Author, "?", 60)
		why := fmt.Sprintf("made for a newer helmstudio (format %d; this one reads 1)", *head.Format)
		return foundPlugin{M: m, Origin: origin, Dir: dir, Blocked: "needs_newer", Problem: &why}, true
	}
	var m manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	why := ""
	if err := dec.Decode(&m); err != nil {
		why = "its plugin.json does not fit the plugin format"
	} else if why = s.problem(ctx, &m, folder); why == "" && m.Kind == "skill" {
		why = skillFolderProblem(dir)
	}
	if why != "" {
		log.Printf("plugin %s (%s) skipped: %s", folder, origin, why)
		return blocked(folder, origin, dir, head.Kind, why), origin == "yours" && pluginNameRe.MatchString(folder)
	}
	return foundPlugin{M: m, Origin: origin, Dir: dir}, true
}

func cutTo(v, fallback string, n int) string {
	if r := []rune(strings.TrimSpace(v)); len(r) > 0 {
		return string(r[:min(len(r), n)])
	}
	return fallback
}

// skillFolderProblem checks a skill's files: SKILL.md, at most 15 more, plain names, .md/.txt/.json, under 64 KB.
func skillFolderProblem(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "its folder can't be read"
	}
	extra, hasGuide := 0, false
	for _, e := range entries {
		info, err := os.Lstat(filepath.Join(dir, e.Name()))
		switch {
		case e.Name() == "plugin.json":
			continue
		case err != nil || !info.Mode().IsRegular():
			return e.Name() + " is not a plain file"
		case !skillFileRe.MatchString(e.Name()) || info.Size() > skillFileMax:
			return e.Name() + ": files are .md, .txt or .json under 64 KB with plain names"
		case e.Name() == "SKILL.md":
			hasGuide = true
		default:
			extra++
		}
	}
	switch {
	case !hasGuide:
		return "it has no SKILL.md"
	case extra > skillExtra:
		return "it has more than 15 files besides SKILL.md"
	}
	return ""
}

// skillFiles is a skill's files besides SKILL.md, by name.
func skillFiles(dir string) []string {
	entries, _ := os.ReadDir(dir)
	out := []string{}
	for _, e := range entries {
		if n := e.Name(); n != "plugin.json" && n != "SKILL.md" && e.Type().IsRegular() && skillFileRe.MatchString(n) {
			out = append(out, n)
		}
	}
	return out
}

// unzip unpacks a .zip of yours into the cache (one folder per name and content); a problem says why it can't be used.
func unzip(file string) (string, string) {
	info, err := os.Stat(file)
	if err != nil || info.Size() > zipMax {
		return "", "it is over 5 MB"
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", "it can't be read"
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", "it is not a zip"
	}
	if len(zr.File) > zipEntries {
		return "", "it has more than 64 files"
	}
	name := strings.TrimSuffix(filepath.Base(file), ".zip")
	sum := sha256.Sum256(raw)
	dest := filepath.Join(os.TempDir(), "helm-plugins", name+"-"+hex.EncodeToString(sum[:4]), name)
	if _, err := os.Stat(filepath.Join(dest, "plugin.json")); err == nil {
		return dest, "" // unpacked before
	}
	// every entry: a plain relative path, no symlink; a single top folder of its name is dropped
	prefix := ""
	if slices.ContainsFunc(zr.File, func(f *zip.File) bool { return strings.HasPrefix(f.Name, name+"/") }) {
		prefix = name + "/"
	}
	total := int64(0)
	tmp := dest + ".part"
	_ = os.RemoveAll(tmp)
	for _, f := range zr.File {
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" || strings.HasSuffix(f.Name, "/") && !strings.Contains(strings.TrimSuffix(rel, "/"), "/") {
			continue // the top folder, or an empty folder entry
		}
		clean := path.Clean(rel)
		if f.Mode()&os.ModeSymlink != 0 || f.Mode().IsDir() || strings.Contains(f.Name, `\`) || path.IsAbs(f.Name) ||
			clean != rel || strings.HasPrefix(clean, "..") || strings.Contains(clean, "/") {
			_ = os.RemoveAll(tmp)
			return "", "a file in it points outside its folder"
		}
		if total += int64(f.UncompressedSize64); total > zipUnpacked {
			_ = os.RemoveAll(tmp)
			return "", "it unpacks to over 2 MB"
		}
		if err := writeEntry(f, filepath.Join(tmp, clean)); err != nil {
			_ = os.RemoveAll(tmp)
			return "", "it can't be unpacked"
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil || os.Rename(tmp, dest) != nil {
		_ = os.RemoveAll(tmp)
		return "", "it can't be unpacked"
	}
	return dest, ""
}

func writeEntry(f *zip.File, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, io.LimitReader(r, zipUnpacked+1))
	return cmpErr(err, w.Close())
}

// settling is how long a file of yours must stay unchanged before a scan reads it (a .zip or a folder being copied).
const settling = 2 * time.Second

// changedSince reports whether anything in p (a file, or a folder and its files) changed after t.
func changedSince(p string, t time.Time) bool {
	changed := false
	_ = filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
		if info, e := d.Info(); err == nil && e == nil && info.ModTime().After(t) {
			changed = true
		}
		return nil
	})
	return changed
}

// readManifests reads yours (~/.helm/plugins: folders and .zip files) and the built-in ones. Yours wins a name while it
// is usable; a broken or newer copy of yours leaves the built-in in use, with your copy's problem. busy: names of yours
// still being written (changed in the last 2 s), left as they are until the next scan.
func (s *Service) readManifests(ctx context.Context) (map[string]foundPlugin, []string) {
	out, busy := map[string]foundPlugin{}, []string{}
	if s.userPluginDir != "" {
		entries, _ := os.ReadDir(s.userPluginDir)
		for _, e := range entries {
			full := filepath.Join(s.userPluginDir, e.Name())
			if changedSince(full, time.Now().Add(-settling)) {
				busy = append(busy, strings.TrimSuffix(e.Name(), ".zip"))
				continue
			}
			var found foundPlugin
			ok := false
			switch {
			case e.IsDir():
				found, ok = s.readPlugin(ctx, full, "yours")
			case strings.HasSuffix(e.Name(), ".zip"):
				name := strings.TrimSuffix(e.Name(), ".zip")
				if !pluginNameRe.MatchString(name) {
					log.Printf("plugin %s skipped: not a plugin's name", e.Name())
					continue
				}
				if dir, why := unzip(full); why != "" {
					found, ok = blocked(name, "yours", full, "", why), true
				} else {
					found, ok = s.readPlugin(ctx, dir, "yours")
				}
			}
			if ok && pluginNameRe.MatchString(found.M.Name) {
				if _, seen := out[found.M.Name]; !seen {
					out[found.M.Name] = found
				}
			}
		}
	}
	for _, dir := range s.pluginDirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*", "plugin.json"))
		for _, f := range files {
			found, ok := s.readPlugin(ctx, filepath.Dir(f), "built_in")
			if !ok || found.Blocked == "broken" {
				continue
			}
			switch mine, seen := out[found.M.Name]; {
			case !seen:
				out[found.M.Name] = found
			case mine.Origin == "yours" && mine.Blocked != "":
				// your copy can't be used: the built-in stays in use, and the row says why yours is not
				why := "Your copy is broken: " + *mine.Problem + "; using built in"
				if mine.Blocked == "needs_newer" {
					why = "Your copy needs a newer helmstudio; using built in"
				}
				found.Problem = &why
				out[found.M.Name] = found
			case mine.Origin == "yours" && mine.Replaces == nil:
				v := found.M.Version
				mine.Replaces = &v
				out[found.M.Name] = mine
			}
		}
	}
	return out, busy
}

// ScanPlugins reads the plugin folders into the table: a new one is added off, a known one's manifest replaced, one
// gone is dropped. It returns the names added.
func (s *Service) ScanPlugins(ctx context.Context) ([]string, error) {
	s.scanMu.Lock() // Rescan and the watch never unpack or write at once
	defer s.scanMu.Unlock()
	defer s.publishRuns(ctx) // API 1.65: what should run follows what was found
	found, busy := s.readManifests(ctx)
	added := []string{}
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		names := []string{}
		for _, name := range slices.Sorted(maps.Keys(found)) {
			f := found[name]
			names = append(names, name)
			var inserted bool
			if err := tx.QueryRow(ctx, `INSERT INTO plugins (name, manifest, origin, dir, replaces, blocked, problem)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (name) DO UPDATE SET manifest = excluded.manifest, origin = excluded.origin, dir = excluded.dir,
				replaces = excluded.replaces, blocked = excluded.blocked, problem = excluded.problem,
				enabled = plugins.enabled,
				updated_at = CASE WHEN plugins.manifest = excluded.manifest AND plugins.blocked = excluded.blocked
					THEN plugins.updated_at ELSE clock_timestamp() END
				RETURNING xmax = 0`, name, toJSON(f.M), f.Origin, f.Dir, f.Replaces, f.Blocked, f.Problem).Scan(&inserted); err != nil {
				return err
			}
			if inserted {
				added = append(added, name)
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM plugins WHERE NOT (name = ANY($1))`, append(names, busy...)) // busy: kept as is
		return err
	})
	return added, err
}

// WatchPlugins rescans when ~/.helm/plugins changes (names, sizes and times of its entries and their files), checked
// every interval until ctx ends. ponytail: polling, not inotify (a bind mount from the Mac does not pass inotify on).
func (s *Service) WatchPlugins(ctx context.Context, interval time.Duration) {
	if s.userPluginDir == "" {
		return
	}
	last := pluginDirState(s.userPluginDir)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		if now := pluginDirState(s.userPluginDir); now != last {
			last = now
			if added, err := s.ScanPlugins(ctx); err != nil {
				log.Printf("plugins: rescan after a change failed: %v", err)
			} else if len(added) > 0 {
				log.Printf("plugins: found %v in ~/.helm/plugins (off)", added)
			}
		}
	}
}

// pluginDirState is a short text of what is in dir, one level down.
func pluginDirState(dir string) string {
	var b strings.Builder
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.Count(strings.TrimPrefix(p, dir), string(filepath.Separator)) > 2 {
			return filepath.SkipDir
		}
		if info, err := d.Info(); err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", p, info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	return b.String()
}

// recipes checks a skill's recipes against this install: the workflow is published, each node is in it and each param
// in its node's schema (or its params).
func (s *Service) recipes(m manifest) ([]dto.SkillRecipe, []dto.RecipeSkipped) {
	ok, skipped := []dto.SkillRecipe{}, []dto.RecipeSkipped{}
	for _, r := range m.Recipes {
		why := ""
		w, found := s.registry.Workflow(r.Workflow)
		if !found {
			why = "this install has no workflow " + r.Workflow
		}
		for _, key := range slices.Sorted(maps.Keys(r.Params)) {
			if why != "" {
				break
			}
			node, param, _ := strings.Cut(key, ".")
			n, has := w.Nodes[node]
			if !has {
				why = fmt.Sprintf("%s has no node %s", r.Workflow, node)
				continue
			}
			_, inParams := n.Params[param]
			if !inParams && !slices.ContainsFunc(w.NodeSchemas[n.Kind].Params, func(p registry.Param) bool { return p.Name == param }) {
				why = fmt.Sprintf("%s's %s has no setting %s", r.Workflow, node, param)
			}
		}
		if why != "" {
			skipped = append(skipped, dto.RecipeSkipped{Name: r.Name, Why: why})
		} else {
			ok = append(ok, dto.SkillRecipe{Name: r.Name, Title: r.Title, Workflow: r.Workflow, Params: r.Params, Note: r.Note})
		}
	}
	return ok, skipped
}

// onSkills are the skills that are on and usable, by name.
func onSkills(ctx context.Context, tx pgx.Tx) ([]pluginRow, error) {
	return readPlugins(ctx, tx, `WHERE enabled AND blocked = '' AND manifest->>'kind' = 'skill'`)
}

// ListSkills answers GET /skills (every user).
func (s *Service) ListSkills(ctx context.Context) (dto.SkillList, error) {
	out := dto.SkillList{Skills: []dto.SkillSummary{}}
	err := common.System(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := onSkills(ctx, tx)
		for _, r := range rows {
			out.Skills = append(out.Skills, summary(r))
		}
		return err
	})
	return out, err
}

func summary(r pluginRow) dto.SkillSummary {
	return dto.SkillSummary{Name: r.Name, Title: r.Manifest.Title, When: r.Manifest.When, Version: r.Manifest.Version}
}

// skillRow is the skill that is on, by name; 404 otherwise.
func (s *Service) skillRow(ctx context.Context, name string) (pluginRow, error) {
	var rows []pluginRow
	err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
		rows, err = onSkills(ctx, tx)
		return err
	})
	if err != nil {
		return pluginRow{}, err
	}
	if i := slices.IndexFunc(rows, func(r pluginRow) bool { return r.Name == name }); i >= 0 {
		return rows[i], nil
	}
	return pluginRow{}, errNotFound
}

// GetSkill answers GET /skills/{skill}: SKILL.md, its files and the recipes that hold.
func (s *Service) GetSkill(ctx context.Context, name string) (dto.Skill, error) {
	r, err := s.skillRow(ctx, name)
	if err != nil {
		return dto.Skill{}, err
	}
	text, err := readLimited(filepath.Join(r.Dir, "SKILL.md"), skillFileMax)
	if err != nil {
		return dto.Skill{}, errNotFound // the folder went away: the next scan drops it
	}
	ok, _ := s.recipes(r.Manifest)
	return dto.Skill{SkillSummary: summary(r), Origin: r.Origin, Text: string(text[:min(len(text), skillFileMax)]),
		Files: skillFiles(r.Dir), Recipes: ok}, nil
}

// GetSkillFile answers GET /skills/{skill}/files/{file}: one of its files (never plugin.json, SKILL.md or a path).
func (s *Service) GetSkillFile(ctx context.Context, name, file string) (dto.SkillFile, error) {
	r, err := s.skillRow(ctx, name)
	if err != nil {
		return dto.SkillFile{}, err
	}
	if !slices.Contains(skillFiles(r.Dir), file) {
		return dto.SkillFile{}, errNotFound
	}
	text, err := readLimited(filepath.Join(r.Dir, file), skillFileMax)
	if err != nil {
		return dto.SkillFile{}, errNotFound
	}
	return dto.SkillFile{Skill: name, File: file, Text: string(text[:min(len(text), skillFileMax)])}, nil
}

// assistantSkills is a turn's `skills`: the skills that are on, one line each.
func assistantSkills(ctx context.Context, tx pgx.Tx) ([]message.TurnSkill, error) {
	rows, err := onSkills(ctx, tx)
	out := []message.TurnSkill{}
	for _, r := range rows {
		if len(out) < 32 {
			out = append(out, message.TurnSkill{Name: r.Name, Title: r.Manifest.Title, When: r.Manifest.When})
		}
	}
	return out, err
}

// publishRuns writes KV host_settings <host>.plugins (API 1.65, registry.schema.json plugin_runs): per host, the plugins
// that are on, usable and start themselves there, by name. Every host written before is written again (an empty list
// stops them). Errors are logged: the next change or start writes it again.
func (s *Service) publishRuns(ctx context.Context) {
	if s.putSettings == nil {
		return
	}
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	var rows []pluginRow
	if err := common.System(ctx, s.db, func(tx pgx.Tx) (err error) {
		rows, err = readPlugins(ctx, tx, `WHERE enabled AND blocked = '' AND manifest ? 'run'`)
		return err
	}); err != nil {
		log.Printf("plugins: the plugins to run not written: %v", err)
		return
	}
	byHost := map[string][]string{}
	for _, r := range rows {
		if r.Manifest.Run != nil {
			byHost[r.Manifest.Run.host()] = append(byHost[r.Manifest.Run.host()], r.Name)
		}
	}
	if s.runHosts == nil {
		s.runHosts = map[string]bool{"mac": true} // a runner may hold an older list: always write mac
	}
	for host := range byHost {
		s.runHosts[host] = true
	}
	for _, host := range slices.Sorted(maps.Keys(s.runHosts)) {
		names := byHost[host]
		if names == nil {
			names = []string{}
		}
		value := toJSON(map[string]any{"schema_version": 1, "host": host, "plugins": names,
			"updated_at": time.Now().UTC().Format(time.RFC3339Nano)})
		if err := s.putSettings(ctx, host+".plugins", value); err != nil {
			log.Printf("plugins: %s.plugins not written: %v", host, err)
		}
	}
}
