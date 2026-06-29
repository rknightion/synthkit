// SPDX-License-Identifier: AGPL-3.0-only

// Package profiling instruments the synthkit PROCESS itself with continuous profiling
// (Grafana Pyroscope), as distinct from the synthetic telemetry synthkit emits.
//
// Path separation is deliberate and load-bearing: profiles are synthkit's own real
// observability and ship to a SEPARATE (staff) stack via their own credential triplet
// (GC_PYROSCOPE_URL / GC_PYROSCOPE_USER / GC_PYROSCOPE_PASSWORD) — all synthetic data
// (Mimir/Loki/OTLP/Faro) continues to flow to the configured target stack untouched. Hence
// this package never reuses GC_TOKEN; it takes its own ServerAddress + Basic-auth user/password.
//
// Gating is decoupled from DRY_RUN: profiling the process is unrelated to whether synthetic
// data is being pushed, so an enabled profiler runs even for local dry-run dev sessions.
package profiling

import (
	"errors"
	"log"
	"maps"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/grafana/pyroscope-go"
)

// appName is the Pyroscope ApplicationName for the synthkit binary.
const appName = "synthkit"

// Options configures the profiler. The connection triplet (URL/User/Password) is distinct from
// every other sink's credentials — it targets a separate Profiles instance, not the synthetic-data stack.
type Options struct {
	Enabled  bool   // master on/off — shares SELFOBS_ENABLED (set by the composition root)
	URL      string // GC_PYROSCOPE_URL — Profiles ingest ServerAddress
	User     string // GC_PYROSCOPE_USER — Basic-auth user = Profiles instance ID
	Password string // GC_PYROSCOPE_PASSWORD — token with profiles:write scope

	Tags          map[string]string // extra labels (e.g. env=…); merged over the built-ins
	MutexFraction int               // runtime.SetMutexProfileFraction (0 = mutex profiling off)
	BlockRate     int               // runtime.SetBlockProfileRate, ns (0 = block profiling off)
	Version       string            // stamped as the "version" tag (git sha or "dev")
}

// Start begins continuous profiling if enabled and fully configured. It returns (nil, nil) — not an
// error — when profiling is disabled or credentials are missing, so callers can unconditionally:
//
//	prof, err := profiling.Start(opts)
//	if err != nil { log.Printf("profiling: %v", err) }
//	if prof != nil { defer prof.Stop() }
//
// A disabled/misconfigured profiler is a logged no-op, never a fatal startup condition.
func Start(opts Options) (*pyroscope.Profiler, error) {
	if !opts.Enabled {
		return nil, nil
	}
	if opts.URL == "" || opts.User == "" || opts.Password == "" {
		log.Printf("profiling: SELFOBS_ENABLED=true but GC_PYROSCOPE_URL/USER/PASSWORD incomplete — skipping process profiles")
		return nil, nil
	}

	// Mutex and block profiling are opt-in at the runtime level: the Pyroscope ProfileType is
	// inert unless the corresponding runtime sampling rate is set. Guard on > 0 so a zero value
	// genuinely disables that profile rather than silently sampling nothing.
	if opts.MutexFraction > 0 {
		runtime.SetMutexProfileFraction(opts.MutexFraction)
	}
	if opts.BlockRate > 0 {
		runtime.SetBlockProfileRate(opts.BlockRate)
	}

	cfg := pyroscope.Config{
		ApplicationName:   appName,
		ServerAddress:     opts.URL,
		BasicAuthUser:     opts.User,
		BasicAuthPassword: opts.Password,
		Logger:            newPyroscopeLogger(),
		Tags:              buildTags(opts.Version, opts.Tags),
		ProfileTypes:      profileTypes(opts.MutexFraction, opts.BlockRate),
	}

	prof, err := pyroscope.Start(cfg)
	if err != nil {
		return nil, err
	}
	log.Printf("profiling: started → %s (app=%s, mutex_fraction=%d, block_rate=%d, tags=%v)",
		redactURL(opts.URL), appName, opts.MutexFraction, opts.BlockRate, cfg.Tags)
	return prof, nil
}

// errorOnlyLogger adapts pyroscope-go's Logger interface to suppress its routine chatter. The SDK
// logs every profile send at Debug ("uploading at …") and its session banner at Info; left on
// StandardLogger that floods synthkit's own log on each upload cycle. We forward only Errorf
// — the SDK has no Warn level, so Errorf is its highest (and only) above-info severity — through
// the standard log package, so Pyroscope shares the app's logging pipeline and emits only when an
// upload/profiling failure actually occurs.
type errorOnlyLogger struct {
	out *log.Logger
}

func (*errorOnlyLogger) Infof(string, ...any)  {}
func (*errorOnlyLogger) Debugf(string, ...any) {}
func (l *errorOnlyLogger) Errorf(format string, args ...any) {
	l.out.Printf("profiling: "+format, args...)
}

// newPyroscopeLogger returns the error-only logger wired to the default logger, matching the
// log.Printf destination used elsewhere in this package.
func newPyroscopeLogger() pyroscope.Logger {
	return &errorOnlyLogger{out: log.Default()}
}

// profileTypes assembles the enabled profile set: the 5 SDK defaults (CPU + alloc/inuse heap) plus
// goroutines, and mutex/block only when their runtime sampling rate is active.
func profileTypes(mutexFraction, blockRate int) []pyroscope.ProfileType {
	types := []pyroscope.ProfileType{
		pyroscope.ProfileCPU,
		pyroscope.ProfileAllocObjects,
		pyroscope.ProfileAllocSpace,
		pyroscope.ProfileInuseObjects,
		pyroscope.ProfileInuseSpace,
		pyroscope.ProfileGoroutines,
	}
	if mutexFraction > 0 {
		types = append(types, pyroscope.ProfileMutexCount, pyroscope.ProfileMutexDuration)
	}
	if blockRate > 0 {
		types = append(types, pyroscope.ProfileBlockCount, pyroscope.ProfileBlockDuration)
	}
	return types
}

// buildTags returns the base tags (service_name, version) with caller-supplied tags merged on top.
func buildTags(version string, extra map[string]string) map[string]string {
	if version == "" {
		version = "dev"
	}
	tags := map[string]string{
		"service_name": appName,
		"version":      version,
	}
	maps.Copy(tags, extra)
	return tags
}

// ParseTags parses a CSV of k=v pairs (e.g. "env=prod,team=platform") into a tag map. Whitespace is
// trimmed; entries without '=' or with an empty key are skipped. Returns nil for an empty input.
func ParseTags(csv string) map[string]string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	out := map[string]string{}
	for pair := range strings.SplitSeq(csv, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// redactURL strips any userinfo (credentials) from a URL so it is safe to log. On parse failure or
// empty input it returns "<unparseable endpoint>" as a hard sentinel — credentials are never
// silently leaked by an edge-case URL format. Duplicated from internal/selfobs to preserve package
// isolation (selfobs must not be imported by profiling or any non-self-obs package).
func redactURL(raw string) string {
	if raw == "" {
		return "<unparseable endpoint>"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable endpoint>"
	}
	u.User = nil
	return u.String()
}

// stopper is satisfied by *pyroscope.Profiler (its Stop method returns an error).
type stopper interface{ Stop() error }

// StopWithTimeout calls s.Stop() on a goroutine and returns its error, or returns an error if d
// elapses first. When the timeout fires the goroutine is abandoned (not killed — the profiler SDK
// has no cancel path), but the process can exit cleanly because we no longer block on it.
func StopWithTimeout(s stopper, d time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- s.Stop() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		return errors.New("profiling: Stop() timed out; profiler goroutine abandoned")
	}
}
