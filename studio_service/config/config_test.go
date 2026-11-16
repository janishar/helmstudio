package config

import "testing"

// The remote switch reads as pipelines_service reads it (config.REMOTE_MODELS).
func TestSwitchOn(t *testing.T) {
	for v, on := range map[string]bool{"on": true, "ON": true, "1": true, "true": true, "Yes": true, "off": false, "": false,
		"false": false, "0": false} {
		if SwitchOn(v) != on {
			t.Errorf("%q", v)
		}
	}
}
