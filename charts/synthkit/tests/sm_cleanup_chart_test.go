// SPDX-License-Identifier: AGPL-3.0-only
package charttest

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const cleanupPlan = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// smJobContainer renders the chart and returns the sm-provision Job container.
func smJobContainer(t *testing.T, args ...string) map[string]any {
	t.Helper()
	return mapping(list(smJobPod(t, renderHA(t, args...))["containers"])[0])
}

func containerEnv(container map[string]any) map[string]any {
	out := map[string]any{}
	for _, entry := range list(container["env"]) {
		e := mapping(entry)
		out[e["name"].(string)] = e
	}
	return out
}

// The cleanup mode is values-gated and off by default; it changes only the Job's arguments, so
// the preview and the confirmed apply keep exactly the provisioner's least-privilege environment.
func TestSMCleanupJobMode(t *testing.T) {
	for name, fixture := range map[string]string{
		"kubernetes": smFixture(t),
		"file":       filepath.Join(chartPath(t), "ci", "08-sm-provision-values.yaml"),
	} {
		t.Run(name, func(t *testing.T) {
			ordinary := smJobContainer(t, "-f", fixture)
			if _, ok := ordinary["args"]; ok {
				t.Fatalf("default Job passes arguments: %v", ordinary["args"])
			}
			ordinaryApply := smJobContainer(t, "-f", fixture, "--set", "smProvision.apply=true")
			for _, tc := range []struct {
				name    string
				set     string
				args    []any
				compare map[string]any
			}{
				{"preview", "smProvision.cleanup.enabled=true", []any{"cleanup"}, ordinary},
				{"confirmed apply", "smProvision.cleanup.enabled=true,smProvision.apply=true,smProvision.cleanup.confirm=" + cleanupPlan,
					[]any{"cleanup", "-confirm=" + cleanupPlan}, ordinaryApply},
			} {
				container := smJobContainer(t, "-f", fixture, "--set", tc.set)
				equal(t, tc.name+" arguments", container["args"], tc.args)
				equal(t, tc.name+" entrypoint", container["command"], []any{"/app/sm-provision"})
				if !reflect.DeepEqual(containerEnv(container), containerEnv(tc.compare)) {
					t.Fatalf("%s changed the provisioner environment", tc.name)
				}
			}
		})
	}
}

func TestSMCleanupJobInvalidValues(t *testing.T) {
	const schema = "specifications of the schema"
	for _, tc := range []struct{ set, want string }{
		{"smProvision.cleanup.enabled=true,smProvision.apply=true", "requires smProvision.cleanup.confirm"},
		{"smProvision.cleanup.confirm=" + cleanupPlan, "smProvision.cleanup.confirm is only valid"},
		{"smProvision.cleanup.enabled=true,smProvision.cleanup.confirm=" + cleanupPlan, "smProvision.cleanup.confirm is only valid"},
		{"smProvision.apply=true,smProvision.cleanup.confirm=" + cleanupPlan, "smProvision.cleanup.confirm is only valid"},
		{"smProvision.cleanup.enabled=true,smProvision.apply=true,smProvision.cleanup.confirm=" + strings.ToUpper(cleanupPlan), schema},
		{"smProvision.cleanup.enabled=true,smProvision.apply=true,smProvision.cleanup.confirm=" + cleanupPlan[:63], schema},
		{"smProvision.cleanup.enabled=true,smProvision.adoptLegacy=true", "cleanup cannot be combined"},
		{"smProvision.cleanup.enabled=true,smProvision.migrateTarget=true", "cleanup cannot be combined"},
		{"smProvision.cleanup.unknown=true", schema},
	} {
		t.Run(tc.set, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "helm", "template", "synthkit-test", chartPath(t), "--kube-version", "1.31.0", "-f", smFixture(t), "--set", tc.set).CombinedOutput()
			if err == nil || !bytes.Contains(out, []byte(tc.want)) {
				t.Fatalf("expected rejection %q: %v\n%s", tc.want, err, out)
			}
		})
	}
}

// A release whose stored values predate smProvision.cleanup (helm upgrade --reuse-values) still
// renders, NOTES included, as an ordinary provisioning Job.
func TestSMCleanupAbsentValuesRender(t *testing.T) {
	for name, fixture := range map[string]string{
		"kubernetes": smFixture(t),
		"file":       filepath.Join(chartPath(t), "ci", "08-sm-provision-values.yaml"),
	} {
		for _, apply := range []string{"false", "true"} {
			t.Run(name+"/apply="+apply, func(t *testing.T) {
				container := smJobContainer(t, "-f", fixture, "--set", "smProvision.cleanup=null", "--set", "smProvision.apply="+apply)
				if _, ok := container["args"]; ok {
					t.Fatalf("absent cleanup values passed arguments: %v", container["args"])
				}
			})
		}
	}
}
