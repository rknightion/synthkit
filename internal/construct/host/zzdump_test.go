// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/nodeexp"
	"github.com/rknightion/synthkit/internal/shape"
)

func TestZZDumpHostInventory(t *testing.T) {
	if os.Getenv("ZZDUMP") == "" {
		t.Skip("set ZZDUMP=1 to run")
	}
	type hostspec struct {
		name, os, prof string
		cpus           int
		mem            float64
		docker         bool
		osv, kernel    string
	}
	specs := []hostspec{
		{"camden", "linux", "integration", 8, 128 << 30, true, "44", "7.0.10-201.fc44.x86_64"},
		{"oli", "linux", "full", 8, 64 << 30, false, "13", "7.0.6-2-pve"},
		{"alex", "macos", "full", 8, 32 << 30, false, "27.0", "Darwin Kernel 27.0.0"},
		{"winsrv", "windows", "integration", 2, 6 << 30, false, "10.0.26100", ""},
	}

	metricLabels := map[string]map[string]bool{}
	now := time.Unix(1750000000, 0)
	sh := shape.New("Europe/London", nil)

	for _, s := range specs {
		h := &fixture.Host{
			Hostname: s.name, OS: s.os, NumCPU: s.cpus, MemTotal: s.mem,
			Profile: s.prof, OSVersion: s.osv, Kernel: s.kernel,
			Docker: s.docker, Logs: true,
		}
		c, err := Build(&Config{}, &fixture.Set{Host: h, Seed: "bp:host:" + s.name})
		if err != nil {
			t.Fatal(err)
		}
		cc := c.(*Construct)
		factor := 1.0
		const tickSec = 60.0
		const scale = tickSec / 30.0
		top := toTopology(cc.seed, cc.h)
		base := baseLabels(cc.h)
		prof := profileOf(cc.h)
		switch cc.h.OS {
		case "windows":
			nodeexp.EmitWindows(cc.st, base, top, prof, factor, tickSec, scale, sh)
		case "macos", "darwin":
			nodeexp.EmitMacOS(cc.st, base, top, prof, factor, tickSec, scale, sh)
		default:
			nodeexp.EmitLinux(cc.st, base, top, prof, factor, tickSec, scale, sh)
		}
		if cc.h.Docker {
			db := dockerBase(cc.h)
			for _, ct := range dockerContainers(cc.seed) {
				nodeexp.EmitContainer(cc.st, db, ct, nodeexp.CadvisorDocker, factor, tickSec, scale, sh)
			}
			nodeexp.EmitMachine(cc.st, db, top.MemTotal, nodeexp.CadvisorDocker)
		}
		series := cc.st.Collect(now)
		for _, ser := range series {
			keys := make([]string, 0, len(ser.Labels))
			for k := range ser.Labels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			ks := fmt.Sprintf("%v  job=%s", keys, ser.Labels["job"])
			if metricLabels[ser.Name] == nil {
				metricLabels[ser.Name] = map[string]bool{}
			}
			metricLabels[ser.Name][ks] = true
			for k, v := range ser.Labels {
				if v == "" {
					t.Errorf("EMPTY LABEL VALUE: %s label %s", ser.Name, k)
				}
				if k == "blueprint" || k == "cluster" {
					t.Errorf("FORBIDDEN LABEL %s on %s", k, ser.Name)
				}
			}
		}
		streams := buildLogs(cc.h, cc.seed, now, sh)
		for _, st := range streams {
			keys := make([]string, 0, len(st.Labels))
			for k := range st.Labels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Printf("LOG  %s  job=%s\n", keys, st.Labels["job"])
		}
	}

	names := make([]string, 0, len(metricLabels))
	for n := range metricLabels {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ks := make([]string, 0)
		for k := range metricLabels[n] {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Printf("MET  %s  %s\n", n, k)
		}
	}
}
