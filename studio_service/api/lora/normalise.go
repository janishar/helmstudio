package lora

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/helmstudio/studio_service/registry"
)

// The LoRA normaliser (contracts/README.md "LoRA normaliser", normative; contracts/check.py normalise is the
// reference and contracts/fixtures/loras the shared tests). Studio runs it at upload to detect style, rank and family;
// the runner runs the same rules at load.

// suffix is a key suffix and the role it gives: A (lora_down), B (lora_up) or alpha.
type suffix struct{ text, role string }

// suffixes are each style's suffixes, in the order the reference tries them.
var suffixes = map[string][]suffix{
	"kohya": {{".lora_down.weight", "A"}, {".lora_up.weight", "B"}, {".alpha", "alpha"}},
	"comfyui": {{".lora_A.weight", "A"}, {".lora_B.weight", "B"}, {".lora_down.weight", "A"}, {".lora_up.weight", "B"},
		{".alpha", "alpha"}},
	"peft": {{".lora_A.weight", "A"}, {".lora_B.weight", "B"}, {".lora.down.weight", "A"}, {".lora.up.weight", "B"},
		{".alpha", "alpha"}},
}

var kohyaPrefixes = []string{"lora_unet_", "lora_transformer_"}

const comfyPrefix = "diffusion_model."

// Style is the key style of a file's keys: the first rule that matches any key (kohya, comfyui, peft), "" for none.
func Style(keys []string) string {
	switch {
	case slices.ContainsFunc(keys, func(k string) bool { return hasAnyPrefix(k, kohyaPrefixes) != "" }):
		return "kohya"
	case slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, comfyPrefix) }):
		return "comfyui"
	case slices.ContainsFunc(keys, func(k string) bool {
		return slices.ContainsFunc(suffixes["peft"], func(s suffix) bool { return strings.HasSuffix(k, s.text) })
	}):
		return "peft"
	}
	return ""
}

// hasAnyPrefix is the first of prefixes key starts with, "" for none.
func hasAnyPrefix(key string, prefixes []string) string {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return prefix
		}
	}
	return ""
}

// parse splits a key of style into its module part and role; ok false when it is not a key of that style.
func parse(key, style string) (part, role string, ok bool) {
	var rest string
	switch style {
	case "kohya":
		prefix := hasAnyPrefix(key, kohyaPrefixes)
		if prefix == "" {
			return "", "", false
		}
		rest = key[len(prefix):]
	case "comfyui":
		var found bool
		if rest, found = strings.CutPrefix(key, comfyPrefix); !found {
			return "", "", false
		}
	case "peft":
		rest = strings.TrimPrefix(strings.TrimPrefix(key, "base_model.model."), "transformer.")
	default:
		return "", "", false
	}
	for _, s := range suffixes[style] {
		if strings.HasSuffix(rest, s.text) {
			if len(rest) <= len(s.text) {
				return "", "", false
			}
			return rest[:len(rest)-len(s.text)], s.role, true
		}
	}
	return "", "", false
}

var moduleRange = regexp.MustCompile(`\{(\d+)\.\.(\d+)\}`)

// ExpandModules expands each module entry's one {a..b} range to a..b inclusive.
func ExpandModules(modules []string) []string {
	var out []string
	for _, m := range modules {
		r := moduleRange.FindStringSubmatchIndex(m)
		if r == nil {
			out = append(out, m)
			continue
		}
		a, _ := strconv.Atoi(m[r[2]:r[3]])
		b, _ := strconv.Atoi(m[r[4]:r[5]])
		for i := a; i <= b && i-a < 100000; i++ {
			out = append(out, m[:r[0]]+strconv.Itoa(i)+m[r[1]:])
		}
	}
	return out
}

// Glob matches s against an ignore pattern: `*` any run, `?` one character, everything else itself (case-sensitive).
// ponytail: no [seq] classes; the schema's ignore globs use * and ? only.
func Glob(pattern, s string) bool {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(s)
}

// ignored reports whether key matches any of globs.
func ignored(key string, globs []string) bool {
	return slices.ContainsFunc(globs, func(g string) bool { return Glob(g, key) })
}

// Result is what the normaliser makes of a file for one family.
type Result struct {
	Style     string   `json:"key_style"` // "" unrecognised
	Rank      int      `json:"rank"`      // 0 when no group has the right shapes
	Total     int      `json:"total"`
	Applied   int      `json:"applied"`
	Ignored   int      `json:"ignored"`
	Unapplied []string `json:"unapplied"` // code-point order
	Error     string   `json:"error"`     // "" when the file fits the family
}

// Normalise fits a file's header to one family's lora block (named family in the errors): every key ignored,
// applied or unapplied. A file fits when at least one key applies and none is unapplied.
func Normalise(name string, header Header, block registry.LoraBlock, family string) Result {
	keys := header.Keys()
	style := Style(keys)
	modules := map[string]bool{}
	for _, m := range ExpandModules(block.Modules) {
		if style == "kohya" {
			m = strings.ReplaceAll(m, ".", "_")
		}
		modules[m] = true
	}
	out := Result{Style: style, Total: len(keys), Unapplied: []string{}}
	type group struct{ a, b, alpha []string }
	groups, order := map[string]*group{}, []string{}
	for _, key := range keys {
		if ignored(key, block.Ignore) {
			out.Ignored++
			continue
		}
		part, role, ok := parse(key, style)
		if !ok {
			out.Unapplied = append(out.Unapplied, key)
			continue
		}
		for _, r := range block.Renames {
			if r.At == "end" && strings.HasSuffix(part, r.From) {
				part = part[:len(part)-len(r.From)] + r.To
				break
			}
			if r.At != "end" && strings.HasPrefix(part, r.From) {
				part = r.To + part[len(r.From):]
				break
			}
		}
		g := groups[part]
		if g == nil {
			g = &group{}
			groups[part], order = g, append(order, part)
		}
		switch role {
		case "A":
			g.a = append(g.a, key)
		case "B":
			g.b = append(g.b, key)
		default:
			g.alpha = append(g.alpha, key)
		}
	}
	lora := func(k string) bool { t := header.Tensors[k].DType; return t == "F32" || t == "F16" || t == "BF16" }
	for _, part := range order {
		g := groups[part]
		ok := len(g.a) == 1 && len(g.b) == 1 && len(g.alpha) <= 1
		if ok {
			ta, tb := header.Tensors[g.a[0]], header.Tensors[g.b[0]]
			ok = len(ta.Shape) == 2 && len(tb.Shape) == 2 && ta.Shape[0] == tb.Shape[1] && ta.Shape[0] >= 1 &&
				lora(g.a[0]) && lora(g.b[0]) && !slices.ContainsFunc(g.alpha, func(k string) bool {
				shape := header.Tensors[k].Shape
				return !lora(k) || !(len(shape) == 0 || (len(shape) == 1 && shape[0] == 1))
			})
			if ok {
				out.Rank = max(out.Rank, int(ta.Shape[0]))
			}
		}
		n := len(g.a) + len(g.b) + len(g.alpha)
		if ok && modules[part] {
			out.Applied += n
		} else {
			out.Unapplied = append(out.Unapplied, slices.Concat(g.a, g.b, g.alpha)...)
		}
	}
	slices.Sort(out.Unapplied)
	switch n := len(out.Unapplied); {
	case out.Applied == 0:
		out.Error = fmt.Sprintf("LoRA %s: 0 of %d keys apply to %s (%s keys); is it for another model?", name, out.Total,
			family, cmp.Or(style, "unrecognised"))
	case n > 0:
		out.Error = fmt.Sprintf("LoRA %s: %d of %d keys do not apply to %s: %s", name, n, out.Total, family,
			strings.Join(out.Unapplied[:min(3, n)], ", "))
		if n > 3 {
			out.Error += fmt.Sprintf(" and %d more", n-3)
		}
	}
	return out
}

// Check runs the upload checks that follow the header's (contracts/README.md "LoRA upload checks"): a style, every
// key a LoRA weight of it or matching one of ignore (every published family's ignore globs), each module part with
// both lora_A and lora_B of one rank, at most MaxRank, and no IC-LoRA metadata. It answers the file's style and
// largest rank, or the 422 detail.
func Check(header Header, ignore []string) (style string, rank int, detail string) {
	keys := header.Keys()
	if style = Style(keys); style == "" {
		return "", 0, "no LoRA keys found (is it a full model?)"
	}
	type pair struct{ a, b string }
	parts := map[string]*pair{}
	for _, key := range keys {
		if ignored(key, ignore) {
			continue
		}
		part, role, ok := parse(key, style)
		if !ok {
			return "", 0, key + " is not a LoRA weight"
		}
		if parts[part] == nil {
			parts[part] = &pair{}
		}
		switch role {
		case "A":
			parts[part].a = cmp.Or(parts[part].a, key)
		case "B":
			parts[part].b = cmp.Or(parts[part].b, key)
		}
	}
	order, largest := slices.Sorted(maps.Keys(parts)), int64(0)
	for _, part := range order {
		switch p := parts[part]; {
		case p.a != "" && p.b == "":
			return "", 0, part + ": lora_B is missing"
		case p.b != "" && p.a == "":
			return "", 0, part + ": lora_A is missing"
		}
	}
	for _, part := range order {
		p := parts[part]
		if p.a == "" {
			continue // an alpha alone: the normaliser leaves it unapplied
		}
		a, b := header.Tensors[p.a].Shape, header.Tensors[p.b].Shape
		if len(a) != 2 || len(b) != 2 || a[0] != b[1] || a[0] < 1 {
			return "", 0, part + ": lora_A and lora_B ranks differ"
		}
		largest = max(largest, a[0])
	}
	if largest > MaxRank {
		return "", 0, fmt.Sprintf("rank %d is over %d", largest, MaxRank)
	}
	if largest == 0 {
		return "", 0, "no LoRA keys found (is it a full model?)" // alphas only
	}
	if _, ok := header.Metadata["reference_downscale_factor"]; ok { // API 1.39: Lightricks' LTX-2.5 IC-LoRAs
		return "", 0, "an IC-LoRA (it needs a reference video), not a stack LoRA"
	}
	return style, int(largest), ""
}

// Licence is the file's licence: __metadata__ modelspec.license, else license, trimmed, cut to 200 characters; nil
// when neither names one.
func Licence(metadata map[string]string) *string {
	value := metadata["modelspec.license"]
	if value == "" {
		value = metadata["license"]
	}
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 200 {
		value = string([]rune(value)[:200])
	}
	if value == "" {
		return nil
	}
	return &value
}

// Detect is the family whose block the file fits, "unknown" when none: one a non-fake env publishes before one only
// env fake publishes (a real LoRA never lands on the fake echo family), then by name.
func Detect(name string, header Header, blocks map[string]registry.LoraBlock) string {
	families := slices.SortedFunc(maps.Keys(blocks), func(a, b string) int {
		if blocks[a].Fake != blocks[b].Fake {
			if blocks[a].Fake {
				return 1
			}
			return -1
		}
		return strings.Compare(a, b)
	})
	for _, family := range families {
		if Normalise(name, header, blocks[family], family).Error == "" {
			return family
		}
	}
	return "unknown"
}

// Ignores is every family's ignore globs.
func Ignores(blocks map[string]registry.LoraBlock) []string {
	var out []string
	for _, family := range slices.Sorted(maps.Keys(blocks)) {
		out = append(out, blocks[family].Ignore...)
	}
	return out
}
