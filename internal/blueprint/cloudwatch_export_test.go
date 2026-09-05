// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"strings"
	"testing"
)

func TestCloudWatchExportValidation(t *testing.T) {
	for _, mode := range []string{"", "remote_write", "otlp", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			data := fmt.Sprintf("name: transport-test\nenvironments:\n  - name: prod\n    cloud:\n      provider: aws\n      account_id: '111122223333'\n      region: eu-west-1\n      vpc_id: vpc-test\n      cloudwatch_export: %q\n", mode)
			res, err := Load([]byte(data), testRegistry(t))
			if mode == "invalid" {
				if err == nil || !strings.Contains(err.Error(), "cloudwatch_export") {
					t.Fatalf("invalid mode accepted or wrong error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := mode
			if want == "" {
				want = "remote_write"
			}
			if len(res.Constructs) == 0 {
				t.Fatal("no cloud construct resolved")
			}
			for _, ci := range res.Constructs {
				if got := ci.Fixtures.Cloud.CloudWatchExportMode(); got != want {
					t.Errorf("%s transport = %q, want %q", ci.Kind, got, want)
				}
			}
		})
	}
}
