package dto

import (
	"strings"
	"testing"

	"github.com/helmstudio/studio_service/api/catalog/model"
)

// An item's label and detail are capped in characters, as the contract's maxLength counts, not bytes: a 200-character
// detail of "·" and "…" (2 and 3 bytes each) is valid, 201 characters are not.
func TestItemLimitsCountCharacters(t *testing.T) {
	detail := strings.Repeat("·", 199) + "…"
	item := model.Item{ID: "x", Option: "pick", Label: strings.Repeat("é", 200), Detail: &detail}
	if msg := ValidateItem(item); msg != "" {
		t.Fatalf("200 characters rejected: %s", msg)
	}
	longer := detail + "·"
	item.Detail = &longer
	if ValidateItem(item) == "" {
		t.Fatal("201 characters accepted")
	}
}
