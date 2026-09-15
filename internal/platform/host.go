package platform

import "runtime"

// HostOS is the operating system in the vocabulary of the manifest's
// requires.os (schema/manifest.json): darwin, linux or windows.
func HostOS() string { return runtime.GOOS }

// HostArch is the CPU architecture in the vocabulary of requires.arch: arm64
// or amd64.
func HostArch() string { return runtime.GOARCH }

// HostBackends lists the compute backends this machine can provide, in the
// vocabulary of runtime.backends (docs/design/01-prd.md R5a). A studio whose
// backends are all absent here is blocked from installing, not warned.
//
// Nothing is probed: Apple Silicon always has Metal, and MPS is PyTorch's
// Metal backend. CUDA and ROCm are never reported, because no platform
// helmstudio builds for today can offer them (01-prd.md §14); detecting them
// is Linux-port work. CPU is always available.
func HostBackends() []string {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return []string{"metal", "mps", "cpu"}
	}
	return []string{"cpu"}
}
