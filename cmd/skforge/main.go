// SPDX-License-Identifier: AGPL-3.0-only

// Command skforge is our-side tooling for converting a captured environment inventory
// into a synthkit blueprint. It has three subcommands:
//
//	skforge inspect <capture> --key <passphrase-file> [--plain]
//	    Decrypts (or reads plain) a capture file and re-emits it as indented JSON.
//
//	skforge prompt <capture> --key <file> [--plain] [--report <path>]
//	    Decrypts, maps the deterministic skeleton, and emits a self-contained LLM prompt.
//	    Optionally writes a coverage report to --report.
//
//	skforge validate <blueprint.yaml>
//	    Loads a blueprint through the real registry + cardinality projection and prints
//	    the result. Exits non-zero if the blueprint is invalid.
//
// skforge is composition-layer tooling: it may import capture/forge/runner/bpsource.
// It is NOT a construct and must never be added to any construct/workload package.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/capture"
	"github.com/rknightion/synthkit/internal/forge"
	"github.com/rknightion/synthkit/internal/runner"
)

const usage = `skforge — synthkit blueprint forge

Usage:
  skforge inspect <capture>  --key <passphrase-file> [--plain]
  skforge prompt  <capture>  --key <file> [--plain] [--report <path>]
  skforge validate <blueprint.yaml>

Subcommands:
  inspect   Decrypt (or read plain) a capture file and print it as indented JSON.
  prompt    Decrypt, map a skeleton, and emit a self-contained LLM prompt to stdout.
            If --report is set, write a coverage report to that path.
  validate  Load a blueprint through the real registry + cardinality projection.
            Prints OK/Name/Cardinality/Estimated/Diagnostics. Exits non-zero if invalid.

Flags (inspect / prompt):
  --key   <file>   Path to a file containing the passphrase (trimmed). Required unless --plain.
  --plain          Skip decryption; treat the file as a plain JSON inventory.
  --report <path>  (prompt only) Write a coverage report to this path.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	sub := os.Args[1]
	switch sub {
	case "inspect":
		runInspect(os.Args[2:])
	case "prompt":
		runPrompt(os.Args[2:])
	case "validate":
		runValidate(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "skforge: unknown subcommand %q\n\n%s", sub, usage)
		os.Exit(1)
	}
}

// readCapture reads a capture file and returns the decrypted (or plain) bytes.
// keyFile and plain are mutually exclusive: plain=true skips decryption.
func readCapture(path, keyFile string, plain bool) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading capture file: %w", err)
	}
	if plain {
		return data, nil
	}
	if keyFile == "" {
		return nil, fmt.Errorf("--key <passphrase-file> is required unless --plain is set")
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("reading passphrase file: %w", err)
	}
	passphrase := strings.TrimSpace(string(raw))
	return capture.Decrypt(data, passphrase)
}

// splitPositional separates the first non-flag argument (the required positional path) from
// the remaining flag arguments. This lets callers write both
//
//	skforge inspect <capture> --key <file>   (positional first)
//	skforge inspect --plain <capture>         (flag first)
//
// because the stdlib flag package stops parsing at the first non-flag token.
func splitPositional(args []string) (positional string, rest []string) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a, append(args[:i:i], args[i+1:]...)
		}
	}
	return "", args
}

// runInspect implements: skforge inspect <capture> --key <file> [--plain]
func runInspect(args []string) {
	capturePath, flagArgs := splitPositional(args)
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	keyFile := fs.String("key", "", "path to passphrase file")
	plain := fs.Bool("plain", false, "skip decryption (plain JSON input)")
	if err := fs.Parse(flagArgs); err != nil {
		os.Exit(1)
	}
	if capturePath == "" {
		fmt.Fprintln(os.Stderr, "skforge inspect: missing <capture> argument")
		fs.Usage()
		os.Exit(1)
	}

	data, err := readCapture(capturePath, *keyFile, *plain)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skforge inspect: %v\n", err)
		os.Exit(1)
	}

	inv, err := capture.Unmarshal(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skforge inspect: %v\n", err)
		os.Exit(1)
	}

	out, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "skforge inspect: marshal: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

// runPrompt implements: skforge prompt <capture> --key <file> [--plain] [--report <path>]
func runPrompt(args []string) {
	capturePath, flagArgs := splitPositional(args)
	fs := flag.NewFlagSet("prompt", flag.ExitOnError)
	keyFile := fs.String("key", "", "path to passphrase file")
	plain := fs.Bool("plain", false, "skip decryption (plain JSON input)")
	report := fs.String("report", "", "write coverage report to this path")
	if err := fs.Parse(flagArgs); err != nil {
		os.Exit(1)
	}
	if capturePath == "" {
		fmt.Fprintln(os.Stderr, "skforge prompt: missing <capture> argument")
		fs.Usage()
		os.Exit(1)
	}

	data, err := readCapture(capturePath, *keyFile, *plain)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skforge prompt: %v\n", err)
		os.Exit(1)
	}

	inv, err := capture.Unmarshal(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skforge prompt: %v\n", err)
		os.Exit(1)
	}

	reg := runner.Catalog()
	sk, gaps := forge.MapSkeleton(inv, reg)

	prompt, err := forge.BuildPrompt(sk, gaps, forge.CatalogDescription(reg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "skforge prompt: build prompt: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(prompt)

	if *report != "" {
		rep := forge.CoverageReport(inv, gaps, reg)
		if err := os.WriteFile(*report, []byte(rep), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "skforge prompt: writing report: %v\n", err)
			os.Exit(1)
		}
	}
}

// runValidate implements: skforge validate <blueprint.yaml>
func runValidate(args []string) {
	bpPath, flagArgs := splitPositional(args)
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	if err := fs.Parse(flagArgs); err != nil {
		os.Exit(1)
	}
	if bpPath == "" {
		fmt.Fprintln(os.Stderr, "skforge validate: missing <blueprint.yaml> argument")
		fs.Usage()
		os.Exit(1)
	}

	data, err := os.ReadFile(bpPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skforge validate: reading blueprint: %v\n", err)
		os.Exit(1)
	}

	// NewManager with only the registry set. Validate only uses m.reg and
	// projectCardinality — it never touches cfg, git, or the data/baked dirs.
	// We supply a temp dir for dataDir so readManifest (called at construction)
	// has a valid path to stat; it returns an empty manifest if no file is found.
	tmpDir, err := os.MkdirTemp("", "skforge-validate-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "skforge validate: creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	m := bpsource.NewManager(bpsource.Options{
		Registry: runner.Catalog(),
		DataDir:  tmpDir,
	})

	res := m.Validate(data)

	fmt.Printf("OK:          %v\n", res.OK)
	fmt.Printf("Name:        %s\n", res.Name)
	fmt.Printf("Cardinality: %d\n", res.Cardinality)
	fmt.Printf("Estimated:   %v\n", res.Estimated)
	if len(res.Diagnostics) > 0 {
		fmt.Printf("Diagnostics:\n")
		for _, d := range res.Diagnostics {
			fmt.Printf("  - %s\n", d)
		}
	}

	if !res.OK {
		os.Exit(1)
	}
}
