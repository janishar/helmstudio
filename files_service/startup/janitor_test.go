package startup

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/helmstudio/orago/storage"
)

// Sweep deletes expired work files and archives only, whatever else lies under the owner.
func TestSweep(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	keys := []string{
		"u/a/catalog/s1/work/models/m.jpg", "u/a/work/x.png", "u/b/archives/z.zip", // expire
		"u/a/catalog/s1/images/i.png", "u/a/catalog/s1/raw/x/work/y.jpg", "u/a/catalog/s1/raw/r.png", // kept
	}
	for _, k := range keys {
		_ = store.Put(ctx, k, strings.NewReader("x"), 1, "image/png")
	}
	// two days on: archives expire, work files (30 days) don't yet
	if n, err := Sweep(ctx, store, time.Now().Add(48*time.Hour), 30*24*time.Hour); err != nil || n != 1 {
		t.Fatalf("after 2 days: %d %v", n, err)
	}
	if n, err := Sweep(ctx, store, time.Now().Add(31*24*time.Hour), 30*24*time.Hour); err != nil || n != 2 {
		t.Fatalf("after 31 days: %d %v", n, err)
	}
	var left []string
	for info := range store.List(ctx, "u/") {
		left = append(left, info.Key)
	}
	slices.Sort(left)
	if want := []string{"u/a/catalog/s1/images/i.png", "u/a/catalog/s1/raw/r.png", "u/a/catalog/s1/raw/x/work/y.jpg"}; !slices.Equal(left, want) {
		t.Fatal(left)
	}
}
