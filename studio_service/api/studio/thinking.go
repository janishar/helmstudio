package studio

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/api/studio/message"
	"github.com/helmstudio/studio_service/gen"
)

// Thinking per model (plan 28, API 1.53; contracts/README.md "Model sources: thinking"): a preset's rules (the
// embedded presets file) say which levels a model offers and the body fragment each sends; studio checks a PUT
// against them and resolves the snapshot's `thinking` wherever it builds one. Pipelines never reads the file.

// thinkingRule is one rule of a preset: the first whose match fits a model id gives its levels.
type thinkingRule struct {
	Match   string                     `json:"match"`
	Levels  map[string]json.RawMessage `json:"levels"`
	Default string                     `json:"default"`
	Always  bool                       `json:"always"`
	re      *regexp.Regexp
}

// thinkingFile is what the presets file says of thinking: the extra tokens per level, each preset's rules, and the
// presets whose levels come from the provider's /models ("models": OpenRouter).
type thinkingFile struct {
	Tokens  map[string]int
	Rules   map[string][]thinkingRule
	FromAPI map[string]bool
}

var thinking = sync.OnceValue(func() thinkingFile {
	var file struct {
		Tokens  map[string]int `json:"thinking_tokens"`
		Presets []struct {
			ID       string          `json:"id"`
			Thinking json.RawMessage `json:"thinking"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(gen.ProviderPresets, &file); err != nil {
		panic(err)
	}
	out := thinkingFile{Tokens: file.Tokens, Rules: map[string][]thinkingRule{}, FromAPI: map[string]bool{}}
	for _, p := range file.Presets {
		var rules []thinkingRule
		switch {
		case len(p.Thinking) == 0:
		case string(p.Thinking) == `"models"`:
			out.FromAPI[p.ID] = true
		case json.Unmarshal(p.Thinking, &rules) == nil:
			for i := range rules {
				rules[i].re = regexp.MustCompile(rules[i].Match) // check.py compiled it; gen's test keeps the copy current
			}
			out.Rules[p.ID] = rules
		default:
			panic("presets.json: " + p.ID + ": thinking is neither rules nor \"models\"")
		}
	}
	return out
})

// levelOrder sorts a rule's levels as the API lists them.
func levelOrder(levels map[string]json.RawMessage) []string {
	out := []string{}
	for _, l := range dto.ThinkingLevels {
		if _, ok := levels[l]; ok {
			out = append(out, l)
		}
	}
	return out
}

// thinkingRuleOf is the rule a preset gives a model: a rule (fragments known), fromAPI true for a "models" preset (every
// level allowed, sent as reasoning {effort}), or neither.
func thinkingRuleOf(preset *string, model string) (rule *thinkingRule, fromAPI bool) {
	if preset == nil {
		return nil, false
	}
	t := thinking()
	if t.FromAPI[*preset] {
		return nil, true
	}
	for i, r := range t.Rules[*preset] {
		if r.re.MatchString(model) {
			return &t.Rules[*preset][i], false
		}
	}
	return nil, false
}

// apiLevels is a "models" preset's levels when its /models answer is not at hand (a saved provider).
var apiLevels = []string{"off", "low", "medium", "high"}

// thinkingInfo is what a model offers under its preset; ok false when no rule covers it.
func thinkingInfo(preset *string, model string) (dto.ThinkingInfo, bool) {
	rule, fromAPI := thinkingRuleOf(preset, model)
	switch {
	case fromAPI:
		return dto.ThinkingInfo{Levels: apiLevels}, true
	case rule == nil:
		return dto.ThinkingInfo{}, false
	case rule.Always:
		return dto.ThinkingInfo{Levels: []string{}, Always: true}, true
	}
	return dto.ThinkingInfo{Levels: levelOrder(rule.Levels), Default: rule.Default}, true
}

// thinkingLevels is Provider.thinking_levels: each saved model a rule covers (nil when none).
func thinkingLevels(preset *string, models []string) map[string]dto.ThinkingInfo {
	var out map[string]dto.ThinkingInfo
	for _, m := range models {
		if info, ok := thinkingInfo(preset, m); ok {
			if out == nil {
				out = map[string]dto.ThinkingInfo{}
			}
			out[m] = info
		}
	}
	return out
}

// thinkingProblem is a PUT's 422 for its thinking ("" when every level is one its model's rule allows).
func thinkingProblem(body *dto.ProviderPut) string {
	for _, m := range slices.Sorted(maps.Keys(body.Thinking)) {
		level := body.Thinking[m]
		if !slices.Contains(body.Models, m) {
			return fmt.Sprintf("thinking: %s is not one of models", m)
		}
		info, ok := thinkingInfo(body.Preset, m)
		if !ok || info.Always {
			return fmt.Sprintf("thinking: %s has no thinking setting", m)
		}
		if !slices.Contains(info.Levels, level) {
			return fmt.Sprintf("thinking: %s takes %s", m, strings.Join(info.Levels, ", "))
		}
	}
	return ""
}

// thinkingSnapshot is defs providerThinking for a provider's models: a saved level gives its fragment and tokens; a model
// with nothing saved whose rule has a default, or that always thinks, gets body {} and that level's tokens (nothing is
// sent, but the call has room for the provider's own thinking). nil when no model has any.
func thinkingSnapshot(preset *string, models []string, saved map[string]string) map[string]any {
	t := thinking()
	out := map[string]any{}
	for _, m := range models {
		rule, fromAPI := thinkingRuleOf(preset, m)
		level, body := saved[m], json.RawMessage(`{}`)
		switch {
		case fromAPI && level != "":
			effort := level
			if level == "off" {
				effort = "none"
			}
			body, _ = json.Marshal(map[string]any{"reasoning": map[string]string{"effort": effort}})
		case rule == nil:
			continue
		case rule.Always:
			level = "on"
		case level != "" && rule.Levels[level] != nil:
			body = rule.Levels[level]
		case rule.Default != "":
			level = rule.Default
		default:
			continue
		}
		out[m] = map[string]any{"level": level, "body": body, "tokens": t.Tokens[level]} // off: 0
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// testThinking is ProviderTest.thinking for a preset: the rules over the listed models, or for a "models" preset the
// reply's reasoning (its efforts among low, medium, high, plus off unless mandatory; fewer than two levels: none).
func testThinking(preset *string, reply message.ProviderTestReply) map[string]dto.ThinkingInfo {
	out := map[string]dto.ThinkingInfo{}
	if _, fromAPI := thinkingRuleOf(preset, ""); fromAPI {
		for m, r := range reply.Reasoning {
			if !slices.Contains(reply.Models, m) {
				continue
			}
			levels := []string{}
			for _, l := range apiLevels {
				if (l == "off" && !r.Mandatory) || (l != "off" && slices.Contains(r.Efforts, l)) {
					levels = append(levels, l)
				}
			}
			if len(levels) >= 2 {
				out[m] = dto.ThinkingInfo{Levels: levels}
			}
		}
	} else {
		for _, m := range reply.Models {
			if info, ok := thinkingInfo(preset, m); ok {
				out[m] = info
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
