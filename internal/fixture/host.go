// SPDX-License-Identifier: AGPL-3.0-only

package fixture

// Host is one declared traditional (non-k8s) machine. Identity is Hostname
// (the `instance` label); substrate-scoped, collision-checked at load.
type Host struct {
	Hostname  string  // identity → `instance` label
	OS        string  // "linux" | "windows" | "darwin"
	PrivateIP string  // optional; "" when absent
	NumCPU    int     // logical CPUs
	MemTotal  float64 // bytes
	Profile   string  // "integration" | "full"
	Docker    bool    // emit docker cadvisor + container-log lane
	Logs      bool    // emit host log streams (journal/winevent/file)
	OSVersion string  // e.g. "22.04" / "Server 2022" / "14.5"; feeds *_os_info
	Kernel    string  // e.g. "6.8.0-40-generic"; feeds node_uname_info (linux/macos)
}
