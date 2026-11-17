// SPDX-License-Identifier: AGPL-3.0-only

// Package pipelinerun is the shared mechanic for blueprint-declared batch data pipelines: a
// pipeline identity, a cadence and an ordered stage list, turned into a deterministic run
// simulation that the AWS data-service constructs (glue, mwaa, and later lambda/sqs/redshift)
// read through one seam, Window.StagesFor.
//
// It owns NO metric, label or log name. Each consumer maps the per-stage window facts (started,
// succeeded, failed, running, pending, oldest pending age, busy seconds) onto its own sourced
// CloudWatch names. It is a peer library like internal/cw: catalog packages may import it, it
// imports only the failuremode leaf.
//
// Determinism: no wall clock and no global RNG. Run trigger times are epoch anchored; per-run
// stage durations and failure draws are fnv hashes over (seed, pipeline, run index, stage).
package pipelinerun

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/rknightion/synthkit/internal/failuremode"
)

// Service values a stage may declare. Glue and Airflow have emitting consumers; Lambda, SQS and
// Redshift stages shape the timeline but have no consumer until their CloudWatch families are
// sourced in signals/cw.md.
const (
	ServiceGlue     = "glue"
	ServiceAirflow  = "airflow"
	ServiceLambda   = "lambda"
	ServiceSQS      = "sqs"
	ServiceRedshift = "redshift"
)

var services = map[string]bool{
	ServiceGlue: true, ServiceAirflow: true, ServiceLambda: true, ServiceSQS: true, ServiceRedshift: true,
}

// AxisStage is the failure-mode axis for one declared pipeline stage. Target names on it are
// StageScope(pipeline, stage).
const AxisStage failuremode.Axis = "pipeline_stage"

// Failure-mode names.
const (
	ModeStageStall = "stage_stall"
	ModeRunFailure = "run_failure"
)

// FailureModes is the stage vocabulary every pipeline consumer registers.
var FailureModes = []failuremode.Mode{
	{Name: ModeStageStall, Axis: AxisStage, Help: "stage starts nothing: arriving runs wait and age, later stages complete nothing until cleared"},
	{Name: ModeRunFailure, Axis: AxisStage, Help: "runs completing the stage fail (fraction = intensity) and end with a FAILED outcome"},
}

// StageScope is the shape-engine scope and failure target name of one stage. The composition
// root must enforce blueprint-wide pipeline-name uniqueness before publishing these targets;
// the MWAA Environment field is an orchestrator name, not a stage-scope namespace.
func StageScope(pipeline, stage string) string { return pipeline + "." + stage }

// Pipeline is one declared pipeline. It is also the blueprint YAML shape.
type Pipeline struct {
	// Name is the pipeline identity (the Airflow DAG id when MWAA orchestrates it). It must be
	// unique across all blueprint environments, not only this consumer's pipeline list.
	Name string `yaml:"name"`
	// Environment is the MWAA environment orchestrating the pipeline; "" when none does.
	Environment string `yaml:"environment"`
	// Every is the run cadence as a Go duration, 1m to 168h. Real batch pipelines run hourly or
	// daily; sub-hour cadences belong in e2e fixtures.
	Every string `yaml:"every"`
	// Offset is the trigger phase inside Every; "" derives a minute-aligned phase from the seed.
	Offset string `yaml:"offset"`
	// Stages run in order; each run passes through every stage unless one fails.
	Stages []Stage `yaml:"stages"`
}

// Stage is one step of a pipeline.
type Stage struct {
	// Name is the stage (Airflow task) id.
	Name string `yaml:"name"`
	// Service is glue, airflow, lambda, sqs or redshift.
	Service string `yaml:"service"`
	// Resource is the executing resource: Glue job name, function, queue or cluster. Required
	// except for airflow stages (which run on the orchestrating environment's workers).
	Resource string `yaml:"resource"`
	// Duration is the nominal run time of the stage, a Go duration of at least 1s.
	Duration string `yaml:"duration"`
}

// Consumer is implemented by a construct Config that accepts the environment's declared
// pipelines from the composition root (the blueprint resolver), so the declaration is written
// once and fans into every consuming construct.
type Consumer interface {
	UsePipelines([]Pipeline)
}

var namePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

const (
	minEvery = time.Minute
	maxEvery = 7 * 24 * time.Hour
)

// Validate checks a pipeline list: names, cadence, offset, stage services and durations.
func Validate(ps []Pipeline) error {
	_, err := compileAll(ps)
	return err
}

type compiled struct {
	decl   Pipeline
	every  time.Duration
	offset time.Duration
	hasOff bool
	durs   []time.Duration // nominal, per stage
}

func compileAll(ps []Pipeline) ([]compiled, error) {
	seen := map[string]bool{}
	out := make([]compiled, 0, len(ps))
	for _, p := range ps {
		c, err := compile(p)
		if err != nil {
			return nil, err
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("pipeline %q: declared twice", p.Name)
		}
		seen[p.Name] = true
		out = append(out, c)
	}
	return out, nil
}

func compile(p Pipeline) (compiled, error) {
	if !namePattern.MatchString(p.Name) {
		return compiled{}, fmt.Errorf("pipeline %q: name must match %s", p.Name, namePattern)
	}
	every, err := time.ParseDuration(p.Every)
	if err != nil {
		return compiled{}, fmt.Errorf("pipeline %q: every %q: %w", p.Name, p.Every, err)
	}
	if every < minEvery || every > maxEvery {
		return compiled{}, fmt.Errorf("pipeline %q: every %v must be within [%v, %v]", p.Name, every, minEvery, maxEvery)
	}
	c := compiled{decl: p, every: every}
	if p.Offset != "" {
		off, err := time.ParseDuration(p.Offset)
		if err != nil {
			return compiled{}, fmt.Errorf("pipeline %q: offset %q: %w", p.Name, p.Offset, err)
		}
		if off < 0 || off >= every {
			return compiled{}, fmt.Errorf("pipeline %q: offset %v must be within [0, every)", p.Name, off)
		}
		c.offset, c.hasOff = off, true
	}
	if len(p.Stages) == 0 {
		return compiled{}, errors.New("pipeline " + p.Name + ": needs at least one stage")
	}
	stageSeen := map[string]bool{}
	for _, s := range p.Stages {
		if !namePattern.MatchString(s.Name) {
			return compiled{}, fmt.Errorf("pipeline %q stage %q: name must match %s", p.Name, s.Name, namePattern)
		}
		if stageSeen[s.Name] {
			return compiled{}, fmt.Errorf("pipeline %q: stage %q declared twice", p.Name, s.Name)
		}
		stageSeen[s.Name] = true
		if !services[s.Service] {
			return compiled{}, fmt.Errorf("pipeline %q stage %q: unknown service %q (glue|airflow|lambda|sqs|redshift)", p.Name, s.Name, s.Service)
		}
		if s.Resource == "" && s.Service != ServiceAirflow {
			return compiled{}, fmt.Errorf("pipeline %q stage %q: %s stage needs a resource", p.Name, s.Name, s.Service)
		}
		d, err := time.ParseDuration(s.Duration)
		if err != nil {
			return compiled{}, fmt.Errorf("pipeline %q stage %q: duration %q: %w", p.Name, s.Name, s.Duration, err)
		}
		if d < time.Second {
			return compiled{}, fmt.Errorf("pipeline %q stage %q: duration %v must be at least 1s", p.Name, s.Name, d)
		}
		c.durs = append(c.durs, d)
	}
	return c, nil
}
