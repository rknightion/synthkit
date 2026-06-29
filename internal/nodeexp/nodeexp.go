// SPDX-License-Identifier: AGPL-3.0-only

package nodeexp

// Profile is the kept-metric allowlist for node/windows/macos exporters.
type Profile string

const (
	ProfileIntegration Profile = "integration" // GC integration allowlist (Appendix A)
	ProfileFull        Profile = "full"        // broad default-Alloy surface (live reference capture)
	ProfileK8s         Profile = "k8s"         // k8s-monitoring chart set (current k8scluster behaviour)
)

// CadvisorProfile is the kept-metric allowlist for the cadvisor/container lane.
type CadvisorProfile string

const (
	CadvisorDocker CadvisorProfile = "docker" // GC docker integration allowlist (Appendix A)
	CadvisorK8s    CadvisorProfile = "k8s"    // k8s-monitoring cadvisor set (current k8scluster behaviour)
)

// OSInfo carries identity-info label values for *_os_info / *_uname_info.
type OSInfo struct {
	ID, Name, PrettyName, Version, VersionID, Kernel, Machine string
	// VariantID/BuildID are Linux node_os_info keys present only on some distros
	// (e.g. Bottlerocket carries them; Amazon Linux omits them). EmitLinux adds each
	// to node_os_info ONLY when non-empty (absent dimension OMITTED, I13).
	VariantID, BuildID string
	// Windows-only:
	Product, Build, MajorVersion, MinorVersion, Revision string
}

// NIC is one network interface.
type NIC struct {
	Name       string
	SpeedBytes float64 // 0 ⇒ no node_network_speed_bytes series (e.g. loopback)
}

// FSMount is the root filesystem mount.
type FSMount struct {
	Device, FSType, Mountpoint string
	SizeBytes                  float64
}

// HostTopology is the per-host shape the emitters iterate over.
type HostTopology struct {
	Hostname string
	NumCPU   int
	MemTotal float64
	Disks    []string
	NICs     []NIC
	FS       FSMount
	OS       OSInfo
	BootTime float64 // unix seconds; node_boot_time_seconds / windows_system_boot_time_*
}

// Container is one cadvisor container/pod observation. The caller owns BOTH label
// scopes (the lib owns names + physics + counter/gauge classification only):
//   - Labels:    container-scoped series (cpu/mem/fs). k8s: {id,image,name,pod,namespace,node,container};
//     docker: {container}.
//   - NetLabels: network series (k8s are POD-scoped: {id,image,name,pod,namespace,node,interface} —
//     NO container label, WITH interface). docker: {container} (= Labels).
//
// cpu/mem/fs vocab labels (cpu="total", device) are added by the lib onto base+Labels.
type Container struct {
	CPURequest float64 // cores
	MemLimit   float64 // bytes
	Labels     map[string]string
	NetLabels  map[string]string
}

// EmitLinux renders one Linux host's node_exporter series into st under the
// caller-supplied base identity labels (job, instance, + any context labels),
// filtered by prof. Ports the physics of internal/construct/k8scluster/nodeexporter.go.
// Implemented in linux.go.

// EmitMacOS renders one macOS host's node_exporter series (macOS memory subset:
// node_memory_total_bytes/compressed/internal/purgeable/wired/swap_*). Appendix A macOS list.
// Implemented in macos.go.

// EmitWindows renders one Windows host's windows_exporter series.
// Signature: func EmitWindows(st *state.State, base map[string]string, top HostTopology, prof Profile, factor, tickSec, scale float64, sh *shape.Engine)
// Implemented in windows.go.

// EmitMachine renders the per-host machine_memory_bytes plus, for CadvisorDocker, the
// machine_scrape_error and up{job=integrations/docker} series. The caller includes any node
// label in base (k8s machine_memory_bytes carries node=<hostname>; docker carries none).
// Signature: func EmitMachine(st *state.State, base map[string]string, memTotal float64, prof CadvisorProfile)
// Implemented in cadvisor.go.

// EmitContainer renders one container's cadvisor series filtered by prof.
// Ports internal/construct/k8scluster/cadvisor.go physics. base = identity (k8s or docker).
// Signature: func EmitContainer(st *state.State, base map[string]string, c Container, prof CadvisorProfile, factor, tickSec, scale float64, sh *shape.Engine)
// Implemented in cadvisor.go.
