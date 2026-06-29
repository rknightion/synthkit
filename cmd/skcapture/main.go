// SPDX-License-Identifier: AGPL-3.0-only

// Command skcapture inspects a Kubernetes environment via kubectl and writes a versioned,
// optionally age-encrypted Inventory file for later processing by skforge.
//
// It imports ONLY synthkit/internal/capture and the Go standard library — no blueprint,
// core, runner, bpsource, construct, or workload packages. This trust boundary is enforced
// by internal/capture.TestCaptureTrustBoundary.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/capture"
)

// buildVersion is the tool version stamped at build time (e.g. via -ldflags).
// Defaults to "dev" when built without explicit version injection.
const buildVersion = "dev"

func main() {
	fs := flag.NewFlagSet("skcapture", flag.ContinueOnError)

	outFlag := fs.String("out", "capture.age", "output file path")
	passphraseFileFlag := fs.String("passphrase-file", "", "path to a file containing the encryption passphrase (required unless --plain)")
	plainFlag := fs.Bool("plain", false, "write unencrypted JSON (mutually exclusive with --passphrase-file)")
	namespacesFlag := fs.String("namespaces", "", "comma-separated namespace allow-list (empty = all)")
	excludeNsFlag := fs.String("exclude-namespaces", "kube-system,kube-node-lease,kube-public", "comma-separated namespace deny-list")
	collectorsFlag := fs.String("collectors", "k8s", "comma-separated list of enabled collectors")
	includeSecretDataFlag := fs.Bool("include-secret-data", false, "read Secret data values (default: metadata only)")
	includeConfigMapDataFlag := fs.Bool("include-configmap-data", false, "read ConfigMap data values (default: metadata only)")
	versionFlag := fs.Bool("version", false, "print tool version and schema version, then exit")

	if err := fs.Parse(os.Args[1:]); err != nil {
		// flag.ContinueOnError means -help prints usage and returns ErrHelp; other errors are real.
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "skcapture: flag error: %v\n", err)
		os.Exit(2)
	}

	if *versionFlag {
		fmt.Printf("skcapture version %s (schema version %d)\n", buildVersion, capture.SchemaVersion)
		os.Exit(0)
	}

	// Validate encryption flags before doing any real work.
	if !*plainFlag && *passphraseFileFlag == "" {
		fmt.Fprintln(os.Stderr, "skcapture: error: must specify either --plain or --passphrase-file")
		os.Exit(2)
	}
	if *plainFlag && *passphraseFileFlag != "" {
		fmt.Fprintln(os.Stderr, "skcapture: error: --plain and --passphrase-file are mutually exclusive")
		os.Exit(2)
	}

	// Build CaptureOpts from flags.
	opts := capture.CaptureOpts{
		IncludeSecretData:    *includeSecretDataFlag,
		IncludeConfigMapData: *includeConfigMapDataFlag,
	}
	if *namespacesFlag != "" {
		opts.Namespaces = splitTrimmed(*namespacesFlag)
	}
	if *excludeNsFlag != "" {
		opts.ExcludeNamespaces = splitTrimmed(*excludeNsFlag)
	}
	if *collectorsFlag != "" {
		opts.Collectors = splitTrimmed(*collectorsFlag)
	}

	// Build enabled collectors.
	var collectors []capture.Collector
	for _, name := range opts.Collectors {
		switch name {
		case "k8s":
			collectors = append(collectors, &capture.K8sCollector{})
		default:
			fmt.Fprintf(os.Stderr, "skcapture: unknown collector %q (registered: k8s)\n", name)
			os.Exit(2)
		}
	}

	// Initialise inventory (Counts map pre-allocated).
	inv := capture.NewInventory()

	ctx := context.Background()

	// Run each collector. Collectors write ONLY ResourceKinds/Counts — the envelope ownership rule.
	for _, col := range collectors {
		if err := col.Collect(ctx, inv, opts); err != nil {
			fmt.Fprintf(os.Stderr, "skcapture: collector %q failed: %v\n", col.Name(), err)
			os.Exit(1)
		}
	}

	// Set scalar envelope fields AFTER collectors run (M3 — avoid clobbering ResourceKinds/Counts).
	inv.Envelope.SchemaVersion = capture.SchemaVersion
	inv.Envelope.CapturedAtMS = time.Now().UnixMilli()
	inv.Envelope.ToolVersion = buildVersion
	inv.Envelope.Flags = nonDefaultFlags(fs)

	// Serialise.
	data, err := capture.Marshal(inv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skcapture: marshal: %v\n", err)
		os.Exit(1)
	}

	// Encrypt unless --plain.
	var outData []byte
	encLabel := "encrypted"
	if *plainFlag {
		outData = data
		encLabel = "plain"
	} else {
		passphrase, rerr := readPassphraseFile(*passphraseFileFlag)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "skcapture: passphrase-file: %v\n", rerr)
			os.Exit(1)
		}
		outData, err = capture.Encrypt(data, passphrase)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skcapture: encrypt: %v\n", err)
			os.Exit(1)
		}
	}

	// Write output file.
	if err := os.WriteFile(*outFlag, outData, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "skcapture: write %s: %v\n", *outFlag, err)
		os.Exit(1)
	}

	// One-line summary to stderr.
	fmt.Fprintf(os.Stderr, "skcapture: wrote %s (%s) — nodes=%d namespaces=%d workloads=%d addons=%d services=%d ingresses=%d\n",
		*outFlag, encLabel,
		inv.Envelope.Counts["nodes"],
		inv.Envelope.Counts["namespaces"],
		countWorkloads(inv),
		inv.Envelope.Counts["addons"],
		inv.Envelope.Counts["services"],
		inv.Envelope.Counts["ingresses"],
	)
}

// splitTrimmed splits a comma-separated string and trims whitespace from each element,
// omitting empty elements.
func splitTrimmed(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// readPassphraseFile reads the named file and returns its trimmed contents as the passphrase.
// No interactive TTY prompt is attempted — skcapture runs in a k8s Job without a TTY.
func readPassphraseFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}
	p := strings.TrimSpace(string(b))
	if p == "" {
		return "", fmt.Errorf("passphrase file %q is empty", path)
	}
	return p, nil
}

// nonDefaultFlags returns a slice of "<flag>=<value>" strings for flags whose values
// differ from their defaults. This gives the SE a clear audit record of how skcapture
// was invoked, stored in the inventory Envelope.
func nonDefaultFlags(fs *flag.FlagSet) []string {
	var out []string
	fs.Visit(func(f *flag.Flag) {
		out = append(out, f.Name+"="+f.Value.String())
	})
	return out
}

// countWorkloads sums deployment, statefulset, and daemonset counts from the envelope.
func countWorkloads(inv *capture.Inventory) int {
	return inv.Envelope.Counts["deployments"] +
		inv.Envelope.Counts["statefulsets"] +
		inv.Envelope.Counts["daemonsets"]
}
