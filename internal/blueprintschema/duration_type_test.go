// SPDX-License-Identifier: AGPL-3.0-only

package blueprintschema

import (
	"testing"
	"time"
)

// TestDurationFieldsRenderAsDuration pins that time.Duration fields (decoded from strings such
// as "1h", never integers) are rendered as `duration` rather than the underlying `int64` kind.
func TestDurationFieldsRenderAsDuration(t *testing.T) {
	type cfg struct {
		Period  time.Duration   `yaml:"period"`
		Window  *time.Duration  `yaml:"window"`
		Phases  []time.Duration `yaml:"phases"`
		Plain   int64           `yaml:"plain"`
		Retries int             `yaml:"retries"`
	}
	got := map[string]Field{}
	for _, f := range walkConfig(cfg{}, nil) {
		got[f.Key] = f
	}
	for _, key := range []string{"period", "window", "phases"} {
		if got[key].Type != "duration" {
			t.Errorf("%s: type = %q, want %q", key, got[key].Type, "duration")
		}
	}
	if !got["phases"].Repeated {
		t.Error("phases: want Repeated")
	}
	if got["plain"].Type != "int" || got["retries"].Type != "int" {
		t.Errorf("plain ints must stay int, got %q and %q", got["plain"].Type, got["retries"].Type)
	}
}
