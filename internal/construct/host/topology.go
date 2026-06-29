// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"hash/fnv"
	"math"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/nodeexp"
)

// Default magnitudes applied when a fixture.Host leaves a field zero. These are
// declaration defaults (the blueprint resolver also applies them) repeated here so the
// construct is self-consistent when driven directly from a fixture.
const (
	defaultNumCPU   = 2
	defaultMemTotal = 8 << 30 // 8 GiB
)

// hashUnit maps key to a stable uniform value in [0,1) via FNV-1a. Deterministic; no
// global rand (invariant I12 — all derived identity keys on the seed).
func hashUnit(key string) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return float64(h.Sum64()) / (float64(math.MaxUint64) + 1)
}

// hashSpan maps key uniformly into [lo, hi].
func hashSpan(key string, lo, hi int64) int64 {
	if hi <= lo {
		return lo
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	span := uint64(hi-lo) + 1
	return lo + int64(h.Sum64()%span)
}

// jobFor returns the Prometheus `job` label for a host's OS exporter. macOS accepts both
// "darwin" (the fixture normal form) and "macos" (defensive).
func jobFor(os string) string {
	switch os {
	case "windows":
		return "integrations/windows_exporter" // UNDERSCORE (standalone form, not the k8s hyphen variant)
	case "darwin", "macos":
		return "integrations/macos-node"
	default: // linux
		return "integrations/node_exporter"
	}
}

// baseLabels returns the substrate identity stamped on every metric series for this host's
// primary exporter: {job, instance}. PrivateIP is NOT a base label (it rides on the
// node_network_info series the emitter builds, not on every series). No blueprint label —
// ScopeSubstrate.
func baseLabels(h *fixture.Host) map[string]string {
	return map[string]string{"job": jobFor(h.OS), "instance": h.Hostname}
}

// dockerBase returns the identity labels for the Docker cadvisor lane.
func dockerBase(h *fixture.Host) map[string]string {
	return map[string]string{"job": "integrations/docker", "instance": h.Hostname}
}

// profileOf maps the fixture profile string to a nodeexp.Profile. Anything other than
// "full" (including the empty string) defaults to the cost-controlled integration set.
func profileOf(h *fixture.Host) nodeexp.Profile {
	if h.Profile == "full" {
		return nodeexp.ProfileFull
	}
	return nodeexp.ProfileIntegration
}

// toTopology derives a deterministic per-host nodeexp.HostTopology from the fixture.Host
// and the construct seed. ALL identity that is not blueprint-declared (disk/NIC/fs device
// names, boot time) is derived from `seed` so the topology is stable across ticks and
// across process restarts (invariant I12; no global rand). Defaults fill any zero field.
func toTopology(seed string, h *fixture.Host) nodeexp.HostTopology {
	numCPU := h.NumCPU
	if numCPU <= 0 {
		numCPU = defaultNumCPU
	}
	memTotal := h.MemTotal
	if memTotal <= 0 {
		memTotal = defaultMemTotal
	}

	disks, nics, fs := devicesFor(h.OS, memTotal)

	// BootTime is a now-independent stable uptime anchor: a fixed unix offset derived from
	// the seed (uptime between 1 and ~45 days). It MUST be constant across ticks — node
	// uptime/boot_time series read it directly, so deriving from `now` would make them jitter.
	uptimeSecs := hashSpan(seed+":boottime", 86400, 86400*45)
	const epochAnchor = 1_700_000_000 // fixed reference so BootTime is fully now-independent
	bootTime := float64(epochAnchor - uptimeSecs)

	return nodeexp.HostTopology{
		Hostname: h.Hostname,
		NumCPU:   numCPU,
		MemTotal: memTotal,
		Disks:    disks,
		NICs:     nics,
		FS:       fs,
		OS:       osInfoFor(h),
		BootTime: bootTime,
	}
}

// devicesFor returns the OS-appropriate default disk, NIC, and root-filesystem topology.
func devicesFor(os string, memTotal float64) (disks []string, nics []nodeexp.NIC, fs nodeexp.FSMount) {
	// Root filesystem size: a generous multiple of RAM, clamped to a sensible floor.
	fsSize := memTotal * 12
	if fsSize < 100<<30 {
		fsSize = 100 << 30
	}
	switch os {
	case "windows":
		return []string{"nvme0n1"},
			[]nodeexp.NIC{{Name: "Ethernet", SpeedBytes: 1e9}},
			nodeexp.FSMount{Device: "C:", FSType: "NTFS", Mountpoint: "C:", SizeBytes: fsSize}
	case "darwin", "macos":
		return []string{"disk0"},
			[]nodeexp.NIC{{Name: "en0", SpeedBytes: 1e9}},
			nodeexp.FSMount{Device: "/dev/disk0", FSType: "apfs", Mountpoint: "/", SizeBytes: fsSize}
	default: // linux
		return []string{"nvme0n1"},
			[]nodeexp.NIC{{Name: "eth0", SpeedBytes: 1e9}},
			nodeexp.FSMount{Device: "/dev/nvme0n1p1", FSType: "ext4", Mountpoint: "/", SizeBytes: fsSize}
	}
}

// osInfoFor builds the nodeexp.OSInfo identity from the host OS + declared OSVersion/Kernel.
// Windows fields (Product/Build/MajorVersion/…) are left for the windows emitter to default
// when empty; we populate Version from OSVersion when given.
func osInfoFor(h *fixture.Host) nodeexp.OSInfo {
	switch h.OS {
	case "windows":
		return nodeexp.OSInfo{
			Name:    "Microsoft Windows",
			Version: h.OSVersion, // e.g. "10.0.20348"; windows emitter defaults when ""
			Machine: "x86_64",
		}
	case "darwin", "macos":
		version := h.OSVersion
		if version == "" {
			version = "14.5"
		}
		kernel := h.Kernel
		if kernel == "" {
			kernel = "23.5.0" // Darwin kernel version (sensible per-OS default; BLUEPRINT-SCHEMA.md)
		}
		return nodeexp.OSInfo{
			ID:         "darwin",
			Name:       "macOS",
			PrettyName: "macOS " + version,
			Version:    version,
			VersionID:  version,
			Kernel:     kernel,
			Machine:    "arm64",
		}
	default: // linux
		version := h.OSVersion
		if version == "" {
			version = "22.04"
		}
		kernel := h.Kernel
		if kernel == "" {
			kernel = "6.8.0-generic" // sensible per-OS default (BLUEPRINT-SCHEMA.md)
		}
		return nodeexp.OSInfo{
			ID:         "ubuntu",
			Name:       "Ubuntu",
			PrettyName: "Ubuntu " + version,
			Version:    version + " LTS",
			VersionID:  version,
			Kernel:     kernel,
			Machine:    "x86_64",
		}
	}
}

// dockerContainers returns a deterministic 3–6 container set for the Docker lane, derived
// entirely from the seed (names + resource shape).
//
// Per a live homelab reference capture (host-capture.md "Docker label schema"), the
// cadvisor METRIC series carry the NATIVE cadvisor labels: `name` (the Docker container
// name), `image` (image:tag), and `id` (the cgroup path, e.g.
// "/system.slice/docker-<hash>.scope"). The `container` label is NOT a cadvisor metric
// label — it is a LOGS-only relabel applied by Alloy's docker log discovery, so it lives
// in logs.go (buildDockerStreams), never on the metric series here.
//
// Labels (cpu/mem/fs) and NetLabels (network) are identical for docker (single-scope), both
// carrying {name, image, id}.
func dockerContainers(seed string) []nodeexp.Container {
	type spec struct {
		name       string
		image      string  // image:tag, as cadvisor reports it
		cpuRequest float64 // cores
		memLimit   float64 // bytes
	}
	catalog := []spec{
		{"nginx", "docker.io/library/nginx:1.27", 0.25, 128 << 20},
		{"postgres", "docker.io/library/postgres:16", 1.0, 1 << 30},
		{"redis", "docker.io/library/redis:7", 0.5, 256 << 20},
		{"app", "ghcr.io/synthkit/app:latest", 1.5, 512 << 20},
		{"worker", "ghcr.io/synthkit/worker:latest", 0.75, 384 << 20},
		{"caddy", "docker.io/library/caddy:2", 0.25, 96 << 20},
	}
	// Count in [3,6], derived from the seed.
	n := int(hashSpan(seed+":containers", 3, 6))
	if n > len(catalog) {
		n = len(catalog)
	}
	out := make([]nodeexp.Container, 0, n)
	for i := 0; i < n; i++ {
		c := catalog[i]
		// id mirrors the captured cadvisor form id="/system.slice/docker-<64hex>.scope".
		id := "/system.slice/docker-" + dockerHash(seed, c.name) + ".scope"
		lbls := map[string]string{
			"name":  c.name,
			"image": c.image,
			"id":    id,
		}
		// Docker is single-scope: network series carry the same {name,image,id}.
		netLbls := map[string]string{
			"name":  c.name,
			"image": c.image,
			"id":    id,
		}
		out = append(out, nodeexp.Container{
			CPURequest: c.cpuRequest,
			MemLimit:   c.memLimit,
			Labels:     lbls,
			NetLabels:  netLbls,
		})
	}
	return out
}

// dockerHash returns a stable 64-hex container id hash for (seed, name), mirroring the
// length/shape of a real Docker container id (the suffix of the cgroup-scope `id` label).
//
// 4 blocks, each from a DISTINCT FNV-1a hash keyed by (seed, name, blockIndex). Each
// 64-bit hash is expanded into 16 hex chars by taking 4 bits per char across all 16
// nibbles, so every char varies. Concatenating the 4 blocks yields 64 non-repeating
// hex chars. Fully seed-derived (I12, no global rand).
//
// (Regression fixed: the prior impl hashed seed+byte(i) and took % 16, varying only the
// low 4 bits with period i mod 16 — producing a 16-hex block repeated 4x.)
func dockerHash(seed, name string) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 64)
	for block := 0; block < 4; block++ {
		h := fnv.New64a()
		_, _ = h.Write([]byte(seed + ":dockerid:" + name + ":"))
		_, _ = h.Write([]byte{byte(block)})
		v := h.Sum64()
		for k := 0; k < 16; k++ {
			out = append(out, hexdigits[(v>>(uint(k)*4))&0xf])
		}
	}
	return string(out)
}
