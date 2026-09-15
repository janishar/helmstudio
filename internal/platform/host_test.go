package platform

import (
	"slices"
	"testing"
)

func TestFreeDiskBytesReadsTheVolume(t *testing.T) {
	n, err := FreeDiskBytes(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("free space on the temp volume reads as zero")
	}
	if _, err := FreeDiskBytes("/no/such/path/for/helmstudio"); err == nil {
		t.Fatal("a missing path should be an error, not zero bytes free")
	}
}

func TestHostBackendsAlwaysIncludeCPUAndNeverCUDA(t *testing.T) {
	b := HostBackends()
	if !slices.Contains(b, "cpu") || slices.Contains(b, "cuda") || slices.Contains(b, "rocm") {
		t.Fatalf("HostBackends() = %v", b)
	}
	if HostOS() == "darwin" && HostArch() == "arm64" && !slices.Contains(b, "metal") {
		t.Fatalf("Apple Silicon without metal: %v", b)
	}
}
