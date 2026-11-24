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
