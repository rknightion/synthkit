// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

var (
	dockerBootIDPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dockerMachineIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// TestDockerMachineIdentityMatchesCapturedPlacement pins the standalone cAdvisor
// descriptor: boot_id and machine_id are labels on machine_memory_bytes only. The
// committed descriptor elides the values, so this test checks shape and presence,
// while the construct derives stable synthetic values from its declared seed.
func TestDockerMachineIdentityMatchesCapturedPlacement(t *testing.T) {
	h := &fixture.Host{Hostname: "docker-example", OS: "linux", Profile: "integration", Docker: true}
	series := tickHost(t, h)

	if err := validateDockerMachineIdentity(series); err != nil {
		t.Fatal(err)
	}
	for _, sample := range series {
		if sample.Name == "machine_memory_bytes" {
			t.Logf("machine_memory_bytes labels=%v value=%v", sample.Labels, sample.Value)
			break
		}
	}
}

// TestDockerMachineIdentityMutationControls proves that the placement guard catches
// machine identity leaking to container siblings or the other machine lane. These
// mutations are test-only and never reach the emitter.
func TestDockerMachineIdentityMutationControls(t *testing.T) {
	h := &fixture.Host{Hostname: "docker-example", OS: "linux", Profile: "integration", Docker: true}
	original := tickHost(t, h)
	if err := rejectMachineIdentityOutsideMemory(original); err != nil {
		t.Fatalf("unmutated Docker envelope rejected: %v", err)
	}

	tests := []struct {
		name    string
		metric  string
		label   string
		wantErr string
	}{
		{
			name:    "container sibling boot_id",
			metric:  "container_cpu_usage_seconds_total",
			label:   "boot_id",
			wantErr: "container_cpu_usage_seconds_total carries boot_id",
		},
		{
			name:    "machine scrape sibling machine_id",
			metric:  "machine_scrape_error",
			label:   "machine_id",
			wantErr: "machine_scrape_error carries machine_id",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated := clonePromSeries(original)
			for i := range mutated {
				if mutated[i].Name == tc.metric {
					mutated[i].Labels[tc.label] = "synthetic-mutation"
					break
				}
			}
			err := rejectMachineIdentityOutsideMemory(mutated)
			if err == nil {
				t.Fatalf("mutation of %s was accepted", tc.metric)
			}
			t.Logf("rejected mutation: %v", err)
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("mutation error = %q, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// TestNonDockerMachineIdentityMutationControl keeps the host OS exporters free of
// cAdvisor machine identity. It also proves a future accidental label addition to
// a non-Docker series is caught by the same negative-control style.
func TestNonDockerMachineIdentityMutationControl(t *testing.T) {
	for _, tc := range []struct {
		name   string
		os     string
		metric string
	}{
		{name: "linux", os: "linux", metric: "node_cpu_seconds_total"},
		{name: "macos", os: "darwin", metric: "node_memory_total_bytes"},
		{name: "windows", os: "windows", metric: "windows_cpu_time_total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fixture.Host{Hostname: tc.name + "-no-docker", OS: tc.os, Profile: "integration"}
			original := tickHost(t, h)
			if err := rejectAnyMachineIdentity(original); err != nil {
				t.Fatalf("unmutated non-Docker envelope rejected: %v", err)
			}

			mutated := clonePromSeries(original)
			var found bool
			for i := range mutated {
				if mutated[i].Name == tc.metric {
					mutated[i].Labels["boot_id"] = "synthetic-mutation"
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("non-Docker host did not emit %s to mutate", tc.metric)
			}
			err := rejectAnyMachineIdentity(mutated)
			if err == nil {
				t.Fatal("non-Docker machine identity mutation was accepted")
			} else if !strings.Contains(err.Error(), "boot_id") {
				t.Fatalf("non-Docker mutation error = %q, want boot_id", err)
			} else {
				t.Logf("rejected mutation: %v", err)
			}
		})
	}
}

func validateDockerMachineIdentity(series []promrw.Series) error {
	var memory, memoryError, up *promrw.Series
	for i := range series {
		switch series[i].Name {
		case "machine_memory_bytes":
			memory = &series[i]
		case "machine_scrape_error":
			memoryError = &series[i]
		case "up":
			up = &series[i]
		}
	}
	if memory == nil {
		return fmt.Errorf("Docker envelope missing machine_memory_bytes")
	}
	if memory.Labels["job"] != "integrations/docker" || memory.Labels["instance"] == "" {
		return fmt.Errorf("machine_memory_bytes has wrong Docker identity labels: %v", memory.Labels)
	}
	if !dockerBootIDPattern.MatchString(memory.Labels["boot_id"]) {
		return fmt.Errorf("machine_memory_bytes boot_id = %q, want UUID-shaped cAdvisor machine identity", memory.Labels["boot_id"])
	}
	if !dockerMachineIDPattern.MatchString(memory.Labels["machine_id"]) {
		return fmt.Errorf("machine_memory_bytes machine_id = %q, want 32-hex machine identity", memory.Labels["machine_id"])
	}
	if _, ok := memory.Labels["system_uuid"]; ok {
		return fmt.Errorf("machine_memory_bytes unexpectedly carries system_uuid: %v", memory.Labels)
	}
	if memoryError == nil || up == nil {
		return fmt.Errorf("Docker envelope missing machine_scrape_error or up")
	}
	if err := rejectMachineIdentityOutsideMemory(series); err != nil {
		return err
	}
	return nil
}

func rejectMachineIdentityOutsideMemory(series []promrw.Series) error {
	for _, series := range series {
		if series.Name == "machine_memory_bytes" {
			continue
		}
		for _, label := range []string{"boot_id", "machine_id"} {
			if _, ok := series.Labels[label]; ok {
				return fmt.Errorf("%s carries %s outside machine_memory_bytes", series.Name, label)
			}
		}
	}
	return nil
}

func rejectAnyMachineIdentity(series []promrw.Series) error {
	for _, series := range series {
		for _, label := range []string{"boot_id", "machine_id"} {
			if _, ok := series.Labels[label]; ok {
				return fmt.Errorf("non-Docker series %s carries %s", series.Name, label)
			}
		}
	}
	return nil
}

func clonePromSeries(series []promrw.Series) []promrw.Series {
	cloned := make([]promrw.Series, len(series))
	for i, original := range series {
		cloned[i] = original
		cloned[i].Labels = make(map[string]string, len(original.Labels))
		for key, value := range original.Labels {
			cloned[i].Labels[key] = value
		}
	}
	return cloned
}
