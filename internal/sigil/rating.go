// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import "context"

// ConversationRatingValue mirrors the SDK rating enum.
// Source: agento11y Go SDK rating.go at 75a1c548553066364cdfe24fe5b1f98a57378b2c.
type ConversationRatingValue string

const (
	ConversationRatingValueGood ConversationRatingValue = "CONVERSATION_RATING_VALUE_GOOD"
	ConversationRatingValueBad  ConversationRatingValue = "CONVERSATION_RATING_VALUE_BAD"
)

// ConversationRatingInput mirrors the SDK request body. Conversation identity is
// a writer argument because it belongs in the URL, not this payload.
type ConversationRatingInput struct {
	RatingID     string                  `json:"rating_id"`
	Rating       ConversationRatingValue `json:"rating"`
	Comment      string                  `json:"comment,omitempty"`
	Metadata     map[string]any          `json:"metadata,omitempty"`
	GenerationID string                  `json:"generation_id,omitempty"`
	RaterID      string                  `json:"rater_id,omitempty"`
	Source       string                  `json:"source,omitempty"`
}

// ConversationRatingWriter is the rating acceptance seam, not a generation
// delivery/readback or durable custody guarantee. Concrete adapters own validation,
// authentication and bounded HTTP; this package installs no runtime writer.
type ConversationRatingWriter interface {
	// WriteConversationRating writes input for conversationID (the string argument).
	WriteConversationRating(context.Context, string, ConversationRatingInput) error
}
