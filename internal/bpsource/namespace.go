// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"regexp"
	"strings"
)

var (
	nsClean     = regexp.MustCompile(`[^a-z0-9_-]+`)
	nsSeparator = regexp.MustCompile(`__+`)
)

// SanitizeNS lowercases raw and replaces any run of non [a-z0-9_-] with a single "-",
// normalises the upload filename separator "__" to "-", and trims leading/trailing "-".
// Empty result defaults to "custom".
func SanitizeNS(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = nsClean.ReplaceAllString(s, "-")
	s = nsSeparator.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "custom"
	}
	return s
}

// Namespace composes the final blueprint name "<sanitized-ns>/<name>".
func Namespace(ns, name string) string {
	return SanitizeNS(ns) + "/" + name
}
