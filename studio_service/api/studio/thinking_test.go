package studio

import (
	"fmt"
	"strings"
	"testing"
)

// AssistantStatus.thinks (plan 29): from the saved level, the rule's default, always thinks; false without a rule.
func TestModelThinks(t *testing.T) {
	deepseek, anthropic, ollama, openrouter, bridge := "deepseek", "anthropic", "ollama", "openrouter", "claude-code-bridge"
	for _, c := range []struct {
		name   string
		preset *string
		model  string
		saved  map[string]string
		want   bool
	}{
		{"level high", &deepseek, "deepseek-flash", map[string]string{"deepseek-flash": "high"}, true},
		{"level off", &deepseek, "deepseek-flash", map[string]string{"deepseek-flash": "off"}, false},
		{"nothing saved, default high", &deepseek, "deepseek-flash", nil, true},
		{"always thinks", &anthropic, "claude-opus-5", nil, true},
		{"no rule for the model", &deepseek, "deepseek-chat", nil, false},
		{"rule without default, nothing saved", &ollama, "qwen3:8b", nil, false},
		{"rule without default, on", &ollama, "qwen3:8b", map[string]string{"qwen3:8b": "on"}, true},
		{"models preset, nothing saved", &openrouter, "x/y", nil, false},
		{"models preset, low", &openrouter, "x/y", map[string]string{"x/y": "low"}, true},
		{"models preset, off", &openrouter, "x/y", map[string]string{"x/y": "off"}, false},
		{"no preset", nil, "deepseek-flash", map[string]string{"deepseek-flash": "high"}, false},
		{"bridge, high (API 1.61)", &bridge, "claude-opus", map[string]string{"claude-opus": "high"}, true},
		{"bridge, nothing saved", &bridge, "claude-opus", nil, false},
	} {
		if got := modelThinks(c.preset, c.model, c.saved); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

// API 1.61: the bridge plugin's preset gives each of its models low, medium and high (claude -p --effort).
func TestBridgeThinkingLevels(t *testing.T) {
	bridge := "claude-code-bridge"
	got := thinkingLevels(&bridge, []string{"claude-sonnet", "claude-opus", "claude-haiku", "other"})
	for _, m := range []string{"claude-sonnet", "claude-opus", "claude-haiku"} {
		if fmt.Sprint(got[m].Levels) != "[low medium high]" {
			t.Errorf("%s: %v", m, got[m])
		}
	}
	if _, ok := got["other"]; ok {
		t.Error("a model the rule does not match has no control")
	}
	snap := thinkingSnapshot(&bridge, []string{"claude-opus"}, map[string]string{"claude-opus": "medium"})
	if !strings.Contains(fmt.Sprint(snap), `"reasoning_effort": "medium"`) {
		t.Error(snap)
	}
}

// AssistantStatus.sees (API 1.65): the preset's vision expression on the model id; no preset or none in it: false.
// A message's text to pipelines carries its attached images as a last line of refs.
func TestModelSeesAndWithTakes(t *testing.T) {
	deepseek, anthropic, ollama := "deepseek", "anthropic", "ollama"
	for _, c := range []struct {
		preset *string
		model  string
		want   bool
	}{
		{&anthropic, "claude-sonnet-5-5", true},
		{&deepseek, "deepseek-flash", false},
		{&ollama, "llava:13b", true},
		{&ollama, "qwen3:8b", false},
		{nil, "gpt-6", false},
	} {
		if got := modelSees(c.preset, c.model); got != c.want {
			t.Errorf("%v %s: got %v", c.preset, c.model, got)
		}
	}
	if got := withTakes("Use this", []string{"a", "b"}); got != "Use this\n\n(attached: [[take:a]] [[take:b]])" {
		t.Errorf("withTakes: %q", got)
	}
	if got := withTakes("Hi", nil); got != "Hi" {
		t.Errorf("withTakes none: %q", got)
	}
}
