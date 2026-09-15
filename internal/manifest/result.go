package manifest

import "fmt"

// Error is one validation failure. Pointer is a JSON pointer into the
// manifest document (e.g. "/processes/1/depends_on/0") when the failure is
// schema-shaped, or a process/field name when a semantic rule produced it —
// either way it is specific enough to find in the file, which is the whole
// point: a validator whose output is "invalid manifest" has failed at its
// only job.
type Error struct {
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"` // 1-based; 0 when the pointer could not be resolved back to a line
	Pointer  string `json:"pointer"`
	Rule     string `json:"rule"`     // which of the seven rules, or "schema"
	Message  string `json:"message"`  // what is wrong
	Expected string `json:"expected"` // what was expected, when that adds information Message doesn't already carry
}

func (e Error) String() string {
	loc := e.Pointer
	if e.Line > 0 {
		loc = fmt.Sprintf("line %d, %s", e.Line, e.Pointer)
	}
	if e.Expected != "" {
		return fmt.Sprintf("%s: %s: %s (expected %s)", e.File, loc, e.Message, e.Expected)
	}
	return fmt.Sprintf("%s: %s: %s", e.File, loc, e.Message)
}

// Result is the outcome of validating one manifest file.
type Result struct {
	File   string  `json:"file"`
	Errors []Error `json:"errors"`
}

func (r Result) OK() bool { return len(r.Errors) == 0 }
