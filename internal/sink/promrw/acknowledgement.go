// SPDX-License-Identifier: AGPL-3.0-only

package promrw

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/promrw/writev2"
)

func rw2Acknowledgement(resp *http.Response, request *writev2.Request) (pushhook.Acknowledgement, error) {
	want := [3]int64{}
	for _, series := range request.Timeseries {
		want[0] += int64(len(series.Samples))
		want[1] += int64(len(series.Histograms))
		want[2] += int64(len(series.Exemplars))
	}
	a := pushhook.Acknowledgement{Status: pushhook.AckUnknown, Unit: "rw2_elements", Expected: want[0] + want[1] + want[2]}
	present := 0
	written := int64(0)
	for i, name := range []string{"X-Prometheus-Remote-Write-Samples-Written", "X-Prometheus-Remote-Write-Histograms-Written", "X-Prometheus-Remote-Write-Exemplars-Written"} {
		values := resp.Header.Values(name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 {
			return a, operationalerr.New(operationalerr.CodeInternal)
		}
		n, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 64)
		if err != nil || n < 0 || n > want[i] {
			return a, operationalerr.New(operationalerr.CodeInternal)
		}
		present++
		written += n
	}
	if resp.StatusCode == http.StatusAccepted && (present < 3 || written < a.Expected) {
		a.Status, a.Accepted = pushhook.AckDeferred, written
		return a, nil // Deferred validation is not rejection and must not cause a blind retry.
	}
	if present == 0 || (resp.StatusCode >= 500 && present < 3) {
		return a, nil // Legacy transport success remains unverified, never a durable receipt.
	}
	a.Accepted, a.Rejected = written, a.Expected-written
	if a.Rejected > 0 {
		a.Status = pushhook.AckPartial
		if written == 0 {
			a.Status = pushhook.AckRejected
		}
		return a, operationalerr.New(operationalerr.CodeRejected)
	}
	if resp.StatusCode/100 == 2 {
		a.Status = pushhook.AckAccepted
	}
	return a, nil
}
