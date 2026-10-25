// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

// Draw returns a uniform [0,1) value for a purpose; deterministic for a fixed seed unit.
type Draw func(purpose string) float64

// UnitDraw hashes a per-request seed unit: u(purpose) is the first 8 bytes (big endian) of
// sha256(unit + "\x00causal\x00" + purpose), shifted right by 11 and divided by 2^53. No wall
// clock, no global RNG and no shape-engine RNG.
func UnitDraw(seedUnit string) Draw {
	return func(purpose string) float64 {
		sum := sha256.Sum256([]byte(seedUnit + "\x00causal\x00" + purpose))
		return float64(binary.BigEndian.Uint64(sum[:8])>>11) / (1 << 53)
	}
}

// Scope returns a Draw whose purposes are prefixed with prefix + "/". Consumers scope a request's
// draw to `<node>/<resource>/<attempt>` before calling Apply, which asks for the leaf purposes
// "fail" and "wait".
func (d Draw) Scope(prefix string) Draw {
	return func(purpose string) float64 { return d(prefix + "/" + purpose) }
}

// FailureClass classifies a failure for caller policy and status mapping.
type FailureClass string

// Failure classes.
const (
	ClassNone       FailureClass = ""
	ClassHTTP       FailureClass = "http"
	ClassThrottle   FailureClass = "throttle"
	ClassTimeout    FailureClass = "timeout"
	ClassConnect    FailureClass = "connect"
	ClassCredential FailureClass = "credential"
)

// Effect is the resolution of one arrival at one resource.
type Effect struct {
	Resource      string
	Kind          Kind
	Mode          string // fault mode, "" for emergent saturation
	Fail          bool
	Class         FailureClass
	Status        int           // 0 when the failure has no HTTP status
	Transport     bool          // origin is the caller's client span (else the callee's server span)
	Wait          time.Duration // added before service (admitted or until failure)
	LatencyFactor float64       // 1 when healthy
	ErrorType     string        // error.type value (failures only)
	Exception     string        // exception.type value; "" = no exception event
	Message       string        // human message naming the cause (failures and slowed arrivals)
}

// Slowed reports whether an admitted arrival was delayed or stretched.
func (e Effect) Slowed() bool { return !e.Fail && (e.Wait > 0 || e.LatencyFactor > 1) }

// Apply resolves one arrival at the named resource from its state at at (the last grid point at
// or before at). An unknown resource or a time with no retained state is a no-op effect.
func (m *Model) Apply(name string, at time.Time, d Draw) Effect {
	m.mu.Lock()
	c, okc := m.byName[name]
	r, ok := m.recAt(name, at)
	m.mu.Unlock()
	e := Effect{Resource: name, LatencyFactor: 1}
	if !okc || !ok {
		return e
	}
	st := r.st
	e.Kind = c.r.Kind
	e.Mode = st.Mode
	failed := func() bool { return st.RejectShare > 0 && d("fail") < st.RejectShare }
	switch c.r.Kind {
	case KindPool:
		T := msDuration(c.r.AcquireTimeoutMS)
		if failed() {
			c.fail(&e, 503)
			e.Wait = T
		} else if st.WaitMS > 0 {
			e.Wait = min(msDuration(st.WaitMS*(0.5+d("wait"))), T)
			e.Message = c.slowMessage(e.Mode)
		}
	case KindConcurrency, KindQuota:
		if failed() {
			c.fail(&e, 429)
		}
	case KindQueue:
		if failed() {
			c.fail(&e, 503)
		}
	case KindDependency:
		if failed() {
			c.fail(&e, 0)
			e.Class, e.Transport = ClassConnect, true
			e.Mode = ModeDependencyUnavailable
		} else if st.LatencyFactor > 1 {
			e.LatencyFactor = st.LatencyFactor
			e.Mode = ModeDependencySlow
			e.Message = c.slowMessage(e.Mode)
		}
	case KindCredential:
		if failed() {
			status := 0
			if c.r.CredentialType == CredentialToken {
				status = 401
			}
			c.fail(&e, status)
			// A certificate fails the caller's handshake; an expired token is refused by the
			// callee's server, which answers 401.
			e.Class, e.Transport = ClassCredential, c.r.CredentialType != CredentialToken
		}
	case KindRule:
		if failed() {
			c.fail(&e, 422)
		}
	}
	return e
}

// ApplyConsumer resolves one message taken from the named queue by a consumer at at: the
// message waited the oldest-message age (FIFO), or backlog over service rate when no age is
// known. Non-queue resources and times with no retained state give a no-op effect.
func (m *Model) ApplyConsumer(name string, at time.Time) Effect {
	m.mu.Lock()
	c, okc := m.byName[name]
	r, ok := m.recAt(name, at)
	m.mu.Unlock()
	e := Effect{Resource: name, LatencyFactor: 1}
	if !okc || !ok || c.r.Kind != KindQueue {
		return e
	}
	st := r.st
	e.Kind, e.Mode = c.r.Kind, st.Mode
	wait := st.OldestAgeSec * 1000
	if wait == 0 && st.Backlog > 0 {
		wait = st.WaitMS
	}
	if wait > 0 {
		e.Wait = msDuration(wait)
		e.Message = fmt.Sprintf("%s: queue backlog (oldest message %s s)", c.r.Name, strconv.FormatFloat(math.Round(st.OldestAgeSec), 'f', -1, 64))
	}
	return e
}

func msDuration(ms float64) time.Duration {
	if ms <= 0 || math.IsNaN(ms) {
		return 0
	}
	return time.Duration(math.Round(ms * float64(time.Millisecond)))
}

// fail fills a failing effect: status (template override first), class, error type, exception
// and message.
func (c *compiled) fail(e *Effect, status int) {
	t := ErrorTemplate{}
	if c.r.Error != nil {
		t = *c.r.Error
	}
	if t.Status != 0 {
		status = t.Status
	}
	e.Fail = true
	e.Status = status
	switch {
	case status == 429:
		e.Class = ClassThrottle
	case status != 0:
		e.Class = ClassHTTP
	}
	e.ErrorType = t.Type
	if e.ErrorType == "" {
		if status != 0 {
			e.ErrorType = strconv.Itoa(status)
		} else {
			e.ErrorType = "_OTHER"
		}
	}
	e.Exception = t.Exception
	if e.Exception == "" && classLike.MatchString(t.Type) && hasUpper.MatchString(t.Type) {
		e.Exception = t.Type
	}
	e.Message = t.Message
	if e.Message == "" {
		e.Message = c.failMessage(e.Mode)
	}
}

// classLike is a dotted identifier (a language exception class such as
// SQLTransientConnectionException or java.net.ConnectException). Together with hasUpper it
// excludes status strings, "timeout" and "_OTHER", so only a declared class name becomes an
// exception event.
var (
	classLike = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$]*(\.[A-Za-z][A-Za-z0-9_$]*)*$`)
	hasUpper  = regexp.MustCompile(`[A-Z]`)
)

func (c *compiled) failMessage(mode string) string {
	var phrase string
	switch c.r.Kind {
	case KindPool:
		if mode == ModePoolExhausted {
			phrase = fmt.Sprintf("connection pool exhausted (acquire timeout %s ms)", strconv.FormatFloat(c.r.AcquireTimeoutMS, 'f', -1, 64))
		} else {
			phrase = "connection pool saturated"
		}
	case KindConcurrency:
		if mode == ModeConcurrencyReduced {
			phrase = "concurrency limit reduced"
		} else {
			phrase = "concurrency limit reached"
		}
	case KindQuota:
		if mode == ModeQuotaReduced {
			phrase = "rate quota reduced"
		} else {
			phrase = "rate quota exceeded"
		}
	case KindQueue:
		if mode == ModeConsumerSlowdown {
			phrase = "queue full (consumers slowed)"
		} else {
			phrase = "queue full"
		}
	case KindDependency:
		phrase = "dependency unavailable (connection failed)"
	case KindCredential:
		switch c.r.CredentialType {
		case CredentialServerCertificate:
			phrase = "server certificate expired"
		case CredentialClientCertificate:
			phrase = "client certificate expired"
		default:
			phrase = "token expired"
		}
	case KindRule:
		phrase = "rule regression rejected the record"
	}
	return c.r.Name + ": " + phrase
}

func (c *compiled) slowMessage(mode string) string {
	switch {
	case c.r.Kind == KindDependency:
		return c.r.Name + ": dependency slow"
	case mode == ModePoolExhausted:
		return c.r.Name + ": connection pool exhausted"
	default:
		return c.r.Name + ": connection pool saturated"
	}
}
