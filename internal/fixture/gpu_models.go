// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

//go:embed gpu_models.csv
var gpuModelsCSV string

// Catalogue sources are immutable provenance, never runtime fetch instructions.
var gpuDatasheets = map[string]GPUFieldSource{
	"h100":  {URL: "https://dam-cdn.nvd.orangelogic.com/AssetLink/mfj81tsm68n0ne632upmuvirso3ta3g3.pdf", Revision: "2287922, May22", SHA256: "200a1084af6cc4a7d1195124a26f5b2915f1e2cf9fa307c02ad47079a750c785", Section: "Specifications"},
	"h200":  {URL: "https://dam-cdn.nvd.orangelogic.com/AssetLink/5o2qgy5d2835ve2pm11i62kv8mphqta8.pdf", Revision: "3512650, Nov24 (preliminary)", SHA256: "ccfeaf25d4dfcd1981d7a02f19355559fa9715ad32cf8f2b14dcf75eb6e0ac6f", Section: "Specifications"},
	"gb200": {URL: "https://dam-cdn.nvd.orangelogic.com/AssetLink/y441155802qub41q118b2852i557jem5.pdf", Revision: "4204213, Oct25", SHA256: "ad8ed65e64974278670d8e6904c0e3020391027925e339bf28e75fdfe2d307d7", Section: "p.8 specifications"},
}
var gpuMIGSource = GPUFieldSource{URL: "https://docs.nvidia.com/datacenter/tesla/mig-user-guide/pdf/MIG_User_Guide.pdf", Revision: "Release 615, 2026-09-11", SHA256: "50cf6c4eb31a0662403d1112a7697e83c04bfea0c8b77beda7a539675e4c5bf6", Section: "Supported GPUs"}
var gpuNVLinkSource = GPUFieldSource{URL: "https://docs.nvidia.com/cuda/archive/12.8.0/hopper-tuning-guide/index.html", Revision: "CUDA 12.8.0", SHA256: "0fb0a5dd532f43e04c41112aa07bb3fddfab03fc8e68392509f0bec2c03ca418", Section: "1.4.3 NVLink (Hopper/SXM)"}

func parseGPUModels() map[string]GPUModelSpec {
	r := csv.NewReader(strings.NewReader(gpuModelsCSV))
	r.Comment = '#'
	r.FieldsPerRecord = 10
	if _, err := r.Read(); err != nil {
		panic(err)
	}
	out := map[string]GPUModelSpec{}
	for {
		f, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		if _, dup := out[f[0]]; dup {
			panic("duplicate GPU model")
		}
		number := func(v string) float64 {
			x, e := strconv.ParseFloat(v, 64)
			if e != nil || !gpuFinite(x) || x <= 0 {
				panic(fmt.Sprintf("invalid GPU catalogue number %q", v))
			}
			return x
		}
		m := GPUModelSpec{Key: f[0], Product: f[1], AdvertisedMemoryGB: number(f[2]), Sources: map[string]GPUFieldSource{}}
		p := number(f[3])
		m.MaxPowerW = &p
		if f[4] != "" {
			n, e := strconv.Atoi(f[4])
			if e != nil || n <= 0 {
				panic("invalid NVLink count")
			}
			m.NVLinkCount = &n
			m.Sources["NVLinkCount"] = gpuNVLinkSource
		}
		m.MIGSupported, err = strconv.ParseBool(f[5])
		if err != nil {
			panic(err)
		}
		m.MIGMaxInstances, err = strconv.Atoi(f[6])
		if err != nil || m.MIGMaxInstances < 1 {
			panic("invalid MIG count")
		}
		if f[7] != "" || f[8] != "" || f[9] != "" {
			if m.Key != "h100_sxm_80gb" || f[7] == "" || f[8] == "" || f[9] == "" {
				panic("unsourced/partial thermal catalogue column")
			}
			slow, shutdown, operating := number(f[7]), number(f[8]), number(f[9])
			if operating > slow || slow > shutdown {
				panic("invalid thermal catalogue order")
			}
			if slow != 89 || shutdown != 95 || operating != 87 {
				panic("unsupported thermal catalogue envelope")
			}
			m.SlowdownTempC, m.ShutdownTempC, m.MaxOperatingTempC = &slow, &shutdown, &operating
			for field, section := range map[string]string{"SlowdownTempC": "GPU slowdown 89 C", "ShutdownTempC": "GPU shutdown 95 C", "MaxOperatingTempC": "GPU core 32 C + T.Limit margin 55 C = 87 C; current nvidia-smi semantics, not branch-pinned"} {
				m.Sources[field] = GPUFieldSource{URL: "capture://loop48-prep/gpucap/p5.48xlarge-h100x8/cap/nvsmi-q.txt", Revision: "H100 SXM; driver 595.91.07; capture 2026-10-03 ONLY", SHA256: "403b1ef416c0de549677eef77ba1c9175d5fcf3eb88270ebb98b88092315bd4d", Section: "lines 187-191; " + section}
			}
		}
		var source GPUFieldSource
		for prefix, s := range gpuDatasheets {
			if strings.HasPrefix(m.Key, prefix+"_") {
				source = s
			}
		}
		if source.URL == "" {
			panic("missing product provenance")
		}
		for _, column := range []string{"Key", "Product", "AdvertisedMemoryGB", "MaxPowerW"} {
			m.Sources[column] = source
		}
		m.Sources["MIGSupported"] = gpuMIGSource
		m.Sources["MIGMaxInstances"] = gpuMIGSource
		out[m.Key] = m
	}
	return out
}

var gpuModels = parseGPUModels()

func GPUModelKeys() []string {
	out := make([]string, 0, len(gpuModels))
	for k := range gpuModels {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func LookupGPUModel(key string) (GPUModelSpec, bool) {
	m, ok := gpuModels[key]
	if !ok {
		return GPUModelSpec{}, false
	}
	// Copy mutable catalogue metadata; callers cannot change future lookups.
	src := make(map[string]GPUFieldSource, len(m.Sources))
	for k, v := range m.Sources {
		src[k] = v
	}
	m.Sources = src
	if m.MaxPowerW != nil {
		v := *m.MaxPowerW
		m.MaxPowerW = &v
	}
	if m.NVLinkCount != nil {
		v := *m.NVLinkCount
		m.NVLinkCount = &v
	}
	for _, ptr := range []**float64{&m.SlowdownTempC, &m.ShutdownTempC, &m.MaxOperatingTempC} {
		if *ptr != nil {
			v := **ptr
			*ptr = &v
		}
	}
	return m, true
}
