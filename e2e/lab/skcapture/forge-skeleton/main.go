// SPDX-License-Identifier: AGPL-3.0-only

// Command forge-skeleton materializes skforge's deterministic skeleton for the disposable
// k3d proof. It deliberately does not call an LLM or add workloads: the resulting blueprint
// is the non-agent baseline that lets the harness exercise load and inventory generation.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rknightion/synthkit/internal/capture"
	"github.com/rknightion/synthkit/internal/forge"
	"github.com/rknightion/synthkit/internal/runner"
	"gopkg.in/yaml.v3"
)

func main() {
	capturePath := flag.String("capture", "", "plain JSON inventory written by skforge inspect")
	outPath := flag.String("out", "", "output blueprint YAML path")
	flag.Parse()
	if *capturePath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "forge-skeleton: -capture and -out are required")
		os.Exit(2)
	}

	raw, err := os.ReadFile(*capturePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge-skeleton: read capture: %v\n", err)
		os.Exit(1)
	}
	inv, err := capture.Unmarshal(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge-skeleton: parse capture: %v\n", err)
		os.Exit(1)
	}
	skeleton, _ := forge.MapSkeleton(inv, runner.Catalog())
	data, err := yaml.Marshal(skeleton)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge-skeleton: marshal skeleton: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "forge-skeleton: write blueprint: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(skeleton.Name)
}
