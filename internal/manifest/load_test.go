package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// Load accepts exactly what Validate accepts, and returns the fields the
// supervisor runs on.
func TestLoadReturnsTheTypedManifest(t *testing.T) {
	m, res, err := Load("../../studios/h3-studio.yaml")
	if err != nil || !res.OK() {
		t.Fatalf("Load: %v %v", err, res.Errors)
	}
	p := m.EffectiveProcesses()[0]
	if p.Port == nil || p.Port.Prefer != 8710 || p.Health == nil || !p.Health.TCP || p.Health.EffectiveTimeoutS() != 30 ||
		p.Busy == nil || p.Busy.Path != "/api/queue" || !p.Heavy || p.UI != "/" || p.EffectiveShell() != "sh" ||
		p.EffectiveRestart() != "never" || !p.EffectiveAutostart() {
		t.Fatalf("process decoded as %+v", p)
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(bad, []byte("id: x\n"), 0o600)
	m, res, err = Load(bad)
	if err != nil || res.OK() || m != nil {
		t.Fatalf("Load of an invalid manifest: m=%v ok=%v err=%v; want nil and errors", m, res.OK(), err)
	}
}

func TestHealthDefaults(t *testing.T) {
	if h := (Health{}); h.EffectiveTimeoutS() != 180 || h.EffectiveIntervalS() != 2 {
		t.Fatalf("defaults = %d, %d; want 180 and 2 (schema/manifest.json)", h.EffectiveTimeoutS(), h.EffectiveIntervalS())
	}
}
