// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import "testing"

func TestUploadFilenameRoundTrip(t *testing.T) {
	fn := uploadFilename("team-a", "fleet")
	if fn != "team-a__fleet.yaml" {
		t.Fatalf("uploadFilename=%q", fn)
	}
	ns, name, ok := parseUploadFilename(fn)
	if !ok || ns != "team-a" || name != "fleet" {
		t.Fatalf("parse=%q,%q,%v", ns, name, ok)
	}
}

func TestParseUploadFilenameRejects(t *testing.T) {
	if _, _, ok := parseUploadFilename("noseparator.yaml"); ok {
		t.Fatal("expected reject for missing __ separator")
	}
	if _, _, ok := parseUploadFilename("a__b.txt"); ok {
		t.Fatal("expected reject for non-yaml")
	}
}
