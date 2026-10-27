package app

import (
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ijaidev/gojent/core/llm"
)

const (
	maxMessageContentBytes  = 1 << 20
	maxImageAttachments     = 4
	maxImageBytes           = 5 << 20
	maxMessageImageBytes    = 8 << 20
	maxThreadImageBytes     = 12 << 20
	maxAttachmentNameLength = 120
)

// AttachmentInput is one image uploaded with a message.
type AttachmentInput struct {
	Filename string `json:"filename,omitempty"`
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// buildImageParts validates attachments and converts them into canonical file
// parts. It returns the total number of image bytes so the caller can enforce
// the per-thread budget.
func buildImageParts(inputs []AttachmentInput) ([]llm.Part, int, error) {
	if len(inputs) == 0 {
		return nil, 0, nil
	}
	if len(inputs) > maxImageAttachments {
		return nil, 0, fmt.Errorf(
			"%w: at most %d images are allowed per message",
			ErrInvalidInput,
			maxImageAttachments,
		)
	}

	parts := make([]llm.Part, 0, len(inputs))
	total := 0
	for index, input := range inputs {
		if len(input.Data) == 0 {
			return nil, 0, fmt.Errorf("%w: image %d is empty", ErrInvalidInput, index+1)
		}
		if len(input.Data) > maxImageBytes {
			return nil, 0, fmt.Errorf(
				"%w: image %d exceeds the %d MiB limit",
				ErrInvalidInput,
				index+1,
				maxImageBytes>>20,
			)
		}

		mimeType, err := validateImageMIME(input.MIMEType, input.Data)
		if err != nil {
			return nil, 0, fmt.Errorf("image %d: %w", index+1, err)
		}
		if total += len(input.Data); total > maxMessageImageBytes {
			return nil, 0, fmt.Errorf(
				"%w: images exceed the %d MiB message limit",
				ErrInvalidInput,
				maxMessageImageBytes>>20,
			)
		}

		part := llm.FileFromBytes(input.Data, mimeType)
		part.Filename = attachmentFilename(input.Filename, mimeType)
		parts = append(parts, part)
	}
	return parts, total, nil
}

// validateImageMIME verifies the declared MIME type against the file signature.
// v1 accepts the intersection supported by every provider adapter.
func validateImageMIME(declared string, data []byte) (string, error) {
	declared = normalizeMIME(declared)
	if declared == "" {
		return "", fmt.Errorf("%w: image MIME type is required", ErrInvalidInput)
	}

	detected := normalizeMIME(http.DetectContentType(data))
	switch detected {
	case "image/png", "image/jpeg", "image/webp":
	case "image/gif", "image/heic", "image/heif":
		return "", fmt.Errorf(
			"%w: %s images are not supported; use PNG, JPEG, or WebP",
			ErrInvalidInput,
			detected,
		)
	default:
		return "", fmt.Errorf(
			"%w: unsupported image type %q; use PNG, JPEG, or WebP",
			ErrInvalidInput,
			detected,
		)
	}

	if declared != detected {
		return "", fmt.Errorf(
			"%w: declared type %q does not match content type %q",
			ErrInvalidInput,
			declared,
			detected,
		)
	}
	return detected, nil
}

func normalizeMIME(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if mediaType, _, err := mime.ParseMediaType(value); err == nil {
		value = mediaType
	}
	value = strings.ToLower(value)
	if value == "image/jpg" {
		return "image/jpeg"
	}
	return value
}

func attachmentFilename(name, mimeType string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return "image" + imageExtension(mimeType)
	}
	if utf8.RuneCountInString(name) > maxAttachmentNameLength {
		runes := []rune(name)
		name = strings.TrimSpace(string(runes[:maxAttachmentNameLength]))
	}
	return name
}

func imageExtension(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

// messageTitle derives a thread title from text or the first image.
func messageTitle(content string, images []llm.Part) string {
	if content != "" {
		return content
	}
	if len(images) == 1 {
		if file, ok := images[0].(*llm.FilePart); ok && file.Filename != "" {
			return file.Filename
		}
		return "1 image"
	}
	return fmt.Sprintf("%d images", len(images))
}

// imageBytesInMessages sums inline file payloads already in the transcript.
func imageBytesInMessages(messages []*Message) int {
	total := 0
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, part := range message.Payload.Content {
			if file, ok := part.(*llm.FilePart); ok {
				total += len(file.Data)
			}
		}
	}
	return total
}
