package gen

import (
	"regexp"
	"strings"
)

// Citations in the contract, and which of them this site's reader needs
// (docs/decisions.md, 2026-09-18).
//
// api/openapi.yaml and schema/manifest.json are written for the people
// building helmstudio, and their prose carries two kinds of citation:
//
//   - a DESIGN reference — "05 §6", "R44–R49", "03 §12a" — naming a section of
//     docs/design/ or a numbered requirement. Those are documents a reader can
//     go and read, so they are kept.
//   - a DECISION-LOG reference — "Q7", "M7 Q10", "docs/decisions.md M3 Q9 and
//     Q11" — naming an answer in this project's own build record. To a studio
//     author it is a number with nothing behind it. Those are dropped from the
//     rendered page. They stay in the contract, which is the document whose
//     job it is to keep them.
//
// The two mix inside one parenthesis — "(R44–R49, 05 §6, §7; docs/decisions.md
// M8 Q7–Q19)" — so this works segment by segment, and removes a parenthesis
// only when nothing in it survived. A description that was nothing but a
// citation becomes empty, which is the right answer: "docs/decisions.md M3 Q6"
// told a studio author nothing.
//
// This is a change to how the contract is SHOWN, never to the contract. The
// gate regenerates the reference and fails on a diff, so the two cannot drift.

var (
	// A parenthesis, without nesting: the contract has none.
	parenthetical = regexp.MustCompile(`\(([^()]*)\)`)
	// A decision-log reference anywhere in a segment.
	// "M4 first review #11" names the same record as a Q does: a milestone
	// review, written down in docs/decisions.md.
	decisionRef = regexp.MustCompile(`\bQ\d+\b|docs/decisions\.md|\bM\d+ first review`)
	// One outside any parenthesis, running to the end of its clause.
	bareDecisionRun = regexp.MustCompile(`,?\s*\bdocs/decisions\.md[^.;)]*`)
	// One whole segment that is a citation and nothing else. A description in
	// this contract conventionally opens with a sentence of them — "R34, Q14.
	// The body is the raw bytes." — outside any parenthesis.
	citationToken = regexp.MustCompile(`^(?:R\d+[a-z]?(?:[\x{2013}\-]R?\d+[a-z]?)?|0\d\s*\x{00a7}\s*\S+|\x{00a7}\s*\S+|(?:M\d+\s+)?Q\d+(?:[\x{2013}\-]Q?\d+)?|M\d+\s+first\s+review\s+#\d+|docs/decisions\.md.*)(?:\s+(?:as\s+)?amended\s+by\s+[^,;]*)?$`)
)

// stripLeadingCitations handles a first sentence that is nothing but
// citations. It filters that sentence the way a parenthesis is filtered, and
// leaves a sentence alone the moment any part of it is prose.
func stripLeadingCitations(s string) string {
	// The end of the first sentence: a full stop followed by any whitespace,
	// because this contract writes its opening citation clause on a line of
	// its own, so the break is a newline and not a space.
	cut := -1
	for i, r := range s {
		if r != '.' {
			continue
		}
		if i == len(s)-1 || s[i+1] == ' ' || s[i+1] == '\n' || s[i+1] == '\t' || s[i+1] == '\r' {
			cut = i
			break
		}
	}
	if cut < 0 {
		return s
	}
	head, rest := s[:cut], strings.TrimSpace(s[cut+1:])
	// "and" joins citations as readily as a comma does: "06 §6 and Q19."
	var segs []string
	for _, part := range strings.FieldsFunc(head, func(r rune) bool { return r == ',' || r == ';' }) {
		segs = append(segs, strings.Split(part, " and ")...)
	}
	if len(segs) == 0 {
		return s
	}
	var kept []string
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		if !citationToken.MatchString(seg) {
			return s
		}
		if decisionRef.MatchString(seg) {
			continue
		}
		kept = append(kept, seg)
	}
	if len(kept) == 0 {
		return rest
	}
	return strings.Join(kept, ", ") + ". " + rest
}

// withoutDecisionRefs returns prose with its decision-log citations removed and
// its design citations kept.
func withoutDecisionRefs(s string) string {
	if s == "" {
		return ""
	}
	s = parenthetical.ReplaceAllStringFunc(s, func(m string) string {
		var kept []string
		for _, seg := range strings.FieldsFunc(m[1:len(m)-1], func(r rune) bool { return r == ';' || r == ',' }) {
			seg = strings.TrimSpace(seg)
			if seg == "" || decisionRef.MatchString(seg) {
				continue
			}
			kept = append(kept, seg)
		}
		if len(kept) == 0 {
			return ""
		}
		return "(" + strings.Join(kept, ", ") + ")"
	})
	s = bareDecisionRun.ReplaceAllString(s, "")
	s = stripLeadingCitations(s)
	return tidyProse(s)
}

// tidyProse closes the gaps a removed citation leaves. Removing "(Q7)" from
// "… as it is (Q7)." leaves " ." and removing a whole clause can leave a
// sentence starting with a comma.
func tidyProse(s string) string {
	for _, r := range []struct{ from, to string }{
		{" )", ")"}, {"( ", "("}, {" .", "."}, {" ,", ","}, {" ;", ";"},
		{",.", "."}, {";.", "."}, {"..", "."}, {" :", ":"},
	} {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, " ,;")
	s = strings.TrimSpace(s)
	// Nothing but punctuation left means the prose was nothing but a citation.
	if strings.Trim(s, " .,;:—-") == "" {
		return ""
	}
	return s
}
