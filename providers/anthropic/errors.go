package anthropic

import (
	"context"
	"errors"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared"
	"github.com/ijaidev/gojent/core/llm"
)

// wrapError maps SDK and transport failures onto the canonical error taxonomy.
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && apiErr != nil {
		return &llm.Error{
			Kind:       classifyAPIError(apiErr),
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

// classifyAPIError prefers the API error type, which also classifies SSE
// error events that arrive on an otherwise successful HTTP response.
func classifyAPIError(err *anthropic.Error) llm.Kind {
	switch err.Type() {
	case shared.ErrorTypeInvalidRequestError, shared.ErrorTypeNotFoundError:
		return llm.KindInvalid
	case shared.ErrorTypeAuthenticationError, shared.ErrorTypePermissionError, shared.ErrorTypeBillingError:
		return llm.KindAuth
	case shared.ErrorTypeRateLimitError:
		return llm.KindRateLimit
	case shared.ErrorTypeTimeoutError:
		return llm.KindTimeout
	case shared.ErrorTypeOverloadedError:
		return llm.KindOverloaded
	}
	if kind := classifyStatus(err.StatusCode); kind != llm.KindProvider {
		return kind
	}
	return llm.KindProvider
}

func classifyStatus(code int) llm.Kind {
	switch code {
	case 400, 404, 422:
		return llm.KindInvalid
	case 401, 403:
		return llm.KindAuth
	case 408:
		return llm.KindTimeout
	case 413:
		return llm.KindContextOverflow
	case 429:
		return llm.KindRateLimit
	case 500, 502, 503, 504, 529:
		return llm.KindOverloaded
	default:
		return llm.KindProvider
	}
}
