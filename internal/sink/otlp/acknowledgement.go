// SPDX-License-Identifier: AGPL-3.0-only

package otlp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	"google.golang.org/protobuf/encoding/protowire"
)

const maxAcknowledgementBytes = 4 << 20

// OTLP responses use the same wire layout for all three signals: field 1 is a
// PartialSuccess message with rejected_<signal> at field 1 and error_message at 2.
// Hand decoding keeps the synthetic path free of the collector gRPC dependencies.
func otlpAcknowledgement(resp *http.Response, signal string, items int) (pushhook.Acknowledgement, error) {
	a := pushhook.Acknowledgement{Status: pushhook.AckUnknown, Expected: int64(items)}
	var key, snake string
	switch signal {
	case "otlp":
		a.Unit, key, snake = "spans", "rejectedSpans", "rejected_spans"
	case "otlpmetrics":
		a.Unit, key, snake = "data_points", "rejectedDataPoints", "rejected_data_points"
	case "otlplogs":
		a.Unit, key, snake = "log_records", "rejectedLogRecords", "rejected_log_records"
	default:
		return a, operationalerr.New(operationalerr.CodeInternal)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAcknowledgementBytes+1))
	if err != nil || len(body) > maxAcknowledgementBytes {
		return a, operationalerr.New(operationalerr.CodeInternal)
	}
	var rejected int64
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '{' {
		if !utf8.Valid(trimmed) {
			return a, operationalerr.New(operationalerr.CodeInternal)
		}
		rejected, err = jsonRejected(trimmed, key, snake)
	} else {
		rejected, err = protobufRejected(body)
	}
	if err != nil || rejected < 0 || rejected > a.Expected {
		return a, operationalerr.New(operationalerr.CodeInternal)
	}
	if rejected > 0 {
		a.Rejected, a.Accepted = rejected, a.Expected-rejected
		a.Status = pushhook.AckPartial
		if a.Accepted == 0 {
			a.Status = pushhook.AckRejected
		}
		return a, operationalerr.New(operationalerr.CodeRejected)
	}
	// Preserve legacy 2xx transport behavior but do not promote nonstandard
	// status codes to OTLP's specified 200 full-success acknowledgement.
	if resp.StatusCode == http.StatusOK {
		a.Status, a.Accepted = pushhook.AckAccepted, a.Expected
	} else if resp.StatusCode == http.StatusAccepted {
		a.Status = pushhook.AckDeferred
	}
	return a, nil
}

func jsonObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("invalid response object")
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("invalid field")
		}
		if _, dup := fields[key]; dup {
			return nil, fmt.Errorf("duplicate field")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing response data")
	}
	return fields, nil
}

func eitherField(fields map[string]json.RawMessage, a, b string) (json.RawMessage, error) {
	x, xok := fields[a]
	y, yok := fields[b]
	if xok && yok {
		return nil, fmt.Errorf("duplicate field aliases")
	}
	if xok {
		return x, nil
	}
	return y, nil
}

func jsonRejected(body []byte, key, snake string) (int64, error) {
	fields, err := jsonObject(body)
	if err != nil {
		return 0, err
	}
	partial, err := eitherField(fields, "partialSuccess", "partial_success")
	if err != nil {
		return 0, err
	}
	if len(partial) == 0 || bytes.Equal(bytes.TrimSpace(partial), []byte("null")) {
		return 0, nil
	}
	fields, err = jsonObject(partial)
	if err != nil {
		return 0, err
	}
	diagnostic, err := eitherField(fields, "errorMessage", "error_message")
	if err != nil {
		return 0, err
	}
	if len(diagnostic) > 0 && !bytes.Equal(bytes.TrimSpace(diagnostic), []byte("null")) {
		var ignored string
		if err := json.Unmarshal(diagnostic, &ignored); err != nil {
			return 0, fmt.Errorf("invalid diagnostic type")
		}
	}
	raw, err := eitherField(fields, key, snake)
	if err != nil {
		return 0, err
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	var text string
	raw = bytes.TrimSpace(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
	} else {
		text = string(raw)
	}
	// Unknown JSON fields are ignored as required by OTLP/JSON. Diagnostics are
	// neither retained nor logged; 64-bit counts may use canonical JSON strings.
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		return n, nil
	}
	// ProtoJSON also accepts exact integral decimal/exponent forms. Bound the
	// representation before arbitrary-precision parsing to avoid exponent abuse.
	if len(text) > 128 || !json.Valid([]byte(text)) {
		return 0, fmt.Errorf("invalid count")
	}
	if i := bytes.IndexAny([]byte(text), "eE"); i >= 0 {
		e, err := strconv.ParseInt(text[i+1:], 10, 16)
		if err != nil || e < -128 || e > 128 {
			return 0, fmt.Errorf("count exponent outside bound")
		}
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("invalid integral count")
	}
	return r.Num().Int64(), nil
}

func protobufRejected(body []byte) (int64, error) {
	var rejected int64
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return 0, protowire.ParseError(n)
		}
		body = body[n:]
		if num == 1 {
			if typ != protowire.BytesType {
				return 0, fmt.Errorf("invalid partial-success wire type")
			}
			partial, n := protowire.ConsumeBytes(body)
			if n < 0 {
				return 0, protowire.ParseError(n)
			}
			body = body[n:]
			count, seen, err := partialRejected(partial)
			if err != nil {
				return 0, err
			}
			if seen {
				rejected = count
			}
		} else {
			n := skipAckField(num, typ, body)
			if n < 0 {
				return 0, protowire.ParseError(n)
			}
			body = body[n:]
		}
	}
	return rejected, nil
}

func partialRejected(body []byte) (int64, bool, error) {
	var count int64
	seen := false
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return 0, false, protowire.ParseError(n)
		}
		body = body[n:]
		switch num {
		case 1:
			if typ != protowire.VarintType {
				return 0, false, fmt.Errorf("invalid count wire type")
			}
			value, n := protowire.ConsumeVarint(body)
			if n < 0 {
				return 0, false, protowire.ParseError(n)
			}
			body = body[n:]
			count, seen = int64(value), true
		case 2:
			if typ != protowire.BytesType {
				return 0, false, fmt.Errorf("invalid diagnostic wire type")
			}
			value, n := protowire.ConsumeBytes(body)
			if n < 0 {
				return 0, false, protowire.ParseError(n)
			}
			if !utf8.Valid(value) {
				return 0, false, fmt.Errorf("invalid diagnostic encoding")
			}
			body = body[n:]
		default:
			n := skipAckField(num, typ, body)
			if n < 0 {
				return 0, false, protowire.ParseError(n)
			}
			body = body[n:]
		}
	}
	return count, seen, nil
}

func skipAckField(num protowire.Number, typ protowire.Type, body []byte) int {
	// OTLP uses proto3: groups are not a legal extension and recursive group
	// parsing would allow an unnecessary response-controlled stack depth.
	if typ == protowire.StartGroupType || typ == protowire.EndGroupType {
		return -1
	}
	return protowire.ConsumeFieldValue(num, typ, body)
}
