// SPDX-License-Identifier: AGPL-3.0-only

package lambda_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/lambda"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
)

// An error spike that starts mid-run, after the healthy error carry has accumulated a
// fraction, must never report more Errors than executions: at thousands per minute the
// carry's boundary tolerance otherwise rounds one extra error up. The five statistics stay
// a feasible 0/1 sample set in both observation modes. 5124/min with error_fraction 0.1 and a
// full spike after 45 healthy minutes is a measured overshoot case; the neighbouring rates and
// a half-intensity spike cover the remaining-success path.
func TestErrorSpikeNeverExceedsExecutions(t *testing.T) {
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	at := func(m int) string { return start.Add(time.Duration(m) * time.Minute).Format("2006-01-02T15:04") }
	for _, zero := range []bool{false, true} {
		for _, spike := range []struct {
			inten float64
			from  int
		}{{1, 46}, {0.5, 50}} {
			for base := 5024; base < 5224; base += 200 {
				var fns []lambda.Function
				for r := base; r < base+200; r++ {
					fns = append(fns, lambda.Function{Name: fmt.Sprintf("fn-%d", r), InvocationsPerMin: float64(r), DurationMS: 25, ErrorFraction: 0.1, ZeroValuedSamples: zero})
				}
				eng := shape.New("UTC", []string{fmt.Sprintf("function_error_spike@%s/30m#%v", at(spike.from), spike.inten)})
				c := build(t, &lambda.Config{Functions: fns})
				for i := 1; i <= spike.from+10; i++ {
					w := &capture{}
					if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Metrics: w, Shape: eng}); err != nil {
						t.Fatal(err)
					}
					got := map[string]map[string]float64{}
					for _, s := range w.batch {
						fn := s.Labels["dimension_FunctionName"]
						if got[fn] == nil {
							got[fn] = map[string]float64{}
						}
						got[fn][s.Name] = s.Value
					}
					for fn, m := range got {
						if fn == "" {
							continue
						}
						inv, errs := m["aws_lambda_invocations_sum"], m["aws_lambda_errors_sum"]
						name := fmt.Sprintf("zero=%v intensity=%v %s minute %d", zero, spike.inten, fn, i)
						if errs > inv {
							t.Fatalf("%s: errors %v > invocations %v", name, errs, inv)
						}
						if spike.inten == 1 && i >= spike.from && errs != inv {
							t.Fatalf("%s: full spike errors %v, want every execution %v", name, errs, inv)
						}
						avg, mx, mn, n := m["aws_lambda_errors_average"], m["aws_lambda_errors_maximum"], m["aws_lambda_errors_minimum"], m["aws_lambda_errors_sample_count"]
						if errs > 0 && (avg < 0 || avg > 1 || mx > 1 || mn < 0 || mn > avg || avg > mx || errs > n) {
							t.Fatalf("%s: infeasible Errors statistics sum=%v avg=%v max=%v min=%v n=%v", name, errs, avg, mx, mn, n)
						}
					}
				}
			}
		}
	}
}
