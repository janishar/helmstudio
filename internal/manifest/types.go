// Package manifest loads and validates helmstudio studio manifests
// (helmstudio.yaml) against schema/manifest.json and the rules the schema
// cannot express on its own.
package manifest

// Manifest mirrors schema/manifest.json. It is decoded permissively — schema
// validation is what rejects an unknown field or a wrong type; this struct
// only needs to hold what the seven semantic rules (see rules.go) look at.
type Manifest struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Kinds       []string `yaml:"kinds"`
	License     string   `yaml:"license"`
	Repo        string   `yaml:"repo"`
	Ref         string   `yaml:"ref"`
	LocalPath   string   `yaml:"local_path"`

	Requires Requires `yaml:"requires"`
	Runtime  Runtime  `yaml:"runtime"`
	Python   *Python  `yaml:"python"`

	Capabilities []string `yaml:"capabilities"`
	Network      []string `yaml:"network"`

	Build   []BuildStep `yaml:"build"`
	Weights []Weight    `yaml:"weights"`

	Processes []Process `yaml:"processes"`
	Run       *Process  `yaml:"run"`

	Test   *TestBlock `yaml:"test"`
	Import *Import    `yaml:"import"`
}

type Requires struct {
	OS   []string `yaml:"os"`
	Arch []string `yaml:"arch"`
}

type Runtime struct {
	Framework string   `yaml:"framework"`
	Backends  []string `yaml:"backends"`
}

type Python struct {
	Version string `yaml:"version"`
}

type BuildStep struct {
	Name string `yaml:"name"`
	Cwd  string `yaml:"cwd"`
	Run  string `yaml:"run"`
}

type Weight struct {
	Name       string  `yaml:"name"`
	Repo       string  `yaml:"repo"`
	Dest       string  `yaml:"dest"`
	SizeGB     float64 `yaml:"size_gb"`
	Selectable bool    `yaml:"selectable"`
	Optional   bool    `yaml:"optional"`
}

// Process mirrors $defs.process. Role and Autostart keep the pointer-free
// zero value distinct from "explicitly set to the schema default" only where
// a rule needs to tell the two apart — role is the one case that matters
// (rule 3), so it stays a plain string and callers use EffectiveRole.
type Process struct {
	Name      string   `yaml:"name"`
	Role      string   `yaml:"role"`
	Cmd       string   `yaml:"cmd"`
	Cwd       string   `yaml:"cwd"`
	DependsOn []string `yaml:"depends_on"`
	Heavy     bool     `yaml:"heavy"`
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
