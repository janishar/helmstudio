// Package manifest loads and validates helmstudio studio manifests
// (helmstudio.yaml) against schema/manifest.json and the rules the schema
// cannot express on its own.
package manifest

// Manifest mirrors schema/manifest.json. It is decoded permissively — schema
// validation is what rejects an unknown field or a wrong type. It holds what
// the semantic rules (see rules.go) look at and what the supervisor needs to
// turn a manifest into a running process group; it is not yet every field.
type Manifest struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Kinds       []string `yaml:"kinds"`
	License     string   `yaml:"license"`
	Repo        string   `yaml:"repo"`
	Ref         string   `yaml:"ref"`
	Submodules  bool     `yaml:"submodules"`
	LocalPath   string   `yaml:"local_path"`

	PeakRAMGB int `yaml:"peak_ram_gb"`

	// Hue is the studio's identity colour per theme; nil gets a ramp entry
	// (docs/design/03-design-system.md §2c).
	Hue *Hue `yaml:"hue"`
	// SDK pins the helm package majors the daemon serves this studio
	// (docs/design/04-packages.md §9); nil means the current ones.
	SDK *SDK `yaml:"sdk"`

	Requires Requires `yaml:"requires"`
	Runtime  Runtime  `yaml:"runtime"`
	Python   *Python  `yaml:"python"`

	Capabilities []string `yaml:"capabilities"`
	Network      []string `yaml:"network"`

	// Storage declares record indexes and per-studio quotas (R33); nil when
	// the manifest has none, which means the defaults.
	Storage *Storage `yaml:"storage"`

	Build   []BuildStep `yaml:"build"`
	Weights []Weight    `yaml:"weights"`

	Processes []Process `yaml:"processes"`
	Run       *Process  `yaml:"run"`

	Test   *TestBlock `yaml:"test"`
	Import *Import    `yaml:"import"`

	// Digest is the sha256 of the manifest's canonical JSON form, set by
	// Load: what an installation records it was built from
	// (docs/design/02-data-model.md §4, manifest_digest). A formatting or
	// comment change to the YAML does not change it.
	Digest string `yaml:"-"`
}

type Requires struct {
	OS    []string `yaml:"os"`
	Arch  []string `yaml:"arch"`
	Tools []string `yaml:"tools"`
	// RAMGB and DiskGB warn on a shortfall; they never block (R5).
	RAMGB  int `yaml:"ram_gb"`
	DiskGB int `yaml:"disk_gb"`
}

type Runtime struct {
	Framework string   `yaml:"framework"`
	Backends  []string `yaml:"backends"`
}

type Python struct {
	Version string `yaml:"version"`
}

// Hue is "#rrggbb" per theme; the schema checks the form.
type Hue struct {
	Dark  string `yaml:"dark"`
	Light string `yaml:"light"`
}

// SDK holds semver ranges ("^1", "~1.2", "1.2.3"); the schema checks the form.
type SDK struct {
	Runtime string `yaml:"runtime"`
	UI      string `yaml:"ui"`
	CSS     string `yaml:"css"`
}

type BuildStep struct {
	Name     string `yaml:"name"`
	Cwd      string `yaml:"cwd"`
	Run      string `yaml:"run"`
	Shell    string `yaml:"shell"`
	TimeoutS int    `yaml:"timeout_s"`
	Optional bool   `yaml:"optional"`
}

// EffectiveName applies the schema's documented default: the command.
func (b BuildStep) EffectiveName() string {
	if b.Name == "" {
		return b.Run
	}
	return b.Name
}

// EffectiveShell applies the schema default ("sh").
func (b BuildStep) EffectiveShell() string {
	if b.Shell == "" {
		return "sh"
	}
	return b.Shell
}

// EffectiveTimeoutS applies the schema default of 3600 seconds.
func (b BuildStep) EffectiveTimeoutS() int {
	if b.TimeoutS == 0 {
		return 3600
	}
	return b.TimeoutS
}

type Weight struct {
	Name     string   `yaml:"name"`
	Repo     string   `yaml:"repo"`
	Revision string   `yaml:"revision"`
	Dest     string   `yaml:"dest"`
	SizeGB   float64  `yaml:"size_gb"`
	Files    []string `yaml:"files"`
	// LocalPath is a directory on this machine that already holds this
	// weight. Install links its files instead of downloading them.
	LocalPath  string `yaml:"local_path"`
	Selectable bool   `yaml:"selectable"`
	Optional   bool   `yaml:"optional"`
}

// EffectiveRevision applies the schema default ("main").
func (w Weight) EffectiveRevision() string {
	if w.Revision == "" {
		return "main"
	}
	return w.Revision
}

// Process mirrors $defs.process. Role and Autostart keep the pointer-free
// zero value distinct from "explicitly set to the schema default" only where
// a rule needs to tell the two apart — role is the one case that matters
// (rule 3), so it stays a plain string and callers use EffectiveRole.
type Process struct {
	Name      string            `yaml:"name"`
	Role      string            `yaml:"role"`
	Cmd       string            `yaml:"cmd"`
	Cwd       string            `yaml:"cwd"`
	Env       map[string]string `yaml:"env"`
	Shell     string            `yaml:"shell"`
	Port      *Port             `yaml:"port"`
	Health    *Health           `yaml:"health"`
	Busy      *Busy             `yaml:"busy"`
	DependsOn []string          `yaml:"depends_on"`
	Autostart *bool             `yaml:"autostart"`
	Restart   string            `yaml:"restart"`
	Heavy     bool              `yaml:"heavy"`
	UI        string            `yaml:"ui"`
}

// Port mirrors $defs.process.port: at most one of Prefer and Fixed is set.
type Port struct {
	Prefer int `yaml:"prefer"`
	Fixed  int `yaml:"fixed"`
}

// Health mirrors $defs.process.health: exactly one of Path, TCP and Exec.
// TimeoutS and IntervalS are zero when omitted; use the Effective methods.
type Health struct {
	Path      string `yaml:"path"`
	TCP       bool   `yaml:"tcp"`
	Exec      string `yaml:"exec"`
	TimeoutS  int    `yaml:"timeout_s"`
	IntervalS int    `yaml:"interval_s"`
}

// EffectiveTimeoutS applies the schema default of 180 seconds.
func (h Health) EffectiveTimeoutS() int {
	if h.TimeoutS == 0 {
		return 180
	}
	return h.TimeoutS
}

// EffectiveIntervalS applies the schema default of 2 seconds.
func (h Health) EffectiveIntervalS() int {
	if h.IntervalS == 0 {
		return 2
	}
	return h.IntervalS
}

// Busy mirrors $defs.process.busy. The supervisor asks it when a heavy
// launch would stop this studio (docs/decisions.md M5 Q12).
type Busy struct {
	Path string `yaml:"path"`
}

// EffectiveShell applies the schema default ("sh").
func (p Process) EffectiveShell() string {
	if p.Shell == "" {
		return "sh"
	}
	return p.Shell
}

// EffectiveRestart applies the schema default ("never").
func (p Process) EffectiveRestart() string {
	if p.Restart == "" {
		return "never"
	}
	return p.Restart
}

// EffectiveAutostart applies the schema default (true).
func (p Process) EffectiveAutostart() bool {
	return p.Autostart == nil || *p.Autostart
}

// EffectiveRole applies the schema's declared default ("main") for an
// omitted role field. yaml.Unmarshal has no notion of a JSON-Schema default,
// so callers that care about the default must ask for it explicitly rather
// than read Role directly.
func (p Process) EffectiveRole() string {
	if p.Role == "" {
		return "main"
	}
	return p.Role
}

type TestBlock struct {
	Profile string `yaml:"profile"`
	Smoke   string `yaml:"smoke"`
}

type Import struct {
	Run string `yaml:"run"`
	Cwd string `yaml:"cwd"`
}

// Storage is the manifest's storage block (docs/design/06-storage.md §6).
type Storage struct {
	Collections []Collection `yaml:"collections"`
	Quota       Quota        `yaml:"quota"`
}

// Collection names a records collection and the top-level fields to index.
type Collection struct {
	Name  string   `yaml:"name"`
	Index []string `yaml:"index"`
	FTS   []string `yaml:"fts"`
}

// Quota holds per-studio caps; a zero field means the default.
type Quota struct {
	Records int64 `yaml:"records"`
	KVBytes int64 `yaml:"kv_bytes"`
}
