// SPDX-License-Identifier: AGPL-3.0-only

package pyroscope

import pprofpb "github.com/rknightion/synthkit/internal/pyroscope/pprofpb"

// LabelPair is one Pyroscope series label (mirrors types.v1.LabelPair).
type LabelPair struct{ Name, Value string }

// Series is the profile-sink seam: a labelled pprof Profile for one push. The sink gzips + marshals
// Profile into the push.v1 RawSample.raw_profile.
type Series struct {
	Labels  []LabelPair
	Profile *pprofpb.Profile
}
