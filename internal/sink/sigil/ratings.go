// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

// Limits copied from the SDK (go/agento11y/rating.go at 75a1c548) and the backend
// (sigil/internal/feedback/service.go at bc963fb09); both validate identically.
const (
	maxRatingConversationIDLen = 255
	maxRatingIDLen             = 128
	maxRatingGenerationIDLen   = 255
	maxRatingActorIDLen        = 255
	maxRatingSourceLen         = 64
	maxRatingCommentBytes      = 4096
	maxRatingMetadataBytes     = 16 * 1024
	maxRatingResponseBytes     = 1 << 20
)

var (
	// ErrRatingValidation reports a rating the client or the server refused as invalid.
	ErrRatingValidation = errors.New("sigil rating: validation failed")
	// ErrRatingConflict reports a rating_id already stored with a different payload (HTTP 409).
	ErrRatingConflict = errors.New("sigil rating: idempotency conflict")
)

// ConversationRating is one stored rating as the server lists it
// (feedback.ConversationRating at bc963fb09).
type ConversationRating struct {
	RatingID       string         `json:"rating_id"`
	ConversationID string         `json:"conversation_id"`
	GenerationID   string         `json:"generation_id,omitempty"`
	Rating         string         `json:"rating"`
	Comment        string         `json:"comment,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RaterID        string         `json:"rater_id,omitempty"`
	Source         string         `json:"source,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

// ConversationRatingPage is one page of GET /api/v1/conversations/{id}/ratings. NextCursor
// is empty on the last page.
type ConversationRatingPage struct {
	Items      []ConversationRating `json:"items"`
	NextCursor string               `json:"next_cursor"`
}

// Ratings writes conversation ratings to POST /api/v1/conversations/{id}/ratings and reads
// them back from GET on the same path. It is the concrete
// nativesigil.ConversationRatingWriter.
//
// A rating is idempotent on rating_id: the server answers a replay of the same payload with
// 200 and a different payload with 409, so a retried POST is safe. A 200 is a rating
// acceptance receipt, not delivery or readback of any generation. The writer does not
// police comment or metadata content; the caller decides what it may send.
type Ratings struct {
	endpoint string
	auth     string
	tenantID string
	hc       *http.Client
	delivery httpretry.Delivery
	policy   httpretry.Policy

	// Observe receives one sanitized event per admitted request (write or read-back).
	// Items is 1 on a verified success. Calls whose context is already done and inputs
	// rejected before any request are silent.
	Observe pushhook.Observer
}

var _ nativesigil.ConversationRatingWriter = (*Ratings)(nil)

// NewRatings returns a rating client for the base URL endpoint, authenticating like the
// ingest sink (HTTP Basic tenantID:token plus X-Scope-OrgID).
func NewRatings(endpoint, tenantID, token string) (*Ratings, error) {
	base, err := normalizeBaseURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("sigil ratings: %w", err)
	}
	return &Ratings{
		endpoint: base,
		auth:     basicAuth(tenantID, token),
		tenantID: tenantID,
		hc:       newNoRedirectClient(10 * time.Second),
		delivery: httpretry.DefaultDelivery(),
		policy:   httpretry.EmitOncePolicy(),
	}, nil
}

// SetDelivery installs the HA delivery policy before use.
func (r *Ratings) SetDelivery(d httpretry.Delivery) { r.delivery = d; d.ConfigureClient(r.hc) }

// WriteConversationRating validates input exactly as the SDK does, then posts it.
func (r *Ratings) WriteConversationRating(ctx context.Context, conversationID string, input nativesigil.ConversationRatingInput) (terminalErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := strings.TrimSpace(conversationID)
	normalized, err := normalizeRatingInput(id, input)
	if err != nil {
		return err
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("%w: metadata must be valid JSON", ErrRatingValidation)
	}

	start := time.Now()
	status, verified := 0, false
	defer func() {
		r.observe(ctx, verified, status, time.Since(start), terminalErr)
	}()

	status, response, err := r.do(ctx, http.MethodPost, ratingsURL(r.endpoint, id), body)
	switch {
	case err == nil:
	case status == http.StatusConflict:
		return ErrRatingConflict
	case status == http.StatusBadRequest:
		return fmt.Errorf("%w: server rejected the rating", ErrRatingValidation)
	default:
		return httpretry.Preserve(err, operationalerr.New(operationalerr.Classify(status, err)))
	}

	var out struct {
		Rating ConversationRating `json:"rating"`
	}
	if err := json.Unmarshal(response, &out); err != nil ||
		out.Rating.RatingID != normalized.RatingID || out.Rating.ConversationID != id || out.Rating.Rating != string(normalized.Rating) {
		return operationalerr.New(operationalerr.CodeRejected)
	}
	verified = true
	return nil
}

// ListConversationRatings reads one page back from GET /api/v1/conversations/{id}/ratings.
// limit <= 0 sends none (the server default); cursor is the opaque value of a previous page.
func (r *Ratings) ListConversationRatings(ctx context.Context, conversationID string, limit int, cursor string) (page ConversationRatingPage, terminalErr error) {
	if err := ctx.Err(); err != nil {
		return ConversationRatingPage{}, err
	}
	id := strings.TrimSpace(conversationID)
	if id == "" || len(id) > maxRatingConversationIDLen {
		return ConversationRatingPage{}, fmt.Errorf("%w: conversation id must be 1..%d bytes", ErrRatingValidation, maxRatingConversationIDLen)
	}
	target := ratingsURL(r.endpoint, id)
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	start := time.Now()
	status, verified := 0, false
	defer func() {
		r.observe(ctx, verified, status, time.Since(start), terminalErr)
	}()

	status, response, err := r.do(ctx, http.MethodGet, target, nil)
	if err != nil {
		if status == http.StatusBadRequest {
			return ConversationRatingPage{}, fmt.Errorf("%w: server rejected the query", ErrRatingValidation)
		}
		return ConversationRatingPage{}, httpretry.Preserve(err, operationalerr.New(operationalerr.Classify(status, err)))
	}
	if err := json.Unmarshal(response, &page); err != nil {
		return ConversationRatingPage{}, operationalerr.New(operationalerr.CodeRejected)
	}
	verified = true
	return page, nil
}

// do sends one request through the HA delivery gate with the idempotent retry policy and
// returns the final status and body. A body is returned only for a 2xx status.
func (r *Ratings) do(ctx context.Context, method, target string, body []byte) (int, []byte, error) {
	var (
		lastStatus int
		lastBody   []byte
	)
	err := r.delivery.Policy(r.policy).Do(ctx, func(ctx context.Context) (int, error) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return 0, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", r.auth)
		if r.tenantID != "" {
			req.Header.Set("X-Scope-OrgID", r.tenantID)
		}
		return r.delivery.Send(ctx, r.hc, req, func(resp *http.Response) (int, error) {
			lastStatus = resp.StatusCode
			data, err := io.ReadAll(io.LimitReader(resp.Body, maxRatingResponseBytes+1))
			if err != nil {
				return resp.StatusCode, err
			}
			if len(data) > maxRatingResponseBytes {
				return resp.StatusCode, errors.New("rating response too large")
			}
			if resp.StatusCode != http.StatusOK {
				return resp.StatusCode, fmt.Errorf("rating request returned HTTP %d", resp.StatusCode)
			}
			lastBody = data
			return resp.StatusCode, nil
		})
	})
	return lastStatus, lastBody, err
}

func (r *Ratings) observe(ctx context.Context, verified bool, status int, duration time.Duration, err error) {
	if r.Observe == nil {
		return
	}
	items := 0
	if verified {
		items = 1
	}
	r.Observe(ctx, pushhook.Event{
		Sink: ObserveSinkRatings, Items: items, Status: status, Duration: duration,
		ErrorCode: operationalerr.Classify(status, err),
	})
}

func ratingsURL(base, conversationID string) string {
	return base + "/api/v1/conversations/" + url.PathEscape(conversationID) + "/ratings"
}

// normalizeRatingInput trims and bounds a rating the way the SDK's
// normalizeConversationRatingInput does, so nothing the server would reject is sent.
func normalizeRatingInput(conversationID string, in nativesigil.ConversationRatingInput) (nativesigil.ConversationRatingInput, error) {
	invalid := func(msg string) error { return fmt.Errorf("%w: %s", ErrRatingValidation, msg) }
	out := nativesigil.ConversationRatingInput{
		RatingID:     strings.TrimSpace(in.RatingID),
		Rating:       nativesigil.ConversationRatingValue(strings.TrimSpace(string(in.Rating))),
		Comment:      strings.TrimSpace(in.Comment),
		Metadata:     in.Metadata,
		GenerationID: strings.TrimSpace(in.GenerationID),
		RaterID:      strings.TrimSpace(in.RaterID),
		Source:       strings.TrimSpace(in.Source),
	}
	switch {
	case conversationID == "":
		return out, invalid("conversation id is required")
	case len(conversationID) > maxRatingConversationIDLen:
		return out, invalid("conversation id is too long")
	case out.RatingID == "":
		return out, invalid("rating id is required")
	case len(out.RatingID) > maxRatingIDLen:
		return out, invalid("rating id is too long")
	case out.Rating != nativesigil.ConversationRatingValueGood && out.Rating != nativesigil.ConversationRatingValueBad:
		return out, invalid("rating must be CONVERSATION_RATING_VALUE_GOOD or CONVERSATION_RATING_VALUE_BAD")
	case len(out.Comment) > maxRatingCommentBytes:
		return out, invalid("comment is too long")
	case len(out.GenerationID) > maxRatingGenerationIDLen:
		return out, invalid("generation id is too long")
	case len(out.RaterID) > maxRatingActorIDLen:
		return out, invalid("rater id is too long")
	case len(out.Source) > maxRatingSourceLen:
		return out, invalid("source is too long")
	}
	if out.Metadata != nil {
		encoded, err := json.Marshal(out.Metadata)
		if err != nil {
			return out, invalid("metadata must be valid JSON")
		}
		if len(encoded) > maxRatingMetadataBytes {
			return out, invalid("metadata is too large")
		}
	}
	return out, nil
}
