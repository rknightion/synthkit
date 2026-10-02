// SPDX-License-Identifier: AGPL-3.0-only

package fixture

type GPUFieldSource struct {
	URL      string `yaml:"url"`
	Revision string `yaml:"revision"`
	SHA256   string `yaml:"sha256"`
	Section  string `yaml:"section"`
}

type GPUAddressBlockSpec struct {
	CIDR        string `yaml:"cidr"`
	StartOffset int    `yaml:"start_offset"`
}

type GPUTopologySpec struct {
	Racks           []GPURackSpec           `yaml:"racks"`
	Pools           []GPUPoolSpec           `yaml:"pools"`
	Fabrics         []GPUFabricSpec         `yaml:"fabrics"`
	StorageClusters []GPUStorageClusterSpec `yaml:"storage_clusters"`
	Schedulers      []GPUSchedulerSpec      `yaml:"schedulers"`
}

type GPURackSpec struct {
	Name            string             `yaml:"name"`
	Site            string             `yaml:"site"`
	Shape           string             `yaml:"shape"`
	Cooling         string             `yaml:"cooling"`
	HeightU         int                `yaml:"height_u"`
	Physics         GPURackPhysicsSpec `yaml:"physics"`
	FacilityDevices []GPUDeviceSpec    `yaml:"facility_devices"`
	NVLink          *NVLinkSpec        `yaml:"nvlink"`
}

type GPURackPhysicsSpec struct {
	FixedOverheadW           float64 `yaml:"fixed_overhead_w"`
	LiquidHeatFraction       float64 `yaml:"liquid_heat_fraction"`
	SupplyTempC              float64 `yaml:"supply_temp_c"`
	FlowLPM                  float64 `yaml:"flow_lpm"`
	FluidDensityKgPerL       float64 `yaml:"fluid_density_kg_per_l"`
	FluidSpecificHeatJPerKgC float64 `yaml:"fluid_specific_heat_j_per_kg_c"`
}

type GPUPoolSpec struct {
	Name              string                 `yaml:"name"`
	Shape             string                 `yaml:"shape"`
	GPUModel          string                 `yaml:"gpu_model"`
	NodeCount         int                    `yaml:"node_count"`
	GPUsPerNode       int                    `yaml:"gpus_per_node"`
	HostnamePrefix    string                 `yaml:"hostname_prefix"`
	KubernetesCluster string                 `yaml:"kubernetes_cluster"`
	ScalableUnit      string                 `yaml:"scalable_unit"`
	NodeIPs           *GPUAddressBlockSpec   `yaml:"node_ips"`
	Hardware          GPUNodeHardwareSpec    `yaml:"hardware"`
	Operating         GPUOperatingSpec       `yaml:"operating"`
	BMC               GPUBMCTemplateSpec     `yaml:"bmc"`
	Placements        []GPUPoolPlacementSpec `yaml:"placements"`
	NICs              []GPUNICSpec           `yaml:"nics"`
	Storage           *GPUStorageBindingSpec `yaml:"storage"`
	Nodes             []GPUNodeOverrideSpec  `yaml:"nodes"`
	GPUs              []GPUOverrideSpec      `yaml:"gpus"`
}

type GPUPoolPlacementSpec struct {
	RackKey     string `yaml:"rack"`
	NodeStart   int    `yaml:"node_start"`
	NodeCount   int    `yaml:"node_count"`
	SlotStart   int    `yaml:"slot_start"`
	NodeHeightU int    `yaml:"node_height_u"`
}

type GPUNodeHardwareSpec struct {
	CPUs      int     `yaml:"cpus"`
	MemoryGiB float64 `yaml:"memory_gib"`
	Arch      string  `yaml:"arch"`
}

type GPUOperatingSpec struct {
	PowerLimitW           *float64 `yaml:"power_limit_w"`
	IdlePowerFraction     float64  `yaml:"idle_power_fraction"`
	AmbientTempC          float64  `yaml:"ambient_temp_c"`
	FullPowerRiseC        float64  `yaml:"full_power_rise_c"`
	CoolingFaultRiseC     float64  `yaml:"cooling_fault_rise_c"`
	CoolingPerfLoss       float64  `yaml:"cooling_perf_loss"`
	CongestionPerfLoss    float64  `yaml:"congestion_perf_loss"`
	StoragePerfLoss       float64  `yaml:"storage_perf_loss"`
	NodeBasePowerW        float64  `yaml:"node_base_power_w"`
	NodeDynamicPerCoreW   float64  `yaml:"node_dynamic_per_core_w"`
	NodeDynamicPerNICW    float64  `yaml:"node_dynamic_per_nic_w"`
	NVLinkErrorsPerSecond float64  `yaml:"nvlink_errors_per_second"`
}

type GPUBMCTemplateSpec struct {
	HostnameSuffix string               `yaml:"hostname_suffix"`
	ManagementIPs  *GPUAddressBlockSpec `yaml:"management_ips"`
	Ports          []GPUDevicePortSpec  `yaml:"ports"`
}

type GPUNodeOverrideSpec struct {
	Ordinal      int                    `yaml:"ordinal"`
	Hostname     string                 `yaml:"hostname"`
	IP           string                 `yaml:"ip"`
	Serial       string                 `yaml:"serial"`
	TrayID       string                 `yaml:"tray_id"`
	BMC          *GPUDeviceSpec         `yaml:"bmc"`
	NICAddresses []GPUNICAddressSpec    `yaml:"nic_addresses"`
	Attachments  []GPUNICAttachmentSpec `yaml:"attachments"`
}

type GPUNICAddressSpec struct {
	NIC string `yaml:"nic"`
	IP  string `yaml:"ip"`
}

type GPUNICAttachmentSpec struct {
	NIC    string `yaml:"nic"`
	Switch string `yaml:"switch"`
	Port   string `yaml:"port"`
}

type GPUOverrideSpec struct {
	NodeOrdinal  int                  `yaml:"node_ordinal"`
	Slot         int                  `yaml:"slot"`
	UUID         string               `yaml:"uuid"`
	BoardSerial  string               `yaml:"board_serial"`
	PCIBusID     string               `yaml:"pci_bus_id"`
	UsableMemory *GPUUsableMemorySpec `yaml:"usable_memory"`
}

type GPUUsableMemorySpec struct {
	Bytes  uint64         `yaml:"bytes"`
	Source GPUFieldSource `yaml:"source"`
}

type GPUNICSpec struct {
	Name      string               `yaml:"name"`
	Fabric    string               `yaml:"fabric"`
	Rail      *int                 `yaml:"rail"`
	Switch    string               `yaml:"switch"`
	SpeedGbps float64              `yaml:"speed_gbps"`
	IPs       *GPUAddressBlockSpec `yaml:"ips"`
}

type GPUStorageBindingSpec struct {
	Cluster string `yaml:"cluster"`
	NIC     string `yaml:"nic"`
	Role    string `yaml:"role"`
}

type GPUDeviceSpec struct {
	Name         string              `yaml:"name"`
	Kind         string              `yaml:"kind"`
	Vendor       string              `yaml:"vendor"`
	Model        string              `yaml:"model"`
	OSVersion    string              `yaml:"os_version"`
	Serial       string              `yaml:"serial"`
	ManagementIP string              `yaml:"management_ip"`
	Rail         *int                `yaml:"rail"`
	ScalableUnit string              `yaml:"scalable_unit"`
	StaticPowerW *float64            `yaml:"static_power_w"`
	Ports        []GPUDevicePortSpec `yaml:"ports"`
	NICs         []GPUDeviceNICSpec  `yaml:"nics"`
	Collection   *GPUCollectionSpec  `yaml:"collection"`
}

type GPUDevicePortSpec struct {
	Name      string  `yaml:"name"`
	SpeedGbps float64 `yaml:"speed_gbps"`
}

type GPUDeviceNICSpec struct {
	Name      string  `yaml:"name"`
	IP        string  `yaml:"ip"`
	Fabric    string  `yaml:"fabric"`
	Switch    string  `yaml:"switch"`
	Port      string  `yaml:"port"`
	SpeedGbps float64 `yaml:"speed_gbps"`
}

type GPUCollectionSpec struct {
	Protocol   string         `yaml:"protocol"`
	Module     string         `yaml:"module"`
	Port       int            `yaml:"port"`
	ModbusUnit *int           `yaml:"modbus_unit"`
	Source     GPUFieldSource `yaml:"source"`
}

type GPUFabricSpec struct {
	Name    string              `yaml:"name"`
	Site    string              `yaml:"site"`
	Role    string              `yaml:"role"`
	Devices []GPUDeviceSpec     `yaml:"devices"`
	Links   []GPUFabricLinkSpec `yaml:"links"`
}

type GPUFabricLinkSpec struct {
	ADevice string `yaml:"a_device"`
	APort   string `yaml:"a_port"`
	BDevice string `yaml:"b_device"`
	BPort   string `yaml:"b_port"`
}

type GPUStorageClusterSpec struct {
	Name    string          `yaml:"name"`
	Site    string          `yaml:"site"`
	Fabric  string          `yaml:"fabric"`
	Devices []GPUDeviceSpec `yaml:"devices"`
}

type NVLinkSpec struct {
	Domain          string                  `yaml:"domain"`
	Correlated      bool                    `yaml:"correlated"`
	LinksPerGPU     *int                    `yaml:"links_per_gpu"`
	LinkCountSource *GPUFieldSource         `yaml:"link_count_source"`
	Partitions      []NVLinkPartitionSpec   `yaml:"partitions"`
	SwitchTrays     []NVSwitchTraySpec      `yaml:"switch_trays"`
	PortMappings    []NVLinkPortMappingSpec `yaml:"port_mappings"`
	Links           []NVLinkLinkSpec        `yaml:"links"`
	EntityMappings  []GPUEntityMappingSpec  `yaml:"entity_mappings"`
}

type NVLinkPartitionSpec struct {
	Name string   `yaml:"name"`
	GPUs []string `yaml:"gpus"`
}

type NVSwitchTraySpec struct {
	Ordinal  int            `yaml:"ordinal"`
	ID       string         `yaml:"id"`
	BMC      *GPUDeviceSpec `yaml:"bmc"`
	Switches []NVSwitchSpec `yaml:"switches"`
}

type NVSwitchSpec struct {
	Ordinal int    `yaml:"ordinal"`
	Name    string `yaml:"name"`
	Serial  string `yaml:"serial"`
}

type NVLinkPortMappingSpec struct {
	Tray     int            `yaml:"tray"`
	Switch   int            `yaml:"switch"`
	Port     int            `yaml:"port"`
	VendorID string         `yaml:"vendor_id"`
	Source   GPUFieldSource `yaml:"source"`
}

type NVLinkLinkSpec struct {
	GPU          string         `yaml:"gpu"`
	GPULinkIndex int            `yaml:"gpu_link_index"`
	Tray         int            `yaml:"tray"`
	Switch       int            `yaml:"switch"`
	Port         int            `yaml:"port"`
	Source       GPUFieldSource `yaml:"source"`
}

type GPUEntityMappingSpec struct {
	Kind     string         `yaml:"kind"`
	Key      string         `yaml:"key"`
	VendorID string         `yaml:"vendor_id"`
	Source   GPUFieldSource `yaml:"source"`
}

type GPUSchedulerSpec struct {
	Name      string            `yaml:"name"`
	Kind      string            `yaml:"kind"`
	Pools     []string          `yaml:"pools"`
	Projects  []GPUProjectSpec  `yaml:"projects"`
	Workloads []GPUWorkloadSpec `yaml:"workloads"`
}

type GPUProjectSpec struct {
	Name       string `yaml:"name"`
	Department string `yaml:"department"`
	Partition  string `yaml:"partition"`
	GPUQuota   int    `yaml:"gpu_quota"`
}

type GPUWorkloadSpec struct {
	Name       string          `yaml:"name"`
	Project    string          `yaml:"project"`
	Role       string          `yaml:"role"`
	Priority   int             `yaml:"priority"`
	Cycle      string          `yaml:"cycle"`
	PendingFor string          `yaml:"pending_for"`
	RunningMin string          `yaml:"running_min"`
	RunningMax string          `yaml:"running_max"`
	Namespace  string          `yaml:"namespace"`
	Container  string          `yaml:"container"`
	Demand     GPUDemandSpec   `yaml:"demand"`
	Workers    []GPUWorkerSpec `yaml:"workers"`
}

type GPUDemandSpec struct {
	UtilizationBase                    float64 `yaml:"utilization_base"`
	UtilizationAmplitude               float64 `yaml:"utilization_amplitude"`
	Period                             string  `yaml:"period"`
	MemoryReservedFraction             float64 `yaml:"memory_reserved_fraction"`
	MemoryDynamicFraction              float64 `yaml:"memory_dynamic_fraction"`
	RequestsPerSecondAtFullUtilization float64 `yaml:"requests_per_second_at_full_utilization"`
}

type GPUWorkerSpec struct {
	Name        string `yaml:"name"`
	Pool        string `yaml:"pool"`
	NodeOrdinal int    `yaml:"node_ordinal"`
	GPUSlots    []int  `yaml:"gpu_slots"`
}
