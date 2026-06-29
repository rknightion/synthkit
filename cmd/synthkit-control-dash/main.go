// SPDX-License-Identifier: AGPL-3.0-only

// Command synthkit-control-dash generates the CUSTOMER self-serve control dashboard: an
// Infinity-datasource-backed Grafana v2 dashboard exposing only the customer-safe knobs
// (master volume + incident scenarios) as read panels + native fetch-POST action buttons.
// Reads come from the synthkit control plane's GET routes (?audience=customer); writes POST
// to /control/load and /control/scenarios. The operator UI (/control/ui) is unaffected.
// GET routes are open (no auth); POST routes use HTTP Basic auth (WWW-Authenticate challenge)
// so the browser handles the credential prompt natively — no token is embedded in the dashboard.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/rknightion/synthkit/dashboard"
)

type opts struct {
	writeBaseURL string
	dsName       string
	outDir       string
	blueprints   string
}

func main() {
	var o opts
	// Reads are RELATIVE paths resolved against the Infinity datasource's Base URL (no read base
	// here). Writes are browser fetches → need an absolute, browser-reachable base URL. The default
	// is the browser-trusted tailscale-serve endpoint; OVERRIDE per-deploy.
	flag.StringVar(&o.writeBaseURL, "write-base-url", "", "action-button POST base URL (absolute, HTTPS, browser-reachable; per-deploy)")
	flag.StringVar(&o.dsName, "ds-name", "", "Infinity datasource name (required)")
	flag.StringVar(&o.outDir, "out", "", "output directory (required)")
	flag.StringVar(&o.blueprints, "blueprints", "./blueprints", "directory of *.yaml blueprints to enumerate scenarios from")
	flag.Parse()
	if o.dsName == "" || o.outDir == "" {
		log.Fatal("synthkit-control-dash: -ds-name and -out are required")
	}
	if err := generate(o); err != nil {
		log.Fatalf("synthkit-control-dash: %v", err)
	}
}

func generate(o opts) error {
	d, err := buildControlDashboard(o)
	if err != nil {
		return err
	}
	js, err := dashboard.Render(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.outDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(o.outDir, d.UID+".json")
	return os.WriteFile(path, js, 0o644)
}
