// SPDX-License-Identifier: AGPL-3.0-only

// Command lab-matrix is the k3d capture lab's non-orchestration half: it normalizes one
// permutation's raw receiver inventory into a corpus candidate, and it renders the single
// combined report a matrix run ends in. Cluster lifecycle, Docker and Helm stay in run.sh;
// everything with a decision in it lives here where it is unit-tested.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rknightion/synthkit/e2e/lab/matrix"
	"github.com/rknightion/synthkit/internal/inventory"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "normalize":
		err = runNormalize(os.Args[2:])
	case "report":
		os.Exit(runReport(os.Args[2:]))
	case "elide-corpus":
		err = runElideCorpus(os.Args[2:])
	case "promote":
		err = runPromote(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "lab-matrix: %v\n", err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  lab-matrix normalize -in RAW.json -out CANDIDATE.json -substrate S -collector-version V -captured-at T
  lab-matrix report -results DIR -out REPORT.md -json REPORT.json -run-id ID -generated-at T -max-parallel N [-parallelism-note TEXT]
  lab-matrix elide-corpus -file CORPUS.json [-file ...]
  lab-matrix promote -in CANDIDATE.json -out DOC.json -area A -permutation P -kind K
                     -substrate S -collector C -collector-version V -captured-on YYYY-MM-DD
                     -instrument-type-source TEXT [-metric-prefix P ...]
                     [-logs] [-fold-pod-logs] [-merge]
`)
}

func runNormalize(args []string) error {
	flags := flag.NewFlagSet("normalize", flag.ExitOnError)
	in := flags.String("in", "", "raw receiver inventory JSON")
	out := flags.String("out", "", "normalized candidate JSON to write")
	substrate := flags.String("substrate", "", "capture substrate, for example k3s")
	collectorVersion := flags.String("collector-version", "", "pinned collector or chart version")
	capturedAt := flags.String("captured-at", "", "RFC3339 capture timestamp")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" || *substrate == "" || *capturedAt == "" {
		return errors.New("normalize requires -in, -out, -substrate and -captured-at")
	}
	raw, err := readSchema(*in)
	if err != nil {
		return err
	}
	candidate := matrix.NormalizeCandidate(raw, inventory.Provenance{
		Substrate:    *substrate,
		ChartVersion: *collectorVersion,
		CapturedAt:   *capturedAt,
	})
	return writeSchema(*out, candidate)
}

// runPromote turns one permutation capture candidate into ONE corpus document. It is a
// separate, deliberate step from capture on purpose: no matrix job writes to reality-corpus,
// so promotion is always something an operator ran after reading the report.
func runPromote(args []string) error {
	flags := flag.NewFlagSet("promote", flag.ExitOnError)
	in := flags.String("in", "", "capture candidate JSON")
	out := flags.String("out", "", "corpus document to write")
	area := flags.String("area", "", "signals catalogue area this document is evidence for")
	permutation := flags.String("permutation", "", "collector configuration this document is evidence of")
	kind := flags.String("kind", "", "generic producer kind, for example k3d_lab")
	substrate := flags.String("substrate", "", "capture substrate, for example k3s")
	collector := flags.String("collector", "", "the collector under audit")
	collectorVersion := flags.String("collector-version", "", "the audited collector's pinned version")
	capturedOn := flags.String("captured-on", "", "capture date, YYYY-MM-DD")
	instrumentTypeSource := flags.String("instrument-type-source", "", "the mechanism instrument types were read from")
	var metricPrefixes fileList
	flags.Var(&metricPrefixes, "metric-prefix", "select captured metric families by name prefix (repeatable)")
	foldPodLogs := flags.Bool("fold-pod-logs", false, "fold captured log streams into the canonical pod-log source")
	logs := flags.Bool("logs", false, "promote the captured log entries as classified, one per family")
	merge := flags.Bool("merge", false, "canonically merge into an existing document at -out instead of replacing it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" || *area == "" {
		return errors.New("promote requires -in, -out and -area")
	}
	candidate, err := readSchema(*in)
	if err != nil {
		return err
	}
	promoted, err := matrix.PromoteCandidate(candidate, matrix.PromoteOptions{
		MetricPrefixes: metricPrefixes,
		FoldPodLogs:    *foldPodLogs,
		Logs:           *logs,
	})
	if err != nil {
		return err
	}
	document := inventory.CorpusDocument{
		CorpusVersion: inventory.CorpusVersion,
		Area:          *area,
		Source: inventory.CorpusSource{
			Kind:                 *kind,
			Permutation:          *permutation,
			Substrate:            *substrate,
			Collector:            *collector,
			CollectorRole:        inventory.CollectorRoleAudited,
			CollectorVersion:     *collectorVersion,
			CapturedOn:           *capturedOn,
			InstrumentTypeSource: *instrumentTypeSource,
		},
		Authority: inventory.CorpusAuthority{Substrates: []string{*substrate}},
		CaptureVolume: inventory.CaptureVolume{
			Runs: 1,
			// Counts the evidence THIS document holds. A logs-only document carries no metric
			// family, and recording 0 for it would read as a capture that observed nothing.
			ObservedContractCounts: []int{len(promoted.Metrics) + len(promoted.Logs)},
		},
		Inventory: promoted,
	}
	// Validated before writing: a corpus document that cannot be loaded is worse than none,
	// because the whole corpus fails to load and every finding silently disappears.
	if err := inventory.ValidateCorpusDocument(document); err != nil {
		return fmt.Errorf("%s: %w", *out, err)
	}
	if *merge {
		// The cumulative union, which never removes established evidence. A correction that
		// must REMOVE something is not a merge and stays a deliberate, reasoned edit.
		return inventory.MergeCorpusDocumentFile(*out, document)
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, append(encoded, '\n'), 0o644)
}

type fileList []string

func (f *fileList) String() string     { return strings.Join(*f, ",") }
func (f *fileList) Set(v string) error { *f = append(*f, v); return nil }

func runElideCorpus(args []string) error {
	flags := flag.NewFlagSet("elide-corpus", flag.ExitOnError)
	var files fileList
	flags.Var(&files, "file", "corpus document to re-normalize in place (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("elide-corpus requires at least one -file")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Decoded through the typed corpus envelope so the curated provenance fields keep both
		// their values and their order: re-normalizing a producer rule must show up in the diff
		// as the elision it is, not as a whole-file reshuffle.
		var document inventory.CorpusDocument
		if err := json.Unmarshal(raw, &document); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		count := matrix.ElideCaptureInstanceIdentity(&document.Inventory)
		document.Inventory.Normalize()
		encoded, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s: elided %d capture-instance identity label(s)\n", path, count)
	}
	return nil
}

func runReport(args []string) int {
	flags := flag.NewFlagSet("report", flag.ExitOnError)
	results := flags.String("results", "", "directory of per-permutation result records")
	out := flags.String("out", "", "combined Markdown report to write")
	jsonOut := flags.String("json", "", "combined machine-readable report to write")
	runID := flags.String("run-id", "", "matrix run identifier")
	generatedAt := flags.String("generated-at", "", "RFC3339 generation timestamp")
	maxParallel := flags.Int("max-parallel", 0, "concurrency bound this run used")
	note := flags.String("parallelism-note", "", "why that bound")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *results == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "lab-matrix: report requires -results and -out")
		return 2
	}
	records, err := matrix.LoadResults(*results)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lab-matrix: %v\n", err)
		return 2
	}
	candidates := map[string]inventory.Schema{}
	for _, record := range records {
		if !record.Outcome.Succeeded() || record.CandidatePath == "" {
			continue
		}
		schema, err := readSchema(record.CandidatePath)
		if err != nil {
			// A captured permutation whose candidate cannot be read must not be silently
			// dropped from the comparison; refuse rather than report a smaller matrix.
			fmt.Fprintf(os.Stderr, "lab-matrix: %s: %v\n", record.Permutation, err)
			return 2
		}
		candidates[record.Permutation] = schema
	}
	report := matrix.Build(*runID, *generatedAt, *maxParallel, *note, records, candidates)
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lab-matrix: %v\n", err)
		return 2
	}
	if err := os.WriteFile(*out, []byte(report.Markdown()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "lab-matrix: %v\n", err)
		return 2
	}
	if *jsonOut != "" {
		encoded, err := report.JSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "lab-matrix: %v\n", err)
			return 2
		}
		if err := os.WriteFile(*jsonOut, encoded, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "lab-matrix: %v\n", err)
			return 2
		}
	}
	fmt.Print(report.Markdown())
	return report.Verdict.ExitCode()
}

func readSchema(path string) (inventory.Schema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return inventory.Schema{}, err
	}
	var schema inventory.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return inventory.Schema{}, fmt.Errorf("%s: %w", path, err)
	}
	return schema, nil
}

func writeSchema(path string, schema inventory.Schema) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := schema.WriteJSON(file); err != nil {
		return err
	}
	return file.Close()
}
