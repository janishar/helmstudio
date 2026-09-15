// Package platformtest gives tests an isolated directory tree. A test that
// writes outside the tree it gets from here is a bug in the test.
package platformtest

import (
	"testing"

	"github.com/janishar/helmstudio/internal/platform"
)

// Dirs returns every root resolved beneath a fresh t.TempDir(), created and
// removed with the test.
func Dirs(t testing.TB) *platform.Dirs {
	t.Helper()
	d, err := platform.Under(t.TempDir())
	if err != nil {
		t.Fatalf("resolving a temp directory tree: %v", err)
	}
	if err := d.Ensure(); err != nil {
		t.Fatalf("creating a temp directory tree: %v", err)
	}
	return d
}
