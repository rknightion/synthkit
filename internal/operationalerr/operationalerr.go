// SPDX-License-Identifier: AGPL-3.0-only

// Package operationalerr classifies operational failures into a closed set of
// codes that are safe to expose in logs and status surfaces.
package operationalerr

import (
	"context"
	"errors"
	"net"
)

// Code identifies a safe operational failure category.
type Code string

const (
	CodeNone           Code = ""
	CodeCanceled       Code = "canceled"
	CodeTimeout        Code = "timeout"
	CodeAuthentication Code = "authentication"
	CodePermission     Code = "permission"
	CodeRateLimited    Code = "rate_limited"
	CodeRejected       Code = "rejected"
	CodeTransport      Code = "transport"
	CodeInternal       Code = "internal"
)

type codedError struct {
	code Code
}

func (e codedError) Error() string         { return Message(e.code) }
func (e codedError) OperationalCode() Code { return e.code }

// New returns an error containing only a closed operational code and its fixed
// safe message. CodeNone produces nil.
func New(code Code) error {
	code = Normalize(code)
	if code == CodeNone {
		return nil
	}
	return codedError{code: code}
}

// CodeOf finds a wrapped operational code or safely classifies err.
func CodeOf(err error) Code {
	if err == nil {
		return CodeNone
	}
	var coded interface{ OperationalCode() Code }
	if errors.As(err, &coded) {
		return Normalize(coded.OperationalCode())
	}
	return Classify(0, err)
}

// Message returns fixed, credential-safe text for code.
func Message(code Code) string {
	switch code {
	case CodeNone:
		return ""
	case CodeCanceled:
		return "operation canceled"
	case CodeTimeout:
		return "operation timed out"
	case CodeAuthentication:
		return "authentication failed"
	case CodePermission:
		return "permission denied"
	case CodeRateLimited:
		return "rate limited"
	case CodeRejected:
		return "request rejected"
	case CodeTransport:
		return "transport failed"
	case CodeInternal:
		return "internal error"
	default:
		return "internal error"
	}
}

// Normalize maps any value outside the closed code set to CodeInternal.
func Normalize(code Code) Code {
	switch code {
	case CodeNone,
		CodeCanceled,
		CodeTimeout,
		CodeAuthentication,
		CodePermission,
		CodeRateLimited,
		CodeRejected,
		CodeTransport,
		CodeInternal:
		return code
	default:
		return CodeInternal
	}
}

// Classify reduces an HTTP status and error to a safe operational code.
func Classify(status int, err error) Code {
	switch status {
	case 401:
		return CodeAuthentication
	case 403:
		return CodePermission
	case 429:
		return CodeRateLimited
	default:
		if status >= 400 {
			return CodeRejected
		}
	}
	var coded interface{ OperationalCode() Code }
	if errors.As(err, &coded) {
		return Normalize(coded.OperationalCode())
	}
	if errors.Is(err, context.Canceled) {
		return CodeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return CodeTimeout
		}
		return CodeTransport
	}
	if err != nil {
		return CodeInternal
	}
	return CodeNone
}
