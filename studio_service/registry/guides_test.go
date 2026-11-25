package registry

import (
	"testing"
	"time"
)

func guidesKey(published time.Time, card string, families ...string) []byte {
	out := `{"published_at": "` + published.Format(time.RFC3339) + `", "guides": [`
	for i, f := range families {
		if i > 0 {
			out += ","
		}
		out += `{"family": "` + f + `", "title": "` + f + `", "card": "` + card + ` ` + f + `", "guide": "## Card", "recipes": []}`
	}
	return []byte(out + `]}`)
}

// API 1.71: studio keeps the newest guide per family over the known hosts; the cards are the held families' only,
// sorted by family; a host's guides lapse with its hosts key.
func TestGuidesNewestPerFamilyAndCardsOfHeldFamiliesOnly(t *testing.T) {
	var r Registry
	now := time.Now().UTC().Add(-time.Minute)
	_ = r.SetRegistry("mac.torch.guides", guidesKey(now, "old", "qwen-image", "minimax-h3-image"))
	_ = r.SetRegistry("gcp.torch.guides", guidesKey(now.Add(time.Second), "new", "qwen-image"))
	_ = r.SetRegistry("mac.fake.guides", guidesKey(now, "fake", "echo"))
	_ = r.SetRegistry("mac.torch.models", []byte(`{"models": [
		{"id": "qwen-image-2.1", "family": "qwen-image", "variants": [{"id": "base", "presence": "missing", "precisions": ["bf16"]},
			{"id": "turbo", "presence": "present", "precisions": ["bf16"]}]},
		{"id": "minimax-h3-image", "family": "minimax-h3-image", "variants": [{"id": "base", "presence": "partial", "precisions": ["int8"]}]}]}`))
	_ = r.SetRegistry("mac.fake.models", []byte(`{"models": [{"id": "echo", "family": "echo"}]}`))
	if all, _ := r.Guides(); len(all) != 0 || len(r.Cards()) != 0 {
		t.Fatal("no hosts key: nothing read")
	}
	present(&r, "mac.torch", "gcp.torch", "mac.fake")
	all, held := r.Guides()
	if len(all) != 3 || all["qwen-image"].Card != "new qwen-image" || !held["qwen-image"] || held["minimax-h3-image"] || !held["echo"] {
		t.Fatalf("guides %v, held %v", all, held)
	}
	cards := r.Cards()
	if len(cards) != 2 || cards[0].Family != "echo" || cards[1].Family != "qwen-image" || cards[1].Card != "new qwen-image" {
		t.Fatalf("cards %v", cards)
	}
	for range 3 { // the same order every time: the prompt's start stays byte-identical
		if again := r.Cards(); again[0].Family != "echo" || again[1].Card != cards[1].Card {
			t.Fatal("cards moved")
		}
	}
	_ = r.SetHost("gcp.torch", nil)
	if all, _ := r.Guides(); all["qwen-image"].Card != "old qwen-image" {
		t.Fatal("gcp's guide lapses with its hosts key")
	}
	_ = r.SetRegistry("mac.torch.guides", nil)
	if all, _ := r.Guides(); len(all) != 1 {
		t.Fatal("a deleted key drops its guides")
	}
}
