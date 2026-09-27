// SPDX-License-Identifier: AGPL-3.0-only

// Command synthkit-control-dash generates the CUSTOMER self-serve control dashboard: an
// Infinity-datasource-backed Grafana v2 dashboard exposing only the customer-safe knobs
// (master volume + incident scenarios) as read panels and action buttons. Reads come from the
// synthkit control plane's GET routes (?audience=customer); writes POST to /control/load and
// /control/scenarios. The operator UI (/control/ui) is unaffected. In fetch mode (the default),
// writes use the browser's separate HTTP Basic challenge. In infinity mode Grafana sends writes
// server-side through the Infinity datasource's secure Basic auth. No token is embedded in JSON.
package main

import (
	"errors"
	"flag"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rknightion/synthkit/dashboard"
)

const (
	actionModeFetch    = "fetch"
	actionModeInfinity = "infinity"
)

type opts struct {
	writeBaseURL string
	dsName       string
	dsUID        string
	actionMode   string
	outDir       string
	blueprints   string
}

func (o opts) validate() error {
	switch o.actionMode {
	case "", actionModeFetch:
		return nil
	case actionModeInfinity:
		if o.dsUID == "" || o.writeBaseURL == "" {
			return errors.New("-action-mode infinity requires -ds-uid and -write-base-url (the URL the datasource reaches)")
		}
		u, err := url.Parse(o.writeBaseURL)
		if err != nil || u == nil {
			return errors.New("-write-base-url must be an absolute HTTPS URL without credentials, query or fragment in infinity mode")
		}
		unsupportedScheme := !strings.EqualFold(u.Scheme, "https")
		if unsupportedScheme || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(o.writeBaseURL, "#") {
			return errors.New("-write-base-url must be an absolute HTTPS URL without credentials, query or fragment in infinity mode")
		}
		return nil
	default:
		return errors.New("-action-mode must be fetch or infinity")
	}
}

func main() {
	var o opts
	// Reads are RELATIVE paths resolved against the Infinity datasource's Base URL (no read base
	// here). In fetch mode, POSTs run in the browser; in infinity mode, the datasource sends them.
	flag.StringVar(&o.writeBaseURL, "write-base-url", "", "action-button POST base URL (HTTPS without credentials, query or fragment for infinity; browser-reachable in fetch mode; datasource-reachable in infinity mode; empty uses relative paths)")
	flag.StringVar(&o.dsName, "ds-name", "", "Infinity datasource name (required)")
	flag.StringVar(&o.actionMode, "action-mode", actionModeFetch, "fetch (browser POST) or infinity (server-side via the datasource; needs Grafana toggle vizActionsAuth)")
	flag.StringVar(&o.dsUID, "ds-uid", "", "Infinity datasource UID (required with -action-mode infinity)")
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
	if err := o.validate(); err != nil {
		return err
	}
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
