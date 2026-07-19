package session

import (
	"encoding/json"
	"strings"
)

// ContentKind identifies the type of a content block, using ACP's content
// block vocabulary plus harness-observed extensions.
type ContentKind string

// ContentKind values.
const (
	ContentText     ContentKind = "text"
	ContentImage    ContentKind = "image"
	ContentDocument ContentKind = "document"
	ContentResource ContentKind = "resource"

	// ContentOther marks a block shape the adapter recognized as content
	// but has no first-class mapping for; Raw holds it verbatim.
	ContentOther ContentKind = "other"
)

// ContentBlock is one unit of message or tool-result content.
type ContentBlock struct {
	Kind ContentKind `json:"kind" schema:"acp"`

	// Text is set for text blocks.
	Text string `json:"text,omitempty" schema:"acp"`

	// MimeType describes binary or document content, when known.
	MimeType string `json:"mime_type,omitempty" schema:"acp"`

	// URI locates external or embedded resource content, when known.
	URI string `json:"uri,omitempty" schema:"acp"`

	// Raw preserves the native block verbatim for non-text kinds, so no
	// payload is lost even when only Kind could be mapped.
	Raw json.RawMessage `json:"raw,omitempty" schema:"ext"`

	// Truncated marks a block whose payload exceeded the configured parse
	// limit and was replaced by Size and Digest.
	Truncated bool `json:"truncated,omitempty" schema:"ext"`

	// Size is the byte length of the original payload, set when Truncated.
	Size int64 `json:"size,omitempty" schema:"ext"`

	// Digest is the SHA-256 hex digest of the original payload, set when
	// Truncated.
	Digest string `json:"digest,omitempty" schema:"ext"`
}

// blocksText concatenates the text of all text blocks, separated by
// newlines.
func blocksText(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Kind == ContentText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
