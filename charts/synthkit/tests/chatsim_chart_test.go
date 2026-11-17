// SPDX-License-Identifier: AGPL-3.0-only
package charttest

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var chatsimOn = []string{"--set", "chatsim.enabled=true", "--set", "chatsim.configMap=story", "--set", "chatsim.existingSecret=story-tokens"}

func chatsimContainer(t *testing.T, objects []map[string]any) map[string]any {
	t.Helper()
	pod := mapping(mapping(mapping(object(t, objects, "Deployment")["spec"])["template"])["spec"])
	for _, c := range list(pod["containers"]) {
		if mapping(c)["name"] == "chatsim" {
			return mapping(c)
		}
	}
	return nil
}

func TestChatsimOffByDefault(t *testing.T) {
	if c := chatsimContainer(t, renderHA(t)); c != nil {
		t.Fatalf("default render has a chatsim container: %v", c)
	}
}

func TestChatsimSidecar(t *testing.T) {
	objects := renderHA(t, append(chatsimOn, "--set", "chatsim.seedKey=seed.yaml")...)
	c := chatsimContainer(t, objects)
	if c == nil {
		t.Fatal("no chatsim container")
	}
	pod := mapping(mapping(mapping(object(t, objects, "Deployment")["spec"])["template"])["spec"])
	emitter := mapping(list(pod["containers"])[0])
	equal(t, "same image as the emitter", c["image"], emitter["image"])
	equal(t, "binary", c["command"], []any{"/app/synthkit-chat"})
	equal(t, "args", c["args"], []any{"run", "-scenario", "/etc/synthkit/chat/scenario.chat.yaml",
		"-bindings", "/etc/synthkit/chat/bindings.yaml", "-seed", "/etc/synthkit/chat/seed.yaml"})
	equal(t, "writable journal dir", c["workingDir"], "/tmp")
	equal(t, "tokens from the operator Secret", c["envFrom"], []any{map[string]any{"secretRef": map[string]any{"name": "story-tokens"}}})
	equal(t, "read-only root", mapping(c["securityContext"])["readOnlyRootFilesystem"], true)
	equal(t, "single replica", mapping(object(t, objects, "Deployment")["spec"])["replicas"], 1)
	var cm bool
	for _, v := range list(pod["volumes"]) {
		if mapping(v)["name"] == "chatsim-config" {
			cm = mapping(mapping(v)["configMap"])["name"] == "story"
		}
	}
	if !cm {
		t.Fatal("chatsim-config volume does not project the operator ConfigMap")
	}
	noSeed := chatsimContainer(t, renderHA(t, chatsimOn...))
	if strings.Contains(strings.Join(anyStrings(list(noSeed["args"])), " "), "-seed") {
		t.Fatal("seed flag rendered without seedKey")
	}
}

func TestChatsimInvalid(t *testing.T) {
	cases := map[string][]string{
		"requires chatsim.configMap":      {"--set", "chatsim.enabled=true", "--set", "chatsim.existingSecret=s"},
		"requires chatsim.existingSecret": {"--set", "chatsim.enabled=true", "--set", "chatsim.configMap=c"},
		"must be distinct":                append(chatsimOn, "--set", "chatsim.seedKey=bindings.yaml"),
		"single pod":                      append(chatsimOn, "-f", filepath.Join(chartPath(t), "ci-ha", "11-ha-values.yaml")),
	}
	for want, args := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		out, err := exec.CommandContext(ctx, "helm", append([]string{"template", "synthkit-test", chartPath(t), "--kube-version", "1.31.0"}, args...)...).CombinedOutput()
		cancel()
		if err == nil || !strings.Contains(string(out), want) {
			t.Errorf("%s: err=%v\n%s", want, err, out)
		}
	}
}

func anyStrings(xs []any) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, x.(string))
	}
	return out
}
