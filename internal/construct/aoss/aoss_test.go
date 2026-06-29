// SPDX-License-Identifier: AGPL-3.0-only

package aoss

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func TestAOSS_EmitsCollectionAndOCUSeries(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
		Seed:  "test-seed",
	}
	c, err := Build(&Config{Collections: []string{"my-collection"}}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Collection-scoped metric present.
	if got := mc.Find("aws_aoss_search_request_rate_average"); len(got) == 0 {
		t.Fatal("missing aws_aoss_search_request_rate_average")
	}
	if got := mc.Find("aws_aoss_search_request_rate_sum"); len(got) == 0 {
		t.Fatal("missing aws_aoss_search_request_rate_sum")
	}
	// OCU metric present.
	if got := mc.Find("aws_aoss_search_ocu_average"); len(got) == 0 {
		t.Fatal("missing aws_aoss_search_ocu_average")
	}
	// Collection-scoped series carries correct namespace.
	for _, s := range mc.Find("aws_aoss_search_request_rate_average") {
		if s.Labels["namespace"] != "AWS/AOSS" {
			t.Errorf("namespace=%q want AWS/AOSS", s.Labels["namespace"])
		}
		if s.Labels["dimension_CollectionName"] != "my-collection" {
			t.Errorf("dimension_CollectionName=%q want my-collection", s.Labels["dimension_CollectionName"])
		}
	}
	// OCU series must NOT carry dimension_CollectionId (account-level only).
	for _, s := range mc.Find("aws_aoss_search_ocu_average") {
		if _, ok := s.Labels["dimension_CollectionId"]; ok {
			t.Error("OCU series must NOT carry dimension_CollectionId")
		}
	}
	// 2xx/4xx/5xx are valid series names.
	if got := mc.Find("aws_aoss_2xx_sum"); len(got) == 0 {
		t.Fatal("missing aws_aoss_2xx_sum")
	}
}
