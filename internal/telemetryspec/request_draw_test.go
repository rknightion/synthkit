// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"crypto/sha256"
	"encoding/binary"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func drawModel(d RequestDraw) ValueModel { return ValueModel{RequestDraw: &d} }

// counterDraw is a deterministic purpose -> [0,1) test draw: a SHA-256 of the salt and purpose.
func counterDraw(salt int) func(string) float64 {
	return func(purpose string) float64 {
		sum := sha256.Sum256([]byte(strconv.Itoa(salt) + "\x00" + purpose))
		return float64(binary.BigEndian.Uint64(sum[:8])>>11) / float64(uint64(1)<<53)
	}
}

func TestRequestDrawFormatRendersDeclaredShape(t *testing.T) {
	v := drawModel(RequestDraw{Key: "k", Format: "ORD-{digits:10}-{hex:4}-{upper:6}"})
	if err := v.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if v.Kind() != KindRequestDraw {
		t.Fatalf("Kind = %q", v.Kind())
	}
	shape := regexp.MustCompile(`^ORD-[0-9]{10}-[0-9a-f]{4}-[A-Z0-9]{6}$`)
	seen := map[string]bool{}
	for salt := range 200 {
		n, got := v.Eval(EvalCtx{Draw: counterDraw(salt)})
		if n != 0 || !shape.MatchString(got) {
			t.Fatalf("Eval = (%v, %q), want ORD-<10 digits>-<4 hex>-<6 upper>", n, got)
		}
		seen[got] = true
	}
	if len(seen) < 199 {
		t.Fatalf("only %d distinct IDs in 200 draws", len(seen))
	}
}

func TestRequestDrawIsAFunctionOfTheDrawAndKey(t *testing.T) {
	a := drawModel(RequestDraw{Key: "a", Format: "{digits:12}"})
	b := drawModel(RequestDraw{Key: "b", Format: "{digits:12}"})
	_, a1 := a.Eval(EvalCtx{Draw: counterDraw(1)})
	_, a2 := a.Eval(EvalCtx{Draw: counterDraw(1)})
	_, b1 := b.Eval(EvalCtx{Draw: counterDraw(1)})
	_, a3 := a.Eval(EvalCtx{Draw: counterDraw(2)})
	if a1 != a2 {
		t.Fatalf("same draw, same key: %q vs %q", a1, a2)
	}
	if a1 == b1 {
		t.Fatalf("different keys drew the same value %q", a1)
	}
	if a1 == a3 {
		t.Fatalf("different draws gave the same value %q", a1)
	}
	// A line that repeats the declaration (same key and format) reads the same value whatever
	// its position: nothing else feeds the draw.
	same := drawModel(RequestDraw{Key: "a", Format: "{digits:12}"})
	if _, got := same.Eval(EvalCtx{Draw: counterDraw(1), Rand: func() float64 { return 0.99 }}); got != a1 {
		t.Fatalf("engine Rand changed the draw: %q vs %q", got, a1)
	}
}

func TestRequestDrawEnumFollowsWeights(t *testing.T) {
	v := drawModel(RequestDraw{Key: "path", Enum: []EnumEntry{
		{Value: "/a", Weight: 6}, {Value: "/b", Weight: 3}, {Value: "/c", Weight: 1},
	}})
	if err := v.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	counts := map[string]int{}
	const n = 20000
	for salt := range n {
		_, got := v.Eval(EvalCtx{Draw: counterDraw(salt)})
		counts[got]++
	}
	for value, want := range map[string]float64{"/a": 0.6, "/b": 0.3, "/c": 0.1} {
		if got := float64(counts[value]) / n; got < want-0.02 || got > want+0.02 {
			t.Errorf("%s share = %.3f, want %.2f", value, got, want)
		}
	}
}

func TestRequestDrawWithoutADrawIsStable(t *testing.T) {
	v := drawModel(RequestDraw{Key: "k", Format: "ID-{digits:6}"})
	_, a := v.Eval(EvalCtx{})
	_, b := v.Eval(EvalCtx{})
	if a != b || !strings.HasPrefix(a, "ID-") || len(a) != 9 {
		t.Fatalf("nil Draw: %q, %q", a, b)
	}
}

func TestRequestDrawValidation(t *testing.T) {
	scale := 2.0
	for name, tc := range map[string]struct {
		d    RequestDraw
		want string
	}{
		"blank key":        {RequestDraw{Key: " ", Format: "{digits:4}"}, "key"},
		"neither":          {RequestDraw{Key: "k"}, "format or an enum"},
		"both":             {RequestDraw{Key: "k", Format: "{digits:4}", Enum: []EnumEntry{{Value: "x", Weight: 1}}}, "not both"},
		"constant format":  {RequestDraw{Key: "k", Format: "ORD-1"}, "no generated run"},
		"unclosed":         {RequestDraw{Key: "k", Format: "ORD-{digits:4"}, "unclosed"},
		"stray brace":      {RequestDraw{Key: "k", Format: "ORD}-{digits:4}"}, "unmatched"},
		"unknown run":      {RequestDraw{Key: "k", Format: "{base64:4}"}, "bad run"},
		"zero count":       {RequestDraw{Key: "k", Format: "{digits:0}"}, "bad run"},
		"count too large":  {RequestDraw{Key: "k", Format: "{digits:33}"}, "bad run"},
		"no count":         {RequestDraw{Key: "k", Format: "{digits}"}, "bad run"},
		"too many chars":   {RequestDraw{Key: "k", Format: "{hex:32}{hex:32}{hex:1}"}, "exceeds"},
		"empty enum value": {RequestDraw{Key: "k", Enum: []EnumEntry{{Value: "", Weight: 1}}}, "empty value"},
		"duplicate":        {RequestDraw{Key: "k", Enum: []EnumEntry{{Value: "a"}, {Value: "a"}}}, "duplicate"},
		"negative weight":  {RequestDraw{Key: "k", Enum: []EnumEntry{{Value: "a", Weight: -1}}}, "negative weight"},
		"metric scale":     {RequestDraw{Key: "k", Enum: []EnumEntry{{Value: "a", Weight: 1, RateScale: &scale}}}, "metric labels only"},
	} {
		err := drawModel(tc.d).Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", name, tc.want, err)
		}
	}
	// The format boundary: 64 generated characters is the most a format may draw.
	if err := drawModel(RequestDraw{Key: "k", Format: "{hex:32}{hex:32}"}).Validate(); err != nil {
		t.Errorf("64 generated characters: %v", err)
	}
}

func TestRequestDrawIsExactlyOneOf(t *testing.T) {
	c := 1.0
	v := ValueModel{Const: &c, RequestDraw: &RequestDraw{Key: "k", Format: "{digits:4}"}}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "need exactly one") {
		t.Fatalf("const plus request_draw must be rejected, got %v", err)
	}
}

// request_draw is a log body field only: a label must enumerate a stable domain, a metric value is
// numeric, and no other evaluator supplies the per-request seed.
func TestRequestDrawCapability(t *testing.T) {
	v := drawModel(RequestDraw{Key: "k", Format: "{digits:4}"})
	if err := checkValue(v, posBody, "w"); err != nil {
		t.Fatalf("log body: %v", err)
	}
	for _, pos := range []position{posLabel, posMetricLabel, posMetricValue, posAttr} {
		err := checkValue(v, pos, "w")
		if err == nil {
			t.Fatalf("position %d must reject request_draw", pos)
		}
	}
	l := LogSpec{Source: "s", Body: map[string]ValueModel{"id": v}}
	if err := l.Validate(); err != nil {
		t.Fatalf("log spec: %v", err)
	}
	l.StreamLabels = map[string]ValueModel{"id": v}
	if err := l.Validate(); err == nil || !strings.Contains(err.Error(), strconv.Quote("s")) {
		t.Fatalf("stream label must be rejected naming the log, got %v", err)
	}
}
