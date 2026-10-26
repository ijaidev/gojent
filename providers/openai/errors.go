package openai

import (
	"context"
	"errors"
	"fmt"

	"github.com/ijaidev/gojent/core/llm"
	sdk "github.com/openai/openai-go"
)

var (
	// ErrModelNotSet is returned when Config.Model is empty.
	ErrModelNotSet = errors.New("openai: model is not set")
)

// wrapError maps SDK and transport failures onto the canonical error taxonomy.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) && apiErr != nil {
		return &llm.Error{
			Kind:       classifyStatus(apiErr.StatusCode),
			StatusCode: apiErr.StatusCode,
			Err:        err,
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &llm.Error{Kind: llm.KindTimeout, Err: err}
	case errors.Is(err, context.Canceled):
		return &llm.Error{Kind: llm.KindCanceled, Err: err}
	default:
		return err
	}
}

// classifyStatus maps HTTP status codes onto the canonical taxonomy.
func classifyStatus(status int) llm.Kind {
	switch status {
	case 400, 404, 409, 422:
		return llm.KindInvalid
	case 401, 403:
		return llm.KindAuth
	case 413:
		return llm.KindContextOverflow
	case 429:
		return llm.KindRateLimit
	case 503:
		return llm.KindOverloaded
	case 504:
		return llm.KindTimeout
	default:
		return llm.KindProvider
	}
}

// streamError turns an SSE error event into a canonical provider error.
func streamError(code, message string) error {
	kind := classifyCode(code)
	return &llm.Error{
		Kind: kind,
		Err:  fmt.Errorf("openai: stream error %s: %s", code, message),
	}
}

// classifyCode maps Responses error codes onto the canonical taxonomy.
func classifyCode(code string) llm.Kind {
	switch code {
	case "invalid_prompt", "invalid_image", "invalid_image_format",
		"invalid_base64_image", "invalid_image_url", "image_too_large",
		"image_too_small", "image_parse_error", "invalid_image_mode",
		"image_file_too_large", "unsupported_image_media_type",
		"empty_image_file", "failed_to_download_image", "image_file_not_found":
		return llm.KindInvalid
	case "rate_limit_exceeded":
		return llm.KindRateLimit
	case "context_length_exceeded":
		return llm.KindContextOverflow
	case "content_policy_violation", "image_content_policy_violation":
		return llm.KindSafety
	default:
		return llm.KindProvider
	}
}
