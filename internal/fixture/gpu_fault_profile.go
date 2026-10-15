// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import "fmt"

// RequireGPUFaultProfile admits explicitly declared fault scenarios separately
// from the ordinary operating-envelope capability. It never changes that older
// capability's meaning.
func RequireGPUFaultProfile(s *GPUSelection, driver string) error {
	if s == nil || s.Topology == nil || len(s.Nodes()) == 0 {
		return fmt.Errorf("GPU fault profile requires a nonempty selection")
	}
	for _, node := range s.Nodes() {
		if len(node.GPUs) != 8 {
			return fmt.Errorf("GPU fault profile requires an eight-GPU HGX node")
		}
		for _, gpu := range node.GPUs {
			profile := LookupGPUConstraintProfile(gpu.Model, driver)
			if profile == nil || gpu.HGXModuleID == nil {
				return fmt.Errorf("GPU fault profile unsupported for %s driver %s", gpu.Model, driver)
			}
			normal := node.Operating.AmbientTempC + node.Operating.FullPowerRiseC
			fault := normal + node.Operating.CoolingFaultRiseC
			if !gpuFinite(normal, fault) || node.Operating.AmbientTempC < -273.15 || normal > profile.MaxOperatingTempC || fault >= profile.ShutdownTempC {
				return fmt.Errorf("GPU fault profile exceeds normal operating or shutdown boundary")
			}
		}
	}
	return nil
}
