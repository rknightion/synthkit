// SPDX-License-Identifier: AGPL-3.0-only

package pyroscope

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	pprofpb "github.com/rknightion/synthkit/internal/pyroscope/pprofpb"

	"google.golang.org/protobuf/proto"
)

const t0ns = int64(1_750_000_000_000_000_000)

func testProfile(ts int64) *pprofpb.Profile {
	return &pprofpb.Profile{
		StringTable: []string{"", "cpu", "nanoseconds"},
		SampleType:  []*pprofpb.ValueType{{Type: 1, Unit: 2}},
		TimeNanos:   ts,
		Sample:      []*pprofpb.Sample{{Value: []int64{1}}},
	}
}

func TestSinkDryRunInventory(t *testing.T) {
	s := New("", "", "", true)
	_ = s.Write(context.Background(), []Series{{
		Labels: []LabelPair{
			{Name: "__name__", Value: "process_cpu"},
			{Name: "__profile_type__", Value: "process_cpu:cpu:nanoseconds:cpu:nanoseconds"},
			{Name: "service_name", Value: "acme"},
		},
		Profile: testProfile(t0ns),
	}})
	got := s.Inventory()["process_cpu:cpu:nanoseconds:cpu:nanoseconds"]
	want := []string{"__name__", "__profile_type__", "service_name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inventory=%v want %v", got, want)
	}
}

func TestEncodeAndID(t *testing.T) {
	mk := func(ts int64) []Series {
		return []Series{{Labels: []LabelPair{{Name: "service_name", Value: "acme"}}, Profile: testProfile(ts)}}
	}
	req := encodePush(mk(t0ns))
	if len(req.Series) != 1 || len(req.Series[0].Samples) != 1 {
		t.Fatal("shape")
	}
	id1 := req.Series[0].Samples[0].ID
	if id1 == "" {
		t.Fatal("empty ID")
	}
	// inner raw_profile is gzipped pprof:
	gz, err := gzip.NewReader(bytes.NewReader(req.Series[0].Samples[0].RawProfile))
	if err != nil {
		t.Fatal(err)
	}
	dec, _ := io.ReadAll(gz)
	if err := proto.Unmarshal(dec, &pprofpb.Profile{}); err != nil {
		t.Fatal("raw_profile not gzipped pprof")
	}
	// deterministic for fixed (labels, ts):
	if encodePush(mk(t0ns)).Series[0].Samples[0].ID != id1 {
		t.Fatal("ID must be stable for fixed labels+ts")
	}
	// differs across ts:
	if encodePush(mk(t0ns + 60_000_000_000)).Series[0].Samples[0].ID == id1 {
		t.Fatal("ID must differ across ticks")
	}
}

func TestNoContentEncodingHeader(t *testing.T) {
	var gotCE, gotCT, gotCPV string
	var gotXScope bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCE = r.Header.Get("Content-Encoding")
		gotCT = r.Header.Get("Content-Type")
		gotCPV = r.Header.Get("Connect-Protocol-Version")
		_, gotXScope = r.Header["X-Scope-Orgid"]
		w.WriteHeader(200)
	}))
	defer srv.Close()
	s := New(srv.URL, "u", "p", false)
	if err := s.Write(context.Background(), []Series{{
		Labels:  []LabelPair{{Name: "service_name", Value: "a"}},
		Profile: testProfile(t0ns),
	}}); err != nil {
		t.Fatal(err)
	}
	if gotCE != "" {
		t.Fatalf("must NOT set request Content-Encoding, got %q", gotCE)
	}
	if gotCT != "application/proto" {
		t.Fatalf("Content-Type=%q", gotCT)
	}
	if gotCPV != "1" {
		t.Fatalf("Connect-Protocol-Version=%q", gotCPV)
	}
	if gotXScope {
		t.Fatal("must NOT set X-Scope-OrgID")
	}
}
