// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	sourceSlug    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	envVarName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	invalidRefRun = regexp.MustCompile(`[ ~^:?*\\[\\]\\\\]`)
)

// ValidateSource rejects source configuration that could not identify a stable, safe HTTPS
// source. It deliberately validates rather than sanitises: changing a source ID or namespace
// changes the staging path and runtime identities, so silently rewriting either is surprising.
func ValidateSource(s Source) error {
	if !sourceSlug.MatchString(s.ID) || strings.Contains(s.ID, "__") {
		return fmt.Errorf("source id must be a stable lowercase slug (letters, numbers, '_' and '-')")
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("source name is required")
	}
	if !sourceSlug.MatchString(s.Namespace) || strings.Contains(s.Namespace, "__") {
		return fmt.Errorf("source namespace must be a lowercase slug (letters, numbers, '_' and '-')")
	}
	u, err := url.ParseRequestURI(s.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("source url must be an HTTPS URL without embedded credentials or a fragment")
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
	if s.TokenEnvVar != "" && !envVarName.MatchString(s.TokenEnvVar) {
		return fmt.Errorf("token env var must be an environment-variable name")
	}
	return nil
}

func validRef(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") ||
		strings.Contains(ref, "//") || strings.Contains(ref, "@{") || invalidRefRun.MatchString(ref) {
		return false
	}
	return true
}
