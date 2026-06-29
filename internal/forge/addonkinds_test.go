// SPDX-License-Identifier: AGPL-3.0-only

package forge

import (
	"testing"

	"github.com/rknightion/synthkit/internal/runner"
)

// TestAddonKindsAreRegistered guards the capture k8s collector's addon-detection table against drift:
// every construct kind that addon detection can emit MUST exist in the real registry, or the mapper
// would silently route a detected addon into a gap (or worse, produce an unloadable blueprint). The
// capture package can't import the registry (trust boundary), so this alignment check lives in forge.
func TestAddonKindsAreRegistered(t *testing.T) {
	reg := runner.Catalog()
	for _, kind := range []string{
		"karpenter", "cert_manager", "argocd", "core_dns",
		"load_balancer_controller", "external_dns", "ebs_csi", "vpc_cni", "envoy_gateway",
	} {
		if _, ok := reg.Construct(kind); !ok {
			t.Errorf("addon table references unregistered construct kind %q", kind)
		}
	}
}
