// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"
)

//go:embed ec2_instancetypes.csv
var ec2InstanceCSV []byte

// InstanceSpec is the resolved hardware shape of an EC2/EKS instance type — the SINGLE
// source of node capacity (vCPU, memory) and network class for both the k8s substrate
// (kube_node_status_capacity, node-exporter) and the EC2 lane. Known is false when the
// type was absent from the captured catalogue and the fields were synthesised from the
// size suffix; Type is ALWAYS preserved verbatim.
type InstanceSpec struct {
	Type        string
	VCPU        int
	MemBytes    float64
	Arch        string  // "x86_64" | "arm64" (catalogue verbatim; "" when synthesised)
	NetworkGbps float64 // parsed figure from the network class; 0 when non-numeric/unknown
	ENIs        int     // max network interfaces (DescribeInstanceTypes NetworkInfo.MaximumNetworkInterfaces); 0 when unknown
	IPv4PerENI  int     // IPv4 addresses per interface (NetworkInfo.Ipv4AddressesPerInterface); 0 when unknown
	Known       bool
}

// MaxPods returns the EKS default (non-prefix-delegation) max pods for the instance type,
// per the AWS VPC-CNI formula maxPods = ENIs*(IPv4PerENI-1)+2 (validated against the live
// a live reference cluster: t4g.large→35, m6g.large→29). Unknown/synthesised types fall back to
// 110 (the prefix-delegation / common default).
func (s InstanceSpec) MaxPods() int {
	if s.ENIs > 0 && s.IPv4PerENI > 1 {
		return s.ENIs*(s.IPv4PerENI-1) + 2
	}
	return 110
}

var ec2Specs = parseEC2Specs(ec2InstanceCSV)

// parseEC2Specs parses the embedded catalogue with a quote-aware CSV reader: fields are
// RFC-4180 quoted where they contain commas — memory ≥ 1024 GiB is written "1,024" and some
// legacy families carry a multi-arch "i386, x86_64" — so a naive comma split would corrupt
// them. '#' provenance lines are skipped; rows with a bad vCPU/memory field are dropped
// (a zero-capacity "known" node is worse than a size-ladder fallback) rather than recorded.
func parseEC2Specs(data []byte) map[string]InstanceSpec {
	out := map[string]InstanceSpec{}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comment = '#'
	r.FieldsPerRecord = -1 // bare-metal rows have fewer columns; we only read the first 5
	r.LazyQuotes = true
	for {
		f, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(f) < 5 {
			continue
		}
		t := strings.TrimSpace(f[0])
		vcpu, errV := strconv.Atoi(strings.TrimSpace(f[1]))
		memGiB, errM := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(f[2]), ",", ""), 64)
		if t == "" || errV != nil || errM != nil {
			continue
		}
		enis, ipv4 := 0, 0
		if len(f) >= 7 {
			enis, _ = strconv.Atoi(strings.TrimSpace(f[5]))
			ipv4, _ = strconv.Atoi(strings.TrimSpace(f[6]))
		}
		out[t] = InstanceSpec{
			Type: t, VCPU: vcpu, MemBytes: memGiB * 1024 * 1024 * 1024,
			Arch: strings.TrimSpace(f[3]), NetworkGbps: parseNetworkGbps(f[4]),
			ENIs: enis, IPv4PerENI: ipv4, Known: true,
		}
	}
	return out
}

// KubeArch maps the catalogue architecture to the node's kubernetes.io/arch label value —
// GOARCH form ("amd64"/"arm64"/"386"), as real KSM emits on kube_node_labels
// (label_kubernetes_io_arch), NOT the uname form node_uname_info uses ("x86_64"/"aarch64").
// Multi-arch and unknown/synthesised types default to amd64 (the 64-bit x86 baseline).
func (s InstanceSpec) KubeArch() string { return kubeArch(s.Arch) }

// UnameMachine returns the node_uname_info `machine` value (uname form) for the instance
// type: arm64→"aarch64", x86_64/multi→"x86_64". Distinct from KubeArch (GOARCH form).
func (s InstanceSpec) UnameMachine() string {
	if strings.Contains(s.Arch, "arm64") {
		return "aarch64"
	}
	return "x86_64"
}

func kubeArch(arch string) string {
	switch {
	case strings.Contains(arch, "arm64"):
		return "arm64"
	case strings.Contains(arch, "x86_64"):
		return "amd64"
	case strings.Contains(arch, "i386"):
		return "386"
	default:
		return "amd64"
	}
}

func parseNetworkGbps(s string) float64 {
	for _, tok := range strings.Fields(s) {
		if v, err := strconv.ParseFloat(tok, 64); err == nil {
			return v
		}
	}
	return 0
}

var sizeLadder = map[string][2]float64{
	"nano": {1, 0.5}, "micro": {1, 1}, "small": {1, 2}, "medium": {1, 4},
	"large": {2, 8}, "xlarge": {4, 16}, "2xlarge": {8, 32}, "4xlarge": {16, 64},
	"8xlarge": {32, 128}, "12xlarge": {48, 192}, "16xlarge": {64, 256},
	"24xlarge": {96, 384}, "32xlarge": {128, 512}, "48xlarge": {192, 768},
}

// LookupInstanceSpec resolves an instance type to its hardware shape. Exact catalogue hit
// => Known=true. A miss preserves Type verbatim and synthesises VCPU/MemBytes from the
// size suffix (Known=false); an unrecognised suffix falls back to 4 vCPU/16 GiB.
func LookupInstanceSpec(instanceType string) InstanceSpec {
	if s, ok := ec2Specs[instanceType]; ok {
		return s
	}
	vcpu, memGiB := 4.0, 16.0
	if dot := strings.LastIndex(instanceType, "."); dot >= 0 {
		if sz, ok := sizeLadder[instanceType[dot+1:]]; ok {
			vcpu, memGiB = sz[0], sz[1]
		}
	}
	return InstanceSpec{Type: instanceType, VCPU: int(vcpu), MemBytes: memGiB * 1024 * 1024 * 1024, Known: false}
}
