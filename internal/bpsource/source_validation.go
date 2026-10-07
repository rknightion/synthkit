// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	sourceSlug    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	envVarName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	hostLabel     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	invalidRefRun = regexp.MustCompile(`[ ~^:?*\\[\\]\\\\]`)
)

// ValidateSource rejects source configuration that could not identify a stable, safe HTTPS
// source. It deliberately validates rather than sanitises: changing a source ID or namespace
// changes the staging path and runtime identities, so silently rewriting either is surprising.
func ValidateSource(s Source) error {
	return (SourcePolicy{}).ValidateSource(s)
}

// SourcePolicy is an immutable, process-wide policy supplied by the composition root.
// Its zero value permits any HTTPS host, but still restricts credential variable names.
type SourcePolicy struct {
	hosts map[string]struct{}
}

// NewSourcePolicy parses a comma-separated list of exact hostnames or IP addresses.
// Empty disables host restrictions. Ports, URLs, wildcards and empty entries are rejected.
func NewSourcePolicy(raw string) (SourcePolicy, error) {
	p := SourcePolicy{}
	if strings.TrimSpace(raw) == "" {
		return p, nil
	}
	p.hosts = make(map[string]struct{})
	for _, entry := range strings.Split(raw, ",") {
		host := strings.ToLower(strings.TrimSpace(entry))
		valid := net.ParseIP(host) != nil
		if !valid && len(host) <= 253 {
			valid = true
			for _, label := range strings.Split(host, ".") {
				if len(label) > 63 || !hostLabel.MatchString(label) {
					valid = false
					break
				}
			}
		}
		if !valid {
			return SourcePolicy{}, fmt.Errorf("GIT_SOURCE_HOST_ALLOWLIST must contain exact hostnames or IP addresses, without ports, URLs, wildcards or empty entries")
		}
		p.hosts[host] = struct{}{}
	}
	return p, nil
}

func validateTokenEnvVar(name string) error {
	if name != "" && !envVarName.MatchString(name) {
		return fmt.Errorf("token env var must be an environment-variable name")
	}
	if name != "" && name != "GIT_TOKEN" && (!strings.HasPrefix(name, "GIT_TOKEN_") || name == "GIT_TOKEN_") {
		return fmt.Errorf("token env var must be GIT_TOKEN or start with GIT_TOKEN_ followed by a non-empty suffix; empty uses the GIT_TOKEN default")
	}
	return nil
}

func (p SourcePolicy) validateURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("source url must be an HTTPS URL without embedded credentials or a fragment")
	}
	if len(p.hosts) != 0 {
		if _, ok := p.hosts[strings.ToLower(u.Hostname())]; !ok {
			return fmt.Errorf("source host is not permitted by GIT_SOURCE_HOST_ALLOWLIST")
		}
	}
	return nil
}

// ValidateSource rejects unsafe source configuration before it is persisted.
func (p SourcePolicy) ValidateSource(s Source) error {
	if !sourceSlug.MatchString(s.ID) || strings.Contains(s.ID, "__") {
		return fmt.Errorf("source id must be a stable lowercase slug (letters, numbers, '_' and '-')")
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("source name is required")
	}
	if !sourceSlug.MatchString(s.Namespace) || strings.Contains(s.Namespace, "__") {
		return fmt.Errorf("source namespace must be a lowercase slug (letters, numbers, '_' and '-')")
	}
	if err := p.validateURL(s.URL); err != nil {
		return err
	}
	if !validRef(s.Ref) {
		return fmt.Errorf("source ref is required and must be a valid git ref")
	}
	if s.Subpath != "" && (!path.IsAbs(s.Subpath) && path.Clean(s.Subpath) == s.Subpath && !strings.HasPrefix(s.Subpath, "../") && s.Subpath != "." && s.Subpath != "..") {
		// A normal relative subpath is accepted below; this branch only avoids the less-helpful
		// fallthrough for clean paths.
	} else if s.Subpath != "" {
		return fmt.Errorf("source subpath must be a clean relative path")
	}
	return validateTokenEnvVar(s.TokenEnvVar)
}

func validRef(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") ||
		strings.Contains(ref, "//") || strings.Contains(ref, "@{") || invalidRefRun.MatchString(ref) {
		return false
	}
	return true
}
