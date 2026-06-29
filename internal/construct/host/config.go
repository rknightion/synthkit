// SPDX-License-Identifier: AGPL-3.0-only

package host

// Config is the blueprint-decoded configuration for one host construct. It is EMPTY:
// all per-host knobs (OS, profile, CPU/mem, docker, logs, OS identity) ride on the
// fixture.Host the resolver builds from the blueprint `hosts:` declaration — the
// ec2/rds precedent (empty config, fixture-carried). The construct is substrate-scoped
// (identity = `instance`=hostname) and never carries a blueprint label.
type Config struct{}
