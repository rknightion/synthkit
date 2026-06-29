// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"testing"
	"time"
)

func TestDropWhereRetiresMatchingSeries(t *testing.T) {
	s := NewState()
	s.Set("kube_pod_info", map[string]string{"pod": "api-0"}, 1)
	s.Set("kube_pod_info", map[string]string{"pod": "api-1"}, 1)
	s.Add("restarts_total", map[string]string{"pod": "api-1"}, 3)
	s.DropWhere(func(name string, lbls map[string]string) bool { return lbls["pod"] == "api-1" })
	got := map[string]bool{}
	for _, ser := range s.Collect(time.Unix(0, 0)) {
		got[ser.Name+"/"+ser.Labels["pod"]] = true
	}
	if !got["kube_pod_info/api-0"] {
		t.Errorf("api-0 must remain")
	}
	if got["kube_pod_info/api-1"] || got["restarts_total/api-1"] {
		t.Errorf("api-1 series must be dropped: %v", got)
	}
}
