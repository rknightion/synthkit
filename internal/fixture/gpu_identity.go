// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"fmt"
	"strconv"
	"strings"
)

func GPUUUID(seed string, parts ...string) string {
	return "GPU-" + NodeUID(seed, append([]string{"gpu_uuid"}, parts...)...)
}
func GPUSerial(seed, kind string, parts ...string) string {
	return "SYN" + strings.ToUpper(HexID(seed, 16, append([]string{kind}, parts...)...))
}
func GPUPCIBusID(domain, bus, device, function int) (string, error) {
	if domain < 0 || domain > 65535 || bus < 0 || bus > 255 || device < 0 || device > 31 || function < 0 || function > 7 {
		return "", fmt.Errorf("invalid PCI address")
	}
	return fmt.Sprintf("%08x:%02x:%02x.%d", domain, bus, device, function), nil
}
func GPUExecutionID(seed, workloadKey string, cycleOrdinal int64) string {
	return "exec-" + HexID(seed, 24, "gpu_execution", workloadKey, strconv.FormatInt(cycleOrdinal, 10))
}
func LookupNodeSpec(node Node) InstanceSpec {
	if node.Capacity != nil {
		return *node.Capacity
	}
	return LookupInstanceSpec(node.InstanceType)
}
