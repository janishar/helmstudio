package weights

import "testing"

func TestMatchIsPerSegment(t *testing.T) {
	cases := []struct {
		pattern, file string
		want          bool
	}{
		{"config.json", "config.json", true},
		{"config.json", "sub/config.json", false},
		{"*.safetensors", "model-00001.safetensors", true},
		{"*.safetensors", "text_encoder/model.safetensors", false},
		{"FL2VA/**", "FL2VA/dit/model.safetensors", true},
		{"FL2VA/**", "FL2VA/config.json", true},
		{"FL2VA/**", "Ref2VA/config.json", false},
		{"FL2VA/**", "FL2VA", true}, // ** matches zero segments
		{"**/*.json", "config.json", true},
		{"**/*.json", "a/b/c.json", true},
		{"**/*.json", "a/b/c.bin", false},
		{"diffusion_models/ltx-2.5-22b-dev-transformer-bf16.safetensors", "diffusion_models/ltx-2.5-22b-dev-transformer-bf16.safetensors", true},
		{"a/*/c", "a/b/c", true},
		{"a/*/c", "a/b/x/c", false},
		{"a?c", "abc", true},
		{"[", "[", false}, // a malformed pattern matches nothing
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.file); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.file, got, c.want)
		}
	}
	if !matchesAny(nil, "anything/at/all") {
		t.Error("no patterns should mean the whole repository")
	}
}
