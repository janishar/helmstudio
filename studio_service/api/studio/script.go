package studio

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	gallerydto "github.com/helmstudio/studio_service/api/gallery/dto"
	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// Dialogue scripts (contracts/README.md "Dialogue and Timeline audio", API 1.32): a text param with `script: true`
// holds `@handle (style): text` lines that studio expands into the node's `lines` rows at create time, each line's
// voice its element's voice_id. contracts/check.py script_lines and expand_script are the reference,
// contracts/fixtures/voices/scripts.json the shared tests.

var scriptLine = regexp.MustCompile(`^@([A-Za-z][A-Za-z0-9_-]*)\s*(?:\(([^()]*)\))?\s*:(.*)$`)

// scriptForm is a line's text when it is not one.
const scriptForm = "write @name: text or @name (style): text"

// line is one line of a script: blank (a pause), an error, or a handle, style and text.
type line struct {
	N                   int
	Blank               bool
	Error               string
	Handle, Style, Text string
}

// scriptLines splits a script on \n (a \r before it dropped), each line trimmed, numbered from 1.
func scriptLines(script string) []line {
	var out []line
	for i, raw := range strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(raw)
		m := scriptLine.FindStringSubmatch(t)
		switch {
		case t == "":
			out = append(out, line{N: i + 1, Blank: true})
		case m == nil || !dto.Handle.MatchString(strings.ToLower(m[1])):
			out = append(out, line{N: i + 1, Error: scriptForm})
		default:
			out = append(out, line{N: i + 1, Handle: strings.ToLower(m[1]), Style: strings.TrimSpace(m[2]), Text: strings.TrimSpace(m[3])})
		}
	}
	return out
}

// scriptHandles are the handles a graph's scripts name (known, not bypassed nodes), first use first.
func scriptHandles(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema) []string {
	var out []string
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		sp, ok := scriptParam(nodes[id], schemas)
		if !ok {
			continue
		}
		script, _ := valueOf(nodes[id], sp).(string)
		for _, l := range scriptLines(script) {
			if l.Handle != "" && !slices.Contains(out, l.Handle) {
				out = append(out, l.Handle)
			}
		}
	}
	return out
}

// scriptParam is a known, not bypassed node's script param.
func scriptParam(node registry.Node, schemas map[string]registry.NodeSchema) (registry.Param, bool) {
	schema, known := schemas[node.Kind]
	if !known || node.Bypass {
		return registry.Param{}, false
	}
	i := slices.IndexFunc(schema.Params, func(p registry.Param) bool { return p.Script })
	if i < 0 {
		return registry.Param{}, false
	}
	return schema.Params[i], true
}

// scriptResult is what the expansion changed: each path's value sent and as typed, the speakers and their voices in
// first-line order, and the clip takes the lines send (parents).
type scriptResult struct {
	Inputs, Typed map[string]any
	Elements      []gallerydto.MadeWithElement
	Voices        []gallerydto.MadeWithVoice
	Parents       []string
}

// itemField is a rows param's item field as a param.
func itemField(p registry.Param, name string) registry.Param {
	var out registry.Param
	_ = json.Unmarshal(p.Item[name], &out)
	return out
}

// expandScript expands every known, not bypassed node's non-blank script into its lines (check.py expand_script):
// nodes hold the merged, checked values; pins the workflow's nodes (only); lib the caller's elements (voice_id null
// once its voice is gone); voices the voices those name that the caller can use now. It returns the result or the
// 422 errors.
func expandScript(nodes map[string]registry.Node, schemas map[string]registry.NodeSchema, pins map[string]registry.Node,
	lib []element, voices map[string]voiceItem) (scriptResult, []string) {
	res := scriptResult{Inputs: map[string]any{}, Typed: map[string]any{}, Elements: []gallerydto.MadeWithElement{},
		Voices: []gallerydto.MadeWithVoice{}, Parents: []string{}}
	byHandle := map[string]element{}
	for _, e := range lib {
		byHandle[e.Handle] = e
	}
	var errs []string
	for _, nid := range slices.Sorted(maps.Keys(nodes)) {
		n := nodes[nid]
		sp, ok := scriptParam(n, schemas)
		if !ok {
			continue
		}
		schema := schemas[n.Kind]
		lp, _ := paramOf(schema, "lines")
		vals := merged(schema, n.Params)
		script, _ := vals[sp.Name].(string)
		given, _ := vals["lines"].([]any)
		if given == nil {
			given = []any{}
		}
		if strings.TrimSpace(script) == "" {
			if len(given) == 0 {
				errs = append(errs, nid+"."+sp.Name+": the script is empty")
			}
			continue // typed lines are resolved as voice values (runVoices)
		}
		if len(given) > 0 {
			errs = append(errs, nid+".lines: give the script or the lines, not both")
			continue
		}
		var field registry.Param
		for _, vp := range voiceParams(map[string]registry.Node{nid: n}, schemas, pins) {
			if vp.Param.Name == "lines.voice" {
				field = vp.Param
			}
		}
		textP, styleP, pauseP := itemField(lp, "text"), itemField(lp, "style"), itemField(lp, "pause_s")
		pause, ok := vals["pause"].(float64)
		if !ok {
			pause, _ = pauseP.Default.(float64)
		}
		type row struct {
			text, style string
			el          element
			item        voiceItem
			blanks      int
		}
		var rows []row
		failed := false
		for _, l := range scriptLines(script) {
			if l.Blank {
				if len(rows) > 0 {
					rows[len(rows)-1].blanks++
				}
				continue
			}
			el, known := byHandle[l.Handle]
			var item voiceItem
			hasVoice := false
			if known && el.VoiceID != nil {
				item, hasVoice = voices[strings.ToLower(*el.VoiceID)]
			}
			nText, nStyle := utf8.RuneCountInString(l.Text), utf8.RuneCountInString(l.Style)
			e := l.Error
			switch {
			case e != "":
			case l.Text == "":
				e = "nothing to say"
			case textP.Max != nil && float64(nText) > *textP.Max:
				e = fmt.Sprintf("at most %s characters (%d)", format(*textP.Max), nText)
			case styleP.Max != nil && float64(nStyle) > *styleP.Max:
				e = fmt.Sprintf("the style is at most %s characters (%d)", format(*styleP.Max), nStyle)
			case !known:
				e = "unknown element @" + l.Handle
			case !hasVoice:
				e = "@" + l.Handle + " has no voice"
			default:
				if why := voiceReason(n.Kind, field, item); why != "" {
					e = fmt.Sprintf("voice %s does not fit this workflow (%s)", item.Name, why)
				}
			}
			if e != "" {
				errs = append(errs, fmt.Sprintf("%s.%s line %d: %s", nid, sp.Name, l.N, e))
				failed = true
				continue
			}
			rows = append(rows, row{text: l.Text, style: l.Style, el: el, item: item})
		}
		if failed || len(errs) > 0 {
			continue
		}
		if len(rows) == 0 {
			errs = append(errs, nid+"."+sp.Name+": the script is empty")
			continue
		}
		if lp.Max != nil && float64(len(rows)) > *lp.Max {
			errs = append(errs, fmt.Sprintf("%s.lines: at most %s lines", nid, format(*lp.Max)))
			continue
		}
		lines := make([]any, len(rows))
		for i, r := range rows {
			p := 0.0
			if i < len(rows)-1 {
				p = math.Round(pause*float64(1+r.blanks)*1000) / 1000
				if pauseP.Max != nil {
					p = min(p, *pauseP.Max)
				}
			}
			lines[i] = map[string]any{"voice": map[string]any{"voice_id": r.item.ID}, "text": r.text, "style": r.style, "pause_s": p}
			if !slices.ContainsFunc(res.Elements, func(m gallerydto.MadeWithElement) bool { return m.ID == r.el.ID }) {
				res.Elements = append(res.Elements, gallerydto.MadeWithElement{ID: r.el.ID, Handle: r.el.Handle, Name: r.el.Name, Version: r.el.Version})
			}
			if !slices.ContainsFunc(res.Voices, func(m gallerydto.MadeWithVoice) bool { return m.ID == r.item.ID }) {
				res.Voices = append(res.Voices, gallerydto.MadeWithVoice{ID: r.item.ID, Name: r.item.Name, Version: r.item.Version, Source: r.item.Group})
			}
			if sentSource(field, r.item) == "clip" && !slices.Contains(res.Parents, r.item.Voice.Clip.TakeID) {
				res.Parents = append(res.Parents, r.item.Voice.Clip.TakeID)
			}
		}
		res.Inputs[nid+"."+sp.Name], res.Inputs[nid+".lines"] = "", lines
		res.Typed[nid+"."+sp.Name], res.Typed[nid+".lines"] = script, given
	}
	if len(errs) > 0 {
		return scriptResult{}, errs
	}
	return res, nil
}
