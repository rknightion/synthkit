// SPDX-License-Identifier: AGPL-3.0-only

// Command blueprint-schema regenerates the committed blueprint-schema artifacts from the live
// Go types: the embedded field-description index (internal/blueprintschema/fielddocs.json) and
// the human reference (BLUEPRINT-SCHEMA.md). Run via `make blueprint-schema`. The
// TestSchemaCurrent gate fails if these drift from the live types/doc comments.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/rknightion/synthkit/internal/blueprintschema"
	"github.com/rknightion/synthkit/internal/runner"
)

func main() {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	root, err := blueprintschema.FindModuleRoot(wd)
	if err != nil {
		log.Fatalf("find module root: %v", err)
	}

	docsJSON, markdown, err := blueprintschema.Generate(runner.Catalog(), root)
	if err != nil {
		log.Fatalf("generate: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, blueprintschema.DocsIndexPath), docsJSON, 0o644); err != nil {
		log.Fatalf("write docs index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, blueprintschema.MarkdownPath), []byte(markdown), 0o644); err != nil {
		log.Fatalf("write markdown: %v", err)
	}
	log.Printf("wrote %s + %s", blueprintschema.DocsIndexPath, blueprintschema.MarkdownPath)
}
