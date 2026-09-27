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
	actionModeFetch             = "fetch"
	actionModeInfinity          = "infinity"
	writeBaseControlSuffixError = "-write-base-url must not end in /control; pass the base URL, synthkit-control-dash appends /control/... itself"
	layoutCustomer              = "customer"
	layoutControlPlane          = "control-plane"
)

type opts struct {
	writeBaseURL string
	dsName       string
	dsUID        string
	actionMode   string
	layout       string
	folder       string
	promUID      string
	lokiUID      string
	outDir       string
	blueprints   string
}

func (o opts) validate() error {
	switch o.layout {
	case "", layoutCustomer:
	case layoutControlPlane:
		if o.actionMode != actionModeInfinity {
			return errors.New("-layout control-plane requires -action-mode infinity")
		}
		if o.promUID == "" || o.lokiUID == "" {
			return errors.New("-layout control-plane requires -prom-uid and -loki-uid")
		}
	default:
		return errors.New("-layout must be customer or control-plane")
	}
	switch o.actionMode {
	case "", actionModeFetch:
		if o.writeBaseURL == "" {
			return nil
		}
		u, err := url.Parse(o.writeBaseURL)
		if err != nil || u == nil {
			return errors.New("-write-base-url must be a valid URL")
		}
		if hasControlPathSuffix(u.Path) {
			return errors.New(writeBaseControlSuffixError)
		}
		return nil
	case actionModeInfinity:
		if o.dsUID == "" || o.writeBaseURL == "" {
			return errors.New("-action-mode infinity requires -ds-uid and -write-base-url (the URL the datasource reaches)")
		}
		u, err := url.Parse(o.writeBaseURL)
		if err != nil || u == nil {
			return errors.New("-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode")
		}
		unsupportedScheme := !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")
		if unsupportedScheme || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(o.writeBaseURL, "#") {
			return errors.New("-write-base-url must be an absolute HTTP(S) URL without credentials, query or fragment in infinity mode")
		}
		if hasControlPathSuffix(u.Path) {
			return errors.New(writeBaseControlSuffixError)
		}
		return nil
	default:
		return errors.New("-action-mode must be fetch or infinity")
	}
}

func hasControlPathSuffix(path string) bool {
	return strings.HasSuffix(strings.TrimSuffix(path, "/"), "/control")
}

func main() {
	var o opts
	// Reads are RELATIVE paths resolved against the Infinity datasource's Base URL (no read base
	// here). In fetch mode, POSTs run in the browser; in infinity mode, the datasource sends them.
	flag.StringVar(&o.writeBaseURL, "write-base-url", "", "action-button POST base URL (HTTP(S) without credentials, query or fragment for infinity; browser-reachable in fetch mode; datasource-reachable in infinity mode; empty uses relative paths)")
	flag.StringVar(&o.dsName, "ds-name", "", "Infinity datasource name (required)")
	flag.StringVar(&o.actionMode, "action-mode", actionModeFetch, "fetch (browser POST) or infinity (server-side via the datasource; needs Grafana toggle vizActionsAuth)")
	flag.StringVar(&o.dsUID, "ds-uid", "", "Infinity datasource UID (required with -action-mode infinity)")
	flag.StringVar(&o.layout, "layout", layoutCustomer, "customer or control-plane instructor console")
	flag.StringVar(&o.folder, "folder", "", "target Grafana folder UID (empty = General)")
	flag.StringVar(&o.promUID, "prom-uid", "", "Prometheus datasource UID for control-plane impact charts")
	flag.StringVar(&o.lokiUID, "loki-uid", "", "Loki datasource UID for control-plane impact charts")
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
	build := buildControlDashboard
	if o.layout == layoutControlPlane {
		build = buildControlPlaneDashboard
	}
	d, err := build(o)
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
