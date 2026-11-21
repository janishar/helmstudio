package studio

import (
	"testing"
	"time"

	"github.com/helmstudio/studio_service/api/studio/dto"
	"github.com/helmstudio/studio_service/registry"
)

// A host publishing the model but not a variant (API 1.33: base-fp8 is published on CUDA only) is not on that
// variant's hosts: no "missing" state, so no Download button there.
func TestBuildModelsHostsOfAVariant(t *testing.T) {
	now := time.Now()
	values := []registry.Host{{Host: "gcp", Env: "torch", UpdatedAt: now}, {Host: "mac", Env: "torch", UpdatedAt: now}}
	published := map[string]registry.Published{
		"gcp.torch": {Models: []registry.Model{{ID: "q", Family: "qwen-image", Variants: []registry.Variant{
			{ID: "base", Presence: "missing"}, {ID: "base-fp8", Presence: "present"}}}}},
		"mac.torch": {Models: []registry.Model{{ID: "q", Family: "qwen-image", Variants: []registry.Variant{
			{ID: "base", Presence: "present"}}}}},
	}
	hosts := map[string][]string{}
	for _, v := range buildModels(values, published, map[string]dto.VariantJob{}, now, false).Models[0].Variants {
		for _, h := range v.Hosts {
			hosts[v.ID] = append(hosts[v.ID], h.Host+" "+h.State)
		}
	}
	if len(hosts["base"]) != 2 || len(hosts["base-fp8"]) != 1 || hosts["base-fp8"][0] != "gcp present" {
		t.Fatal(hosts)
	}
}
