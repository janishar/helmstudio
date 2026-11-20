package registry

import (
	"log"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// StarterVoice is one entry of a registry voices value (API 1.30): a voice a pack ships (contracts/README.md, Voices:
// starter voices).
type StarterVoice struct {
	ID       string        `json:"id"` // <family>/<pack voice id>
	Family   string        `json:"family"`
	Name     string        `json:"name"`
	Speaker  string        `json:"speaker"`
	Language string        `json:"language"`
	Style    string        `json:"style,omitempty"`
	Version  int           `json:"version"`
	Preview  *VoicePreview `json:"preview"`
}

// VoicePreview is a starter voice's preview clip in the object store (packs/<family>/voices/…), only ever a thumbnail.
type VoicePreview struct {
	Key        string  `json:"key"`
	Bytes      int64   `json:"bytes"`
	DurationS  float64 `json:"duration_s"`
	SampleRate int     `json:"sample_rate"`
}

var (
	voiceSpeaker  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	voiceLanguage = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`) // BCP 47, as registry.schema.json
)

var voicePreview = regexp.MustCompile(`^packs/([a-z0-9][a-z0-9_-]{0,63})/voices/[a-z0-9][a-z0-9-]{0,63}-[0-9a-f]{16}\.wav$`)

// badVoice says why a published starter voice is not kept ("" = kept), as badLook: its id not <family>/<slug>, a
// speaker, language, name (1-80) or style (at most 500) off registry.schema.json, or a control character in its name
// or style; a preview key outside packs/<family>/voices/ is dropped, the voice kept.
func badVoice(v *StarterVoice) string {
	if m := lookID.FindStringSubmatch(v.ID); m == nil || m[1] != v.Family {
		return "id is not <family>/<slug>"
	}
	if !voiceSpeaker.MatchString(v.Speaker) {
		return "speaker is not a speaker id"
	}
	if !voiceLanguage.MatchString(v.Language) {
		return "language is not a BCP 47 tag"
	}
	if n := utf8.RuneCountInString(v.Name); n < 1 || n > 80 || utf8.RuneCountInString(v.Style) > 500 {
		return "name is not 1-80 characters or style is over 500"
	}
	if strings.ContainsFunc(v.Name, unicode.IsControl) || strings.ContainsFunc(v.Style, unicode.IsControl) {
		return "control characters in its name or style"
	}
	if v.Preview != nil {
		if m := voicePreview.FindStringSubmatch(v.Preview.Key); m == nil || m[1] != v.Family {
			v.Preview = nil
		}
	}
	return ""
}

// LiveVoice is a starter voice as the live hosts serving its family publish it: a non-fake env's entry before env
// fake's, then the highest version, and the oldest published_at of the hosts publishing the id (as LiveLook).
type LiveVoice struct {
	StarterVoice
	PublishedAt time.Time
}

// clashLogged holds the starter voice ids already logged as clashing with a starter look.
var clashLogged sync.Map

// Voices returns the starter voices of the live hosts, by id; a voice whose id is also a live starter look's is left
// out (they would share /library/<family>/<id>; the look keeps it), logged once.
func (r *Registry) Voices(now time.Time) map[string]LiveVoice {
	looks := r.Looks(now) // before the lock: Looks takes it too
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sources []string
	for source := range r.voices {
		if host, ok := r.hosts[source]; ok && host.Live(now) {
			sources = append(sources, source)
		}
	}
	slices.SortFunc(sources, byRank) // own hosts first, a fake env last
	out := map[string]LiveVoice{}
	level := map[string]int{} // id → the best rank publishing it: a worse-ranked entry never replaces it
	for _, source := range sources {
		at := r.voicesAt[source]
		for _, entry := range r.voices[source] {
			if !slices.ContainsFunc(r.models[source], func(m Model) bool { return m.Family == entry.Family }) {
				continue
			}
			if _, clash := looks[entry.ID]; clash {
				if _, done := clashLogged.LoadOrStore(entry.ID, true); !done {
					log.Printf("registry %s: voice %s dropped: a starter look has its id", source, entry.ID)
				}
				continue
			}
			live, seen := out[entry.ID]
			if !seen {
				level[entry.ID] = rank(source)
				live.PublishedAt = at
			}
			if !seen || entry.Version > live.Version && rank(source) == level[entry.ID] {
				live.StarterVoice = entry
			}
			if at.Before(live.PublishedAt) {
				live.PublishedAt = at
			}
			out[entry.ID] = live
		}
	}
	return out
}
