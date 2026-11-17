// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
)

func resourcesSchema() Schema {
	s := fullSchema()
	s.Resources = []ResourceInfo{{
		Blueprint: "bp", Name: "enrich-fn-concurrency", Kind: "concurrency", Env: "prod",
		Consumers: []ResourceConsumer{{Kind: "lambda", Name: "prod", Identity: map[string]string{"dimension_FunctionName": "enrich-fn"}}},
	}, {
		Blueprint: "bp", Name: "api-pool-prod", Kind: "pool", Env: "prod", Alias: "api-pool", HeldAcross: "partner-api",
		Consumers: []ResourceConsumer{{Kind: "app", Name: "intake", Service: "partner-gateway", Callers: []string{"api"},
			Identity: map[string]string{"service": "partner-gateway", "deployment_environment_name": "prod"}}},
	}, {
		Blueprint: "bp", Name: "idle", Kind: "dependency", Env: "prod", Consumers: []ResourceConsumer{},
	}}
	return s
}

// GET /control/schema serves each resource with its bound consumers and their identity as plain
// JSON; the customer audience stays the curated subset.
func TestSchemaRouteListsResourceConsumers(t *testing.T) {
	store := NewStore(t.TempDir() + "/control-state.json")
	h := NewHandler(store, nil, "", schemaSourceFunc(resourcesSchema))
	get := func(q string) []byte {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/schema"+q, nil))
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		return rec.Body.Bytes()
	}

	var got Schema
	if err := json.Unmarshal(get(""), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Resources, resourcesSchema().Resources) {
		t.Fatalf("resources round trip = %+v", got.Resources)
	}
	var raw struct {
		Resources []struct {
			Consumers []struct {
				Identity map[string]string `json:"identity"`
			} `json:"consumers"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(get(""), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Resources) != 3 || raw.Resources[0].Consumers[0].Identity["dimension_FunctionName"] != "enrich-fn" {
		t.Fatalf("resources JSON = %+v", raw.Resources)
	}
	if raw.Resources[2].Consumers == nil {
		t.Fatal("an unbound resource must list an empty consumers array, not null")
	}

	var cust map[string]json.RawMessage
	if err := json.Unmarshal(get("?audience=customer"), &cust); err != nil {
		t.Fatal(err)
	}
	if _, leaked := cust["resources"]; leaked {
		t.Fatal("customer schema leaked resources")
	}
}

// A schema with no resources serializes without the key, so existing clients see no change.
func TestSchemaWithoutResourcesOmitsTheKey(t *testing.T) {
	b, err := json.Marshal(fullSchema())
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["resources"]; ok {
		t.Fatalf("resource-less schema carries a resources key: %s", b)
	}
}
