// SPDX-License-Identifier: AGPL-3.0-only

// Command reality-corpus-gcx performs a read-only gcx series read-back and
// cumulative-merges the selected EKS and CloudWatch shapes into the reality corpus.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/inventory"
)

type commandRunner func(name string, args ...string) ([]byte, error)

var liveSeriesSelectors = []string{
	`{__name__="kube_node_info"}`,
	`{__name__="kube_node_labels"}`,
	`{__name__="kube_pod_info"}`,
	`{__name__="kube_pod_labels"}`,
	`{__name__=~"awscni_.*"}`,
	`{__name__=~"kubeproxy_.*"}`,
	`{__name__="kubernetes_build_info",job="integrations/kubernetes/kube-proxy"}`,
	`{__name__=~"aws_amazonmwaa_.*"}`,
	`{__name__=~"aws_aoss_.*"}`,
	`{__name__=~"aws_applicationelb_.*"}`,
	`{__name__=~"aws_docdb_.*"}`,
	`{__name__=~"aws_ebs_.*"}`,
	`{__name__=~"aws_ec2_.*"}`,
	`{__name__=~"aws_eks_.*"}`,
	`{__name__=~"aws_elasticache_.*"}`,
	`{__name__=~"aws_firehose_.*"}`,
	`{__name__=~"aws_glue_.*"}`,
	`{__name__=~"aws_mwaa_.*"}`,
	`{__name__=~"aws_natgateway_.*"}`,
	`{__name__=~"aws_neptune_.*"}`,
	`{__name__=~"aws_networkelb_.*"}`,
	`{__name__=~"aws_privatelinkendpoints_.*"}`,
	`{__name__=~"aws_privatelinkservices_.*"}`,
	`{__name__=~"aws_rds_.*"}`,
	`{__name__=~"aws_s3_.*"}`,
}

func main() {
	if err := run(os.Args[1:], os.Stdout, execute); err != nil {
		fmt.Fprintln(os.Stderr, "reality-corpus-gcx:", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer, runner commandRunner) error {
	flags := flag.NewFlagSet("reality-corpus-gcx", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	contextName := flags.String("context", "", "operator-selected gcx context (required)")
	corpusPath := flags.String("corpus", "reality-corpus", "path to the committed reality corpus")
	since := flags.String("since", "24h", "bounded lookback passed to gcx metrics series")
	capturedOn := flags.String("captured-on", time.Now().UTC().Format("2006-01-02"), "capture date in YYYY-MM-DD")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*contextName) == "" {
		return errors.New("-context is required; select a live stack explicitly through GCX_CONTEXT")
	}
	if strings.TrimSpace(*corpusPath) == "" {
		return errors.New("-corpus must not be empty")
	}
	if strings.TrimSpace(*since) == "" {
		return errors.New("-since must not be empty")
	}

	if contextOutput, err := runner("gcx", "config", "check", "--context", *contextName); err != nil {
		return commandFailure("gcx context check failed; verify the explicit context and its credential", contextOutput, err)
	}
	versionOutput, err := runner("gcx", "version")
	if err != nil {
		return commandFailure("read gcx version", versionOutput, err)
	}
	collectorVersion, err := parseGCXVersion(versionOutput)
	if err != nil {
		return err
	}

	series := make([]map[string]string, 0)
	for _, selector := range liveSeriesSelectors {
		seriesOutput, queryErr := runner(
			"gcx", "metrics", "series", "--context", *contextName,
			"--since", *since, "-o", "json", "--match", selector,
		)
		if queryErr != nil {
			return commandFailure(
				fmt.Sprintf("gcx metrics read-back failed for selector %s; verify the context has a configured Prometheus datasource and valid credential", selector),
				seriesOutput,
				queryErr,
			)
		}
		observed, parseErr := parseGCXSeries(seriesOutput)
		if parseErr != nil {
			return parseErr
		}
		series = append(series, observed...)
	}
	// The metadata API is the only mechanism a read-back has for an instrument type. It is
	// served per ingest path, not per series, so a stack can answer for one family of metrics
	// and not another; whatever it does not answer for keeps the unknown sentinel.
	metadataOutput, err := runner("gcx", "metrics", "metadata", "--context", *contextName, "-o", "json")
	if err != nil {
		return commandFailure("gcx metric metadata read-back failed; verify the context has a configured Prometheus datasource and valid credential", metadataOutput, err)
	}
	metadata, err := parseGCXMetadata(metadataOutput)
	if err != nil {
		return err
	}
	declaredInstruments := inventory.DeclaredInstrumentTypes(metadata)

	documents, err := inventory.BuildGCXLiveReadback(series, declaredInstruments, *capturedOn, collectorVersion)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		return errors.New("gcx read-back returned no in-scope EKS or core CloudWatch series")
	}
	for _, document := range documents {
		path := filepath.Join(*corpusPath, document.Area, "eks-live-readback.json")
		if err := inventory.MergeCorpusDocumentFile(path, document); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "merged %d %s metric contracts into %s\n", len(document.Inventory.Metrics), document.Area, path); err != nil {
			return err
		}
	}
	return nil
}

// execute returns the command's stdout alone on success. gcx writes advisory hints to stderr
// when it detects an agent harness, and a combined stream would feed those into the JSON
// decoders below.
func execute(name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	command := exec.Command(name, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		// On failure the diagnostics matter more than the parse, so return both streams.
		return append(stdout.Bytes(), stderr.Bytes()...), err
	}
	return stdout.Bytes(), nil
}

func commandFailure(prefix string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return fmt.Errorf("%s: %w: %s", prefix, err, message)
}

// parseGCXVersion reads the read-back tool's own version from either shape gcx reports it in:
// the field/value table, and the JSON object it emits when it detects an agent harness.
func parseGCXVersion(data []byte) (string, error) {
	var reported struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &reported); err == nil && strings.TrimSpace(reported.Version) != "" {
		return reported.Version, nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Version" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("parse gcx version: no version reported in %q", strings.TrimSpace(string(data)))
}

func parseGCXSeries(data []byte) ([]map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
	}
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode gcx series response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("decode gcx series response: multiple JSON documents")
		}
		return nil, fmt.Errorf("decode gcx series response: trailing JSON data: %w", err)
	}
	if response.Status != "success" {
		return nil, fmt.Errorf("gcx series response status %q, want success", response.Status)
	}
	return response.Data, nil
}

// parseGCXMetadata decodes a Prometheus-compatible metric metadata response: an object keyed
// by metric name whose values list the metadata records the stack holds for that name. Only
// the declared type is retained; help and unit text is deployment prose.
func parseGCXMetadata(data []byte) (map[string][]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var response struct {
		Status string `json:"status"`
		Data   map[string][]struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode gcx metadata response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("decode gcx metadata response: multiple JSON documents")
		}
		return nil, fmt.Errorf("decode gcx metadata response: trailing JSON data: %w", err)
	}
	if response.Status != "success" {
		return nil, fmt.Errorf("gcx metadata response status %q, want success", response.Status)
	}
	metadata := make(map[string][]string, len(response.Data))
	for name, records := range response.Data {
		for _, record := range records {
			metadata[name] = append(metadata[name], record.Type)
		}
	}
	return metadata, nil
}
