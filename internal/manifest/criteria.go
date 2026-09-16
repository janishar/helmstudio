package manifest

import "fmt"

// The certification criteria a manifest alone can answer
// (docs/design/05-sdk-and-custom-studios.md §9, docs/decisions.md M7 Q16).
//
// Fifteen criteria are defined; seven of them are decidable by reading the
// manifest, and the rest need something that does not exist yet — a smoke
// harness that builds and runs the studio, the studio's source, its
// stylesheets. Those are reported as **not checked, with the reason**, never
// as passes: a tick nothing earned is worse than no tick, because it is the
// tick a person acts on.
//
// One table, read by `helm validate -criteria`, by the editor and by the
// approval preview, so the three cannot disagree about what passed.

// CriterionState is whether a criterion passed, failed, or was not checked.
type CriterionState string

const (
	CriterionPass       CriterionState = "pass"
	CriterionFail       CriterionState = "fail"
	CriterionNotChecked CriterionState = "not_checked"
)

// Criterion is one row of 05 §9 as scored here.
type Criterion struct {
	Number   int            `json:"number"`
	Title    string         `json:"title"`
	State    CriterionState `json:"state"`
	Required bool           `json:"required"`
	Detail   string         `json:"detail,omitempty"`
}

// CriteriaResult is the whole scoring. Passed is out of Checkable, not out of
// fifteen — "7 of 15" would read as a failing grade for a studio that did
// everything a manifest can do.
type CriteriaResult struct {
	Items     []Criterion `json:"items"`
	Passed    int         `json:"passed"`
	Checkable int         `json:"checkable"`
}

// why a criterion is not scored. Each names what is missing, so the answer to
// "when will this be checked?" is in the message rather than in a milestone
// document nobody reading this screen has open.
const (
	needsHarness     = "Not checked: needs the smoke harness, which builds and runs the studio."
	needsSource      = "Not checked: needs the studio's source, which is not cloned yet."
	needsStylesheets = "Not checked: needs the studio's stylesheets, which are not cloned yet."
)

// Criteria scores m. A nil manifest scores nothing, which is what an invalid
// document gets: criteria over a document that did not parse would be noise.
func Criteria(m *Manifest) CriteriaResult {
	if m == nil {
		return CriteriaResult{Items: []Criterion{}}
	}
	items := []Criterion{
		criterion1(m),
		criterion2(m),
		{Number: 3, Title: "Installs from a clean machine with no manual steps", State: CriterionNotChecked, Required: true, Detail: needsHarness},
		criterion4(m),
		criterion5(m),
		{Number: 6, Title: "Releases its memory when stopped", State: CriterionNotChecked, Required: true, Detail: needsHarness},
		{Number: 7, Title: "Writes only inside its own roots", State: CriterionNotChecked, Required: true, Detail: needsHarness},
		criterion8(m),
		{Number: 9, Title: "Requests the minimum capabilities it uses, and no more", State: CriterionNotChecked, Required: true, Detail: needsSource},
		{Number: 10, Title: "Survives a restart with its work intact", State: CriterionNotChecked, Required: false, Detail: needsHarness},
		{Number: 11, Title: "Fails legibly when a weight or a tool is missing", State: CriterionNotChecked, Required: false, Detail: needsHarness},
		{Number: 12, Title: "Theme conformance: tokens only, both themes", State: CriterionNotChecked, Required: false, Detail: needsStylesheets},
		criterion13(m),
		{Number: 14, Title: "Uninstalls completely", State: CriterionNotChecked, Required: true, Detail: needsHarness},
		criterion15(m),
	}
	out := CriteriaResult{Items: items}
	for _, c := range items {
		if c.State == CriterionNotChecked {
			continue
		}
		out.Checkable++
		if c.State == CriterionPass {
			out.Passed++
		}
	}
	return out
}

func scored(n int, title string, required, ok bool, detail string) Criterion {
	c := Criterion{Number: n, Title: title, Required: required, State: CriterionPass}
	if !ok {
		c.State, c.Detail = CriterionFail, detail
	}
	return c
}

// 1: the document is valid, with a well-formed id. Reaching here at all means
// it validated, so only the id is re-checked — a manifest is scored after it
// parses, and an invalid one is not scored at all.
func criterion1(m *Manifest) Criterion {
	return scored(1, "A valid manifest with a well-formed id", true,
		m.ID != "", "the manifest declares no id")
}

// 2: the four fields beyond what the schema already requires. os and arch are
// required by the schema, so declaring them earns nothing.
func criterion2(m *Manifest) Criterion {
	var missing []string
	if len(m.Requires.Tools) == 0 {
		missing = append(missing, "requires.tools")
	}
	if m.Requires.RAMGB == 0 {
		missing = append(missing, "requires.ram_gb")
	}
	if m.Requires.DiskGB == 0 {
		missing = append(missing, "requires.disk_gb")
	}
	if m.PeakRAMGB == 0 {
		missing = append(missing, "peak_ram_gb")
	}
	return scored(2, "Declares what it needs: tools, memory, disk and its peak", true,
		len(missing) == 0, fmt.Sprintf("undeclared: %v", missing))
}

// 4: exactly one main process, with a health probe. "Realistic timeout" is in
// 05 §9's wording and is not scored — nothing offline can judge whether 240
// seconds is realistic for a model nobody has loaded.
func criterion4(m *Manifest) Criterion {
	var mains int
	var withProbe int
	for _, p := range m.EffectiveProcesses() {
		if p.Role != "main" {
			continue
		}
		mains++
		if p.Health != nil {
			withProbe++
		}
	}
	switch {
	case mains != 1:
		return scored(4, "Exactly one main process, with a health probe", true, false,
			fmt.Sprintf("declares %d processes with role main; exactly one is required", mains))
	case withProbe != 1:
		return scored(4, "Exactly one main process, with a health probe", true, false,
			"its main process declares no health probe, so helmstudio cannot tell when it is ready")
	}
	return scored(4, "Exactly one main process, with a health probe", true, true, "")
}

// 5: no fixed port, and {port} in the command of every process that declares
// one. A fixed port is a studio deciding what else may run on the machine.
func criterion5(m *Manifest) Criterion {
	for _, p := range m.EffectiveProcesses() {
		if p.Port == nil {
			continue
		}
		if p.Port.Fixed != 0 {
			return scored(5, "Takes the port it is given", true, false,
				fmt.Sprintf("process %q declares port.fixed; a fixed port decides what else may run on the machine", p.Name))
		}
		var found bool
		for _, ph := range Placeholders(p.Cmd) {
			if ph == "port" {
				found = true
			}
		}
		if !found {
			return scored(5, "Takes the port it is given", true, false,
				fmt.Sprintf("process %q declares a port but its cmd never substitutes {port}", p.Name))
		}
	}
	return scored(5, "Takes the port it is given", true, true, "")
}

// 8: a licence is declared. Weight licences are not checkable — `weights[]` has
// no licence field, which is recorded as an open question.
func criterion8(m *Manifest) Criterion {
	return scored(8, "Declares its licence", true,
		m.License != "", "no license is declared")
}

// 13: a profile for the smoke harness to run.
func criterion13(m *Manifest) Criterion {
	ok := m.Test != nil && m.Test.Profile != ""
	return scored(13, "Declares a test profile", false, ok, "test.profile is not declared")
}

// 15: pinned, and pinned to something. Whether a ref is a branch rather than a
// tag or a commit cannot be decided without asking the repository, so it is
// not decided here.
func criterion15(m *Manifest) Criterion {
	var missing []string
	if m.SDK == nil {
		missing = append(missing, "sdk")
	}
	if m.Ref == "" && m.LocalPath == "" {
		missing = append(missing, "ref")
	}
	return scored(15, "Pins the SDK majors it builds against, and its own ref", false,
		len(missing) == 0, fmt.Sprintf("undeclared: %v", missing))
}
