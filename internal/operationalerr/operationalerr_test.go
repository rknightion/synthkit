// SPDX-License-Identifier: AGPL-3.0-only

package operationalerr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"testing"
)

type testNetError struct {
	timeout bool
}

func (e testNetError) Error() string   { return "network failure" }
func (e testNetError) Timeout() bool   { return e.timeout }
func (e testNetError) Temporary() bool { return false }

type testCodedError struct {
	code Code
	text string
}

func (e testCodedError) Error() string         { return e.text }
func (e testCodedError) OperationalCode() Code { return e.code }

func TestCodeValuesAreStable(t *testing.T) {
	tests := []struct {
		code Code
		want string
	}{
		{code: CodeNone, want: ""},
		{code: CodeCanceled, want: "canceled"},
		{code: CodeTimeout, want: "timeout"},
		{code: CodeAuthentication, want: "authentication"},
		{code: CodePermission, want: "permission"},
		{code: CodeRateLimited, want: "rate_limited"},
		{code: CodeRejected, want: "rejected"},
		{code: CodeTransport, want: "transport"},
		{code: CodeInternal, want: "internal"},
	}
	for _, tt := range tests {
		if got := string(tt.code); got != tt.want {
			t.Errorf("code = %q, want %q", got, tt.want)
		}
	}
}

func TestClassifyHTTPStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   Code
	}{
		{name: "no status", status: 0, want: CodeNone},
		{name: "success", status: 204, want: CodeNone},
		{name: "authentication", status: 401, want: CodeAuthentication},
		{name: "permission", status: 403, want: CodePermission},
		{name: "rate limited", status: 429, want: CodeRateLimited},
		{name: "other client rejection", status: 422, want: CodeRejected},
		{name: "server rejection", status: 503, want: CodeRejected},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.status, nil); got != tt.want {
				t.Fatalf("Classify(%d, nil) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestClassifyUnknownErrorAsInternal(t *testing.T) {
	if got := Classify(0, errors.New("opaque failure")); got != CodeInternal {
		t.Fatalf("Classify(0, err) = %q, want %q", got, CodeInternal)
	}
}

func TestClassifyContextErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Code
	}{
		{name: "canceled", err: context.Canceled, want: CodeCanceled},
		{name: "wrapped canceled", err: fmt.Errorf("request stopped: %w", context.Canceled), want: CodeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: CodeTimeout},
		{name: "wrapped deadline", err: fmt.Errorf("request stopped: %w", context.DeadlineExceeded), want: CodeTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(0, tt.err); got != tt.want {
				t.Fatalf("Classify(0, err) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyTypedNetworkErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Code
	}{
		{name: "timeout", err: testNetError{timeout: true}, want: CodeTimeout},
		{name: "transport", err: testNetError{}, want: CodeTransport},
		{name: "wrapped transport", err: fmt.Errorf("send: %w", testNetError{}), want: CodeTransport},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(0, tt.err); got != tt.want {
				t.Fatalf("Classify(0, err) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMessageUsesFixedText(t *testing.T) {
	tests := []struct {
		code Code
		want string
	}{
		{code: CodeNone, want: ""},
		{code: CodeCanceled, want: "operation canceled"},
		{code: CodeTimeout, want: "operation timed out"},
		{code: CodeAuthentication, want: "authentication failed"},
		{code: CodePermission, want: "permission denied"},
		{code: CodeRateLimited, want: "rate limited"},
		{code: CodeRejected, want: "request rejected"},
		{code: CodeTransport, want: "transport failed"},
		{code: CodeInternal, want: "internal error"},
		{code: Code("not-closed"), want: "internal error"},
	}

	for _, tt := range tests {
		if got := Message(tt.code); got != tt.want {
			t.Errorf("Message(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestNewReturnsCodedError(t *testing.T) {
	if err := New(CodeNone); err != nil {
		t.Fatalf("New(CodeNone) = %v, want nil", err)
	}

	err := New(CodeTimeout)
	if err == nil {
		t.Fatal("New(CodeTimeout) = nil")
	}
	if err.Error() != Message(CodeTimeout) {
		t.Fatalf("Error() = %q, want %q", err.Error(), Message(CodeTimeout))
	}
	var coded interface{ OperationalCode() Code }
	if !errors.As(err, &coded) {
		t.Fatal("New(CodeTimeout) does not support OperationalCode")
	}
	if got := coded.OperationalCode(); got != CodeTimeout {
		t.Fatalf("OperationalCode() = %q, want %q", got, CodeTimeout)
	}
}

func TestNewNormalizesUnknownCode(t *testing.T) {
	err := New(Code("untrusted"))
	if err == nil {
		t.Fatal("New(unknown) = nil")
	}
	if got := CodeOf(err); got != CodeInternal {
		t.Fatalf("CodeOf(New(unknown)) = %q, want %q", got, CodeInternal)
	}
	if err.Error() != Message(CodeInternal) {
		t.Fatalf("Error() = %q, want %q", err.Error(), Message(CodeInternal))
	}
}

func TestNormalizeRejectsUntrustedCodeText(t *testing.T) {
	if got := Normalize(Code("credential-canary")); got != CodeInternal {
		t.Fatalf("Normalize(untrusted)=%q, want %q", got, CodeInternal)
	}
}

func TestCodeOfRecognizesWrappedCodedError(t *testing.T) {
	err := fmt.Errorf("outer: %w", testCodedError{code: CodeRateLimited, text: "untrusted detail"})
	if got := CodeOf(err); got != CodeRateLimited {
		t.Fatalf("CodeOf(wrapped coded error) = %q, want %q", got, CodeRateLimited)
	}
}

func TestCodeOfClassifiesOtherErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Code
	}{
		{name: "nil", err: nil, want: CodeNone},
		{name: "context", err: context.Canceled, want: CodeCanceled},
		{name: "network", err: testNetError{}, want: CodeTransport},
		{name: "unknown", err: errors.New("untrusted detail"), want: CodeInternal},
		{name: "unknown coded value", err: testCodedError{code: Code("untrusted"), text: "untrusted detail"}, want: CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeOf(tt.err); got != tt.want {
				t.Fatalf("CodeOf(err) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSafeOutputsNeverContainCredentialCanary(t *testing.T) {
	const canary = `sk-live:<abc&"'>/+=?`
	jsonCanary, err := json.Marshal(canary)
	if err != nil {
		t.Fatalf("marshal canary: %v", err)
	}
	forms := map[string]string{
		"raw":          canary,
		"URL encoded":  url.QueryEscape(canary),
		"Basic base64": base64.StdEncoding.EncodeToString([]byte("operator:" + canary)),
		"bearer":       "Bearer " + canary,
		"JSON escaped": string(jsonCanary),
		"HTML escaped": html.EscapeString(canary),
	}

	codes := []Code{
		CodeNone,
		CodeCanceled,
		CodeTimeout,
		CodeAuthentication,
		CodePermission,
		CodeRateLimited,
		CodeRejected,
		CodeTransport,
		CodeInternal,
		Code(canary),
	}
	for _, code := range codes {
		outputs := []string{Message(code)}
		if codedErr := New(code); codedErr != nil {
			outputs = append(outputs, codedErr.Error())
		}
		for formName, form := range forms {
			for _, output := range outputs {
				if strings.Contains(output, form) {
					t.Errorf("safe output for code %q contains %s credential form", code, formName)
				}
			}
		}
	}
}
