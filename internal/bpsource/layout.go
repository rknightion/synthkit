// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import "strings"

const (
	customDir    = "custom"
	gitDir       = "git"
	manifestFile = ".boot-manifest.json"
	sep          = "__"
)

func uploadFilename(ns, name string) string {
	return SanitizeNS(ns) + sep + name + ".yaml"
}

func parseUploadFilename(fn string) (ns, name string, ok bool) {
	if !strings.HasSuffix(fn, ".yaml") {
		return "", "", false
	}
	base := strings.TrimSuffix(fn, ".yaml")
	i := strings.Index(base, sep)
	if i < 0 {
		return "", "", false
	}
	return base[:i], base[i+len(sep):], true
}
