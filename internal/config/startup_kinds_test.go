// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"reflect"
	"testing"
)

func TestStartupDisabledKindsParsing(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want []string
	}{
		{"unset", "", nil},
		{"blank", "STARTUP_DISABLED_KINDS=\n", nil},
		{"single", "STARTUP_DISABLED_KINDS=fleet_management\n", []string{"fleet_management"}},
		{"list keeps first-seen order", "STARTUP_DISABLED_KINDS=synthetic_monitoring,fleet_management\n", []string{"synthetic_monitoring", "fleet_management"}},
		{"spaces duplicates and empty entries", "STARTUP_DISABLED_KINDS= fleet_management , ,synthetic_monitoring,fleet_management,\n", []string{"fleet_management", "synthetic_monitoring"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STARTUP_DISABLED_KINDS", "")
			if err := os.Unsetenv("STARTUP_DISABLED_KINDS"); err != nil { // the env file under test must be the only source
				t.Fatal(err)
			}
			cfg, err := Load(writeEnv(t, tc.env))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.StartupDisabledKinds, tc.want) {
				t.Fatalf("StartupDisabledKinds = %#v, want %#v", cfg.StartupDisabledKinds, tc.want)
			}
		})
	}
}
