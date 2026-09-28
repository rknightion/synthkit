// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func compareAppParityFixture(t *testing.T, key string, got appParityWire) {
	t.Helper()
	path := filepath.Join("testdata", "app-parity-"+strings.ReplaceAll(key, "/", "__")+".json.gz")
	if *appParityUpdate {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		writer := gzip.NewWriter(file)
		encoded, err := json.Marshal(got)
		if err == nil {
			_, err = writer.Write(encoded)
		}
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("app parity fixture %s: %v", path, err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("app parity fixture %s: %v", path, err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read app parity fixture %s: %v", path, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	var want appParityWire
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("decode app parity fixture %s: %v", path, err)
	}
	for _, part := range []struct {
		name             string
		actual, expected []byte
	}{
		{"metrics", got.Metrics, want.Metrics}, {"logs", got.Logs, want.Logs},
	} {
		actual := decodeParityJSON(t, part.actual)
		expected := decodeParityJSON(t, part.expected)
		if err := compareParityJSON(actual, expected, part.name); err != nil {
			t.Errorf("app parity %s %s: %v", key, part.name, err)
		}
	}
}

func decodeParityJSON(t *testing.T, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("trailing parity JSON content")
	}
	return result
}

// All non-floating fields are exact. Only series/exemplar Value, native-histogram
// Sum/ZeroThreshold, and decimal JSON numbers inside log bodies admit rounding.
func compareParityJSON(actual, want any, path string) error {
	if a, ok := actual.(json.Number); ok {
		b, ok := want.(json.Number)
		if !ok {
			return fmt.Errorf("%s: number %s changed type (%T)", path, a, want)
		}
		floatField := strings.HasSuffix(path, ".Value") || strings.HasSuffix(path, ".Sum") || strings.HasSuffix(path, ".ZeroThreshold") ||
			(strings.Contains(path, ".Body{json}") && (strings.ContainsAny(string(a), ".eE") || strings.ContainsAny(string(b), ".eE")))
		if !floatField {
			if a != b {
				return fmt.Errorf("%s: integer %s != %s", path, a, b)
			}
			return nil
		}
		af, ae := strconv.ParseFloat(string(a), 64)
		bf, be := strconv.ParseFloat(string(b), 64)
		if ae != nil || be != nil || math.IsInf(af, 0) || math.IsInf(bf, 0) {
			return fmt.Errorf("%s: invalid float %s / %s", path, a, b)
		}
		diff := math.Abs(af - bf)
		mag := math.Max(math.Abs(af), math.Abs(bf))
		if (mag < 1e-3 && diff > 1e-12) || (mag >= 1e-3 && diff/mag > 1e-9) {
			return fmt.Errorf("%s: float %s != %s (absolute %g, relative %g)", path, a, b, diff, diff/mag)
		}
		return nil
	}
	if a, ok := actual.(map[string]any); ok {
		b, ok := want.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: object changed type (%T)", path, want)
		}
		if len(a) != len(b) {
			return fmt.Errorf("%s: object keys changed: %d != %d", path, len(a), len(b))
		}
		keys := make([]string, 0, len(a))
		for key := range a {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, exists := b[key]
			if !exists {
				return fmt.Errorf("%s: missing key %q", path, key)
			}
			if err := compareParityJSON(a[key], value, path+"."+key); err != nil {
				return err
			}
		}
		return nil
	}
	if a, ok := actual.([]any); ok {
		b, ok := want.([]any)
		if !ok {
			return fmt.Errorf("%s: array changed type (%T)", path, want)
		}
		if len(a) != len(b) {
			return fmt.Errorf("%s: array length %d != %d", path, len(a), len(b))
		}
		for i := range a {
			if err := compareParityJSON(a[i], b[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if a, ok := actual.(string); ok && strings.HasSuffix(path, ".Body") {
		b, ok := want.(string)
		if !ok {
			return fmt.Errorf("%s: log body changed type", path)
		}
		var av, bv any
		ad, bd := json.NewDecoder(strings.NewReader(a)), json.NewDecoder(strings.NewReader(b))
		ad.UseNumber()
		bd.UseNumber()
		if ad.Decode(&av) == nil && ad.Decode(new(any)) == io.EOF && bd.Decode(&bv) == nil && bd.Decode(new(any)) == io.EOF {
			return compareParityJSON(av, bv, path+"{json}")
		}
	}
	if !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("%s: %q != %q", path, actual, want)
	}
	return nil
}
