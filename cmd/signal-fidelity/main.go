// SPDX-License-Identifier: AGPL-3.0-only

// Command signal-fidelity compares a synthkit inventory export with the committed
// substrate-scoped reality corpus and prints a report-only findings document.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/rknightion/synthkit/internal/inventory"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "signal-fidelity:", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("signal-fidelity", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	synthPath := flags.String("synth", "", "path to a synthkit -inventory-json export")
	corpusPath := flags.String("corpus", "reality-corpus", "path to the committed reality corpus")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if *synthPath == "" {
		return errors.New("-synth is required")
	}
	if *corpusPath == "" {
		return errors.New("-corpus must not be empty")
	}

	synth, err := loadSynthInventory(*synthPath)
	if err != nil {
		return err
	}
	documents, err := inventory.LoadCorpusDir(*corpusPath)
	if err != nil {
		return err
	}
	return inventory.WriteFindingsReport(output, inventory.CompareCorpus(synth, documents))
}

func loadSynthInventory(path string) (inventory.Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return inventory.Schema{}, fmt.Errorf("read synth inventory %q: %w", path, err)
	}

	var raw map[string]json.RawMessage
	if err := decodeSingleJSON(data, &raw, false); err != nil {
		return inventory.Schema{}, fmt.Errorf("decode synth inventory %q: %w", path, err)
	}
	if raw == nil {
		return inventory.Schema{}, fmt.Errorf("decode synth inventory %q: document must be a JSON object", path)
	}
	for _, field := range []string{"schema_version", "metrics", "logs", "traces", "profiles", "sigil", "receipts"} {
		if _, ok := raw[field]; !ok {
			return inventory.Schema{}, fmt.Errorf("decode synth inventory %q: missing required field %s", path, field)
		}
	}

	var schema inventory.Schema
	if err := decodeSingleJSON(data, &schema, true); err != nil {
		return inventory.Schema{}, fmt.Errorf("decode synth inventory %q: %w", path, err)
	}
	if schema.SchemaVersion != inventory.SchemaVersion {
		return inventory.Schema{}, fmt.Errorf(
			"decode synth inventory %q: schema_version got %q, want %q",
			path, schema.SchemaVersion, inventory.SchemaVersion,
		)
	}
	return schema, nil
}

func decodeSingleJSON(data []byte, target any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	err := decoder.Decode(&trailing)
	switch {
	case err == io.EOF:
		return nil
	case err == nil:
		return errors.New("multiple JSON documents are not allowed")
	default:
		return fmt.Errorf("trailing JSON data: %w", err)
	}
}
