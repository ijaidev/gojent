package llm

import "errors"

var (
	// ErrUnsupportedPart is returned when an LLM provider does not support a given message part.
	ErrUnsupportedPart = errors.New("llm: unsupported part")
	// ErrUnsupportedPartType is returned when a provider does not support the part type (e.g. video, audio).
	ErrUnsupportedPartType = errors.New("llm: unsupported part type")
	// ErrUnsupportedMIMEType is returned when a provider does not support the MIME type of a part.
	ErrUnsupportedMIMEType = errors.New("llm: unsupported mime type")
	// ErrInvalidPart is returned when a part is missing required data, URI, or text.
	ErrInvalidPart = errors.New("llm: invalid part")
)
