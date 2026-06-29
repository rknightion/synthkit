// SPDX-License-Identifier: AGPL-3.0-only

// identity.go — deterministic Azure estate identity + the DUAL ingestion-path label model.
//
// Azure Monitor metrics reach Mimir via two distinct ingestion paths that label the SAME
// underlying metrics DIFFERENTLY (signals/cspazure.md [slug: cspazure], SK-16). A blueprint picks one via the
// `ingestion_path` config knob (serverless preferred):
//
//	serverless ("cloud/azure" managed scraper):
//	  job=cloud/azure/microsoft-<provider>-<type>, credential=<scraper cred name>,
//	  instance=<full ARM resourceID, PascalCase preserved>, resourceID PascalCase, region;
//	  NO interval/timespan. Dimensions ride as dimension_<Name> (underscore). HttpStatusGroup
//	  lowercase (2xx/4xx). SQL elastic-pool carries NO resourceName (not a selectable group-by).
//
//	azure_exporter (prometheus.exporter.azure):
//	  job=integrations/azure, resourceID fully lowercased, instance=<opaque hash>,
//	  interval=PT1M, timespan=PT1M; NO credential/region. A SINGLE requested dimension is the
//	  bare label dimension="<value>"; MULTIPLE are dimension<Name> (NO underscore). HttpStatusGroup
//	  uppercase (2XX/4XX). SQL elastic-pool resourceName=<server>.
package cspazure

import (
	"fmt"
	"math"
	"strings"

	"github.com/rknightion/synthkit/internal/fixture"
)

// Ingestion-path identifiers (Config.IngestionPath values).
const (
	pathServerless = "serverless"
	pathExporter   = "azure_exporter"

	jobExporter = "integrations/azure"
)

// azureSub is one synthetic Azure subscription in the estate.
type azureSub struct {
	subscriptionID   string
	subscriptionName string
	// resourceGroups is the fixed ordered list per the predecessor estate model:
	// [0]=rg-databases [1]=rg-compute [2]=rg-networking [3]=rg-storage [4]=rg-messaging
	resourceGroups []string
	region         string
	// pgNames are the Postgres Flexible Server resource names for this subscription.
	// Derived from fx.DBs when available (even-index PG fixtures, modulo sub count),
	// otherwise self-contained seed-derived names.
	pgNames []string
}

// azureRegions is the default pool of Azure regions (extract §4.4 DefaultProfile).
var azureRegions = []string{"westeurope", "northeurope", "eastus"}

// resourceGroups is the fixed ordered list common to all subscriptions.
var resourceGroups = []string{
	"rg-databases",
	"rg-compute",
	"rg-networking",
	"rg-storage",
	"rg-messaging",
}

// buildSubs builds the deterministic subscription slice from config + fixtures.
func buildSubs(cfg Config, fx *fixture.Set) []azureSub {
	// Capitalise company for subscription display name.
	display := cfg.Company
	if len(display) > 0 {
		display = strings.ToUpper(display[:1]) + display[1:]
	}

	// Collect postgres fixtures (Azure PG, per extract §4.3).
	var pgFixtures []*fixture.DB
	for _, db := range fx.DBs {
		if db.Engine == "postgres" {
			pgFixtures = append(pgFixtures, db)
		}
	}

	subs := make([]azureSub, cfg.Subscriptions)
	for i := 0; i < cfg.Subscriptions; i++ {
		sub := azureSub{
			// UUID-form zero-padded subscription ID (extract §4.1).
			subscriptionID:   fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1),
			subscriptionName: fmt.Sprintf("%s-%02d", display, i+1),
			resourceGroups:   resourceGroups,
			region:           azureRegions[i%len(azureRegions)],
		}
		// Assign PG fixture names for this subscription if available, or generate them.
		if len(pgFixtures) > 0 {
			pg := pgFixtures[i%len(pgFixtures)]
			sub.pgNames = []string{pg.Name}
		} else {
			suffix := fixture.HexID(fx.Seed, 4, "pg", fmt.Sprintf("%d", i))
			sub.pgNames = []string{fmt.Sprintf("pg-%s-%s-01", cfg.Company, suffix)}
		}
		subs[i] = sub
	}
	return subs
}

// ── ARM resource-ID + provider-namespace casing ─────────────────────────────────

// armProviderCasing maps a lowercase ARM path token to its canonical Azure casing.
// Sourced from Azure's documented resource-provider namespaces; the serverless path
// preserves this casing in resourceID/instance. Tokens absent from the map (resource
// instance names embedded in nested provider paths, e.g. a SQL server name) pass through
// unchanged — exactly what we want, since those names keep their literal casing.
//
// NOTE (flag): only Microsoft.ServiceBus and Microsoft.Cdn were live-confirmed (2026-06-14);
// the remaining namespaces follow documented ARM convention. The leading `resourceGroups`
// segment is also assumed canonical (capital G) on the serverless path.
var armProviderCasing = map[string]string{
	"microsoft.compute":           "Microsoft.Compute",
	"virtualmachines":             "virtualMachines",
	"microsoft.sql":               "Microsoft.Sql",
	"servers":                     "servers",
	"databases":                   "databases",
	"elasticpools":                "elasticpools", // live-confirmed lowercase on the managed scraper (2026-06-14)
	"microsoft.dbforpostgresql":   "Microsoft.DBforPostgreSQL",
	"flexibleservers":             "flexibleServers",
	"microsoft.storage":           "Microsoft.Storage",
	"storageaccounts":             "storageAccounts",
	"blobservices":                "blobServices",
	"queueservices":               "queueServices",
	"microsoft.network":           "Microsoft.Network",
	"loadbalancers":               "loadBalancers",
	"applicationgateways":         "applicationGateways",
	"virtualnetworks":             "virtualNetworks",
	"microsoft.cdn":               "Microsoft.Cdn",
	"profiles":                    "profiles",
	"microsoft.eventhub":          "Microsoft.EventHub",
	"microsoft.servicebus":        "Microsoft.ServiceBus",
	"namespaces":                  "namespaces",
	"microsoft.cognitiveservices": "Microsoft.CognitiveServices",
	"accounts":                    "accounts",
}

// pascalProviderPath casing-maps each known provider/type token in a lowercase provider
// path; unknown tokens (embedded instance names) pass through unchanged.
func pascalProviderPath(lower string) string {
	parts := strings.Split(lower, "/")
	for i, p := range parts {
		if c, ok := armProviderCasing[p]; ok {
			parts[i] = c
		}
	}
	return strings.Join(parts, "/")
}

// jobSlug builds the serverless job suffix from the recognised provider/type tokens only
// (dropping embedded instance names): e.g. "microsoft.sql/servers/<srv>/databases" →
// "microsoft-sql-servers-databases"; "microsoft.cdn/profiles" → "microsoft-cdn-profiles".
func jobSlug(providerPathLower string) string {
	var toks []string
	for _, p := range strings.Split(providerPathLower, "/") {
		if _, ok := armProviderCasing[p]; ok {
			toks = append(toks, p)
		}
	}
	return strings.ReplaceAll(strings.Join(toks, "-"), ".", "-")
}

// armID builds the ARM resource ID for the active path. providerPathLower is the lowercase
// provider/type path (possibly nested, incl. parent instance names); idLastSegment is the
// final path segment (the resource's own name, or "default" for storage sub-services).
func (c *construct) armID(subID, rg, providerPathLower, idLastSegment string) string {
	if c.serverless() {
		return "/subscriptions/" + subID + "/resourceGroups/" + rg +
			"/providers/" + pascalProviderPath(providerPathLower) + "/" + idLastSegment
	}
	return "/subscriptions/" + subID + "/resourcegroups/" + rg +
		"/providers/" + providerPathLower + "/" + idLastSegment
}

// exporterInstance returns the opaque deterministic `instance` hash for the azure_exporter
// path (the real exporter emits an opaque per-resource hash, not the resourceID).
func exporterInstance(seed, resourceID string) string {
	return fixture.HexID(seed, 32, "azure_exporter_instance", resourceID)
}

// serverlessResourceName derives the serverless `resourceName` label from a resourceID:
// it is the resource NAME segments (the odd-position segments after the provider namespace)
// joined by "/". Flat resources → the bare name (e.g. "vm-app-01"); nested resources →
// "<parent>/<child>" (e.g. SQL DB "<server>/<db>", elastic pool "<server>/<pool>").
// Live-confirmed against the managed scraper (2026-06-14): the GC cloud/azure integration
// always emits this joined form, NOT the bare last segment.
func serverlessResourceName(rid string) string {
	const marker = "/providers/"
	i := strings.Index(rid, marker)
	if i < 0 {
		return ""
	}
	segs := strings.Split(rid[i+len(marker):], "/")
	// segs = [namespace, type1, name1, type2, name2, …]; names sit at indices 2,4,6,…
	var names []string
	for j := 2; j < len(segs); j += 2 {
		names = append(names, segs[j])
	}
	return strings.Join(names, "/")
}

// ── path-aware base labels ───────────────────────────────────────────────────────

// serverless reports whether this construct emits on the serverless ("cloud/azure") path.
func (c *construct) serverless() bool { return c.cfg.IngestionPath == pathServerless }

// addTags merges configured resource tags onto m as `tag_<key>` labels (both paths). No-op
// when no tags are configured — matching a default managed scraper (live-confirmed: surfaces
// no tags unless explicitly configured).
func (c *construct) addTags(m map[string]string) {
	for k, v := range c.cfg.Tags {
		m["tag_"+k] = v
	}
}

// baseLabels builds the full per-path base label set. idLastSegment is the resourceID's
// final segment; resourceName is the resourceName LABEL (often == idLastSegment, but
// decoupled for storage sub-services and SQL elastic pools). An empty resourceName omits
// the label entirely (I13 — absent, never "").
// stampEnv adds the env label when the construct is env-scoped (fanned per-cell via for_each_env),
// so every Azure family disambiguates per environment. Without it, a fanned csp_azure would push
// byte-identical resourceID-keyed series from every cell (Mimir duplicate-sample rejection).
// Aggregate (single instance) omits env (I13 — absent dimension omitted).
func (c *construct) stampEnv(m map[string]string) {
	if c.fx != nil && c.fx.Env != nil {
		m["env"] = c.fx.Env.Name
	}
}

func (c *construct) baseLabels(sub azureSub, rg, providerPathLower, idLastSegment, resourceName string) map[string]string {
	rid := c.armID(sub.subscriptionID, rg, providerPathLower, idLastSegment)
	m := map[string]string{
		"resourceID":       rid,
		"subscriptionID":   sub.subscriptionID,
		"subscriptionName": sub.subscriptionName,
		"resourceGroup":    rg,
	}
	if c.serverless() {
		// resourceName is the derived <parent>/<child> name form (live-confirmed), NOT the
		// caller-passed value — the managed scraper always joins the name segments.
		m["resourceName"] = serverlessResourceName(rid)
		m["job"] = "cloud/azure/" + jobSlug(providerPathLower)
		m["credential"] = c.cfg.Credential
		m["instance"] = rid
		m["region"] = sub.region
	} else {
		if resourceName != "" {
			m["resourceName"] = resourceName
		}
		m["job"] = jobExporter
		m["instance"] = exporterInstance(c.fx.Seed, rid)
		m["interval"] = "PT1M"
		m["timespan"] = "PT1M"
	}
	c.stampEnv(m)
	c.addTags(m)
	return m
}

// storageBaseLabels builds labels for an Azure Storage sub-service (blob/queue). The
// resourceID is ACCOUNT-ONLY on both paths (`.../storageAccounts/<sa>`) — live-confirmed on
// the managed scraper (2026-06-14): the blob/queue namespace lives in the serverless JOB slug
// + metric name, never the resourceID. serviceSlug ∈ {blobservices, queueservices}.
func (c *construct) storageBaseLabels(sub azureSub, rg, saName, serviceSlug string) map[string]string {
	rid := c.armID(sub.subscriptionID, rg, "microsoft.storage/storageaccounts", saName)
	m := map[string]string{
		"resourceID":       rid,
		"subscriptionID":   sub.subscriptionID,
		"subscriptionName": sub.subscriptionName,
		"resourceGroup":    rg,
		"resourceName":     saName,
	}
	if c.serverless() {
		m["job"] = "cloud/azure/microsoft-storage-storageaccounts-" + serviceSlug
		m["credential"] = c.cfg.Credential
		m["instance"] = rid
		m["region"] = sub.region
	} else {
		m["job"] = jobExporter
		m["instance"] = exporterInstance(c.fx.Seed, rid)
		m["interval"] = "PT1M"
		m["timespan"] = "PT1M"
	}
	c.stampEnv(m)
	c.addTags(m)
	return m
}

// baseLabelsFor is the common case where the resourceID last segment equals the
// resourceName label.
func (c *construct) baseLabelsFor(sub azureSub, rg, providerPathLower, resourceName string) map[string]string {
	return c.baseLabels(sub, rg, providerPathLower, resourceName, resourceName)
}

// ── path-aware dimension rendering ─────────────────────────────────────────────

// dim renders Azure metric dimensions into label keys for the active path:
//   - serverless: dimension_<Name> (underscore), Azure CamelCase preserved.
//   - azure_exporter, single dimension: the bare label dimension="<value>".
//   - azure_exporter, multiple dimensions: dimension<Name> (NO underscore).
//
// Callers pass logical Azure dimension names WITHOUT any "dimension" prefix.
func (c *construct) dim(dims map[string]string) map[string]string {
	out := make(map[string]string, len(dims))
	if c.serverless() {
		for k, v := range dims {
			out["dimension_"+k] = v
		}
		return out
	}
	if len(dims) == 1 {
		for _, v := range dims {
			out["dimension"] = v
		}
		return out
	}
	for k, v := range dims {
		out["dimension"+k] = v
	}
	return out
}

// statusGroup returns the HttpStatusGroup dimension VALUE for the active path: serverless
// emits lowercase (2xx/4xx); azure_exporter emits uppercase (2XX/4XX).
func (c *construct) statusGroup(g string) string {
	if c.serverless() {
		return g
	}
	return strings.ToUpper(g)
}

// ── small shared helpers ─────────────────────────────────────────────────────────

// mergeLabels merges extra key-value pairs onto a base label map (shallow copy).
func mergeLabels(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// clamp returns v clamped to [0, max].
func clamp(v, max float64) float64 {
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// rnd returns math.Round(v).
func rnd(v float64) float64 { return math.Round(v) }
