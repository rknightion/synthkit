// SPDX-License-Identifier: AGPL-3.0-only
package charttest

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHAFleetChart(t *testing.T) {
	fixture := filepath.Join(chartPath(t), "ci-ha", "12-ha-fleet-values.yaml")
	for _, upgrade := range []bool{false, true} {
		args := []string{"-f", fixture}
		if upgrade {
			args = append(args, "--is-upgrade")
		}
		objects := renderHA(t, args...)
		runtime := object(t, objects, "ConfigMap")
		equal(t, "Fleet mapping", mapping(runtime["data"])["STATE_FLEET_CONFIGMAP"], "synthkit-fleet")
		equal(t, "exact named HA rules", object(t, objects, "Role")["rules"], []any{
			map[string]any{"apiGroups": []any{"coordination.k8s.io"}, "resources": []any{"leases"}, "resourceNames": []any{"synthkit-election"}, "verbs": []any{"get", "update", "patch"}},
			map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps"}, "resourceNames": []any{"synthkit-control", "synthkit-boot", "synthkit-fleet", "synthkit-source"}, "verbs": []any{"get", "update", "patch"}},
		})
		for _, obj := range objects {
			if obj["kind"] == "Lease" || (obj["kind"] == "ConfigMap" && mapping(obj["metadata"])["name"] != "synthkit-test") {
				t.Fatal("Helm owns mutable state", obj)
			}
		}
	}
	// Fleet credentials with HA off retain the ordinary file-state path, without RBAC.
	objects := renderHA(t, "-f", fixture, "--set", "ha.enabled=false,persistence.enabled=true", "--kube-version", "1.25.0")
	equal(t, "HA-off Fleet mapping absent", mapping(object(t, objects, "ConfigMap")["data"])["STATE_FLEET_CONFIGMAP"], nil)
	equal(t, "HA-off replicas", mapping(object(t, objects, "Deployment")["spec"])["replicas"], 1)
	object(t, objects, "PersistentVolumeClaim")
	for _, obj := range objects {
		if obj["kind"] == "Role" {
			t.Fatal("HA-off grants API access")
		}
	}
}

func TestHAFleetInvalidChart(t *testing.T) {
	fixture := filepath.Join(chartPath(t), "ci-ha", "11-ha-values.yaml")
	for _, name := range []string{"", "synthkit-test", "synthkit-control", "synthkit-boot", "synthkit-source", "synthkit-election", "Bad_Name", strings.Repeat("a", 64)} {
		t.Run("name="+name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			args := []string{"template", "synthkit-test", chartPath(t), "--kube-version", "1.31.0", "-f", fixture, "--set", "credentials.fm.existingSecret=synthkit-fm,credentials.fm.keys.GC_FM_TOKEN=GC_FM_TOKEN"}
			if name != "" {
				args = append(args, "--set", "ha.fleetConfigMap="+name)
			}
			out, err := exec.CommandContext(ctx, "helm", args...).CombinedOutput()
			if err == nil {
				t.Fatal("invalid Fleet mapping rendered")
			}
			if !bytes.Contains(out, []byte("execution error")) && !bytes.Contains(out, []byte("specifications of the schema")) {
				t.Fatalf("unexpected failure %v: %s", err, out)
			}
		})
	}
}

func TestHAFleetSourceInputCollision(t *testing.T) {
	fixture := filepath.Join(chartPath(t), "ci-ha", "12-ha-fleet-values.yaml")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "helm", "template", "synthkit-test", chartPath(t), "--kube-version", "1.31.0", "-f", fixture, "--set", "blueprintSources.enabled=true,ha.fleetConfigMap=synthkit-test-git-sources").CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("input must be separate from mutable state ConfigMaps")) {
		t.Fatalf("mutable Fleet slot admitted as source input: %v %s", err, out)
	}
}
