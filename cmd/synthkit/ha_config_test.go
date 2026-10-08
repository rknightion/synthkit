// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"github.com/rknightion/synthkit/internal/config"
	"testing"
	"time"
)

func TestHAConfigRejectsEqualityAndWholeFlushOvershoot(t *testing.T) {
	t.Setenv("HA_MODE", "lease")
	t.Setenv("HA_NAMESPACE", "test")
	t.Setenv("HA_LEASE_NAME", "lease")
	t.Setenv("POD_UID", "pod")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HAFlushTimeout != 8*time.Second || cfg.HAHTTPTimeout != 5*time.Second || cfg.SendDrainDeadline != 10*time.Second {
		t.Fatal("frozen HA defaults drifted")
	}
	for _, tc := range []struct{ key, value string }{{"HA_FLUSH_TIMEOUT", "13s"}, {"HA_HTTP_TIMEOUT", "10s"}, {"HA_LEASE_DURATION", "30.5s"}, {"HA_KUBE_REQUEST_TIMEOUT", "15s"}, {"HA_RETRY_PERIOD", "12.5s"}, {"SEND_DRAIN_DEADLINE", "0s"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := config.Load(""); err == nil {
				t.Fatalf("accepted equality/overshoot %s=%s", tc.key, tc.value)
			}
		})
	}
	t.Setenv("SEND_DRAIN_DEADLINE", "9s")
	cfg, err = config.Load("")
	if err != nil || cfg.SendDrainDeadline != 9*time.Second {
		t.Fatal("explicit drain override clamped", err)
	}
}
