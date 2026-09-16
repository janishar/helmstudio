package manifest

// What a capability lets a studio do, in words (docs/design/03-design-system.md
// §18, docs/decisions.md M7 Q12).
//
// `gallery.read_all` tells a person nothing. The approval screen is the one
// place someone decides whether to run a stranger's code, and a screen that
// names a permission in the vocabulary of the thing asking for it has not
// informed anybody.
//
// **This table is the contract, and 03 §18 is where it is written down.** The
// test beside it diffs the two, the way the design tokens are diffed — a
// sentence that drifts from the design is a gate failure, because the sentence
// *is* the security surface. There is one table because a second one would
// eventually disagree, and the disagreement would be invisible: both would
// still be plausible English about the same capability.

// Capability is one capability and the sentence shown for it.
type Capability struct {
	Name     string `json:"capability"`
	Sentence string `json:"sentence"`
	// Warning marks the two that grant reach beyond the studio's own work.
	Warning bool `json:"warning"`
}

// NoCapabilities is what the screen says for a studio that declares none. It
// gets no token at all, which is worth saying plainly rather than leaving the
// section blank.
const NoCapabilities = "Uses no helmstudio services. It gets no access token."

// capabilitySentences is the table. Order is the order the screen shows.
var capabilitySentences = []Capability{
	{Name: "kv", Sentence: "Saves its own settings and sessions in helmstudio."},
	{Name: "records", Sentence: "Keeps its own records, such as a list of takes, in helmstudio."},
	{Name: "assets", Sentence: "Stores the files it makes in your helmstudio library."},
	{Name: "gallery", Sentence: "Adds what it makes to your gallery, and receives items other studios send it."},
	{Name: "jobs", Sentence: "Reports its long-running work to helmstudio."},
	{Name: "timeline", Sentence: "Can create sequences on your timeline."},
	{Name: "gallery.read_all", Sentence: "Can read everything you have ever made, in every studio.", Warning: true},
	{Name: "kv.shared", Sentence: "Can read and change settings shared by every studio.", Warning: true},
	{Name: "handoff.send", Sentence: "Can send items to other studios."},
}

// CapabilitySentences returns the sentences for what a manifest declares, in
// the table's order rather than the manifest's — so two studios asking for the
// same things read the same way, and a manifest cannot bury the alarming one
// at the bottom by declaring it last.
//
// A capability the table does not know is still shown, named as itself, rather
// than dropped: an unknown permission is the last thing to hide.
func CapabilitySentences(declared []string) []Capability {
	want := make(map[string]bool, len(declared))
	for _, c := range declared {
		want[c] = true
	}
	var out []Capability
	for _, c := range capabilitySentences {
		if want[c.Name] {
			out = append(out, c)
			delete(want, c.Name)
		}
	}
	for _, c := range declared {
		if want[c] {
			out = append(out, Capability{
				Name:     c,
				Sentence: "Asks for “" + c + "”, which this version of helmstudio does not recognise.",
				Warning:  true,
			})
			delete(want, c)
		}
	}
	return out
}

// KnownCapabilities is the table, for anything that needs the whole list.
func KnownCapabilities() []Capability {
	out := make([]Capability, len(capabilitySentences))
	copy(out, capabilitySentences)
	return out
}
