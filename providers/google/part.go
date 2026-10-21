package google

import (
	"fmt"
	"strings"

	"github.com/ijaidev/gojent/core/llm"
	"google.golang.org/genai"
)

// convertPartToGemini translates an individual Gojent llm.Part to a Google Gen AI genai.Part.
func convertPartToGemini(p llm.Part) (*genai.Part, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	switch p.Type {
	case llm.PartTypeText:
		return genai.NewPartFromText(p.Text), nil

	case llm.PartTypeImage:
		mimeType := p.MIMEType
		if mimeType != "" && !isSupportedImageMIME(mimeType) {
			return nil, fmt.Errorf("google: unsupported image MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
		if mimeType == "" {
			mimeType = "image/png"
		}

		if len(p.Data) > 0 {
			return genai.NewPartFromBytes(p.Data, mimeType), nil
		}

		if p.URL != "" {
			if strings.HasPrefix(p.URL, "gs://") || strings.HasPrefix(p.URL, "https://generativelanguage.googleapis.com/") {
				return genai.NewPartFromURI(p.URL, mimeType), nil
			}
			return nil, fmt.Errorf("google: direct HTTP/HTTPS URL %q not supported by Gemini API (upload via Files API or provide inline bytes): %w", p.URL, llm.ErrUnsupportedPart)
		}

		return nil, fmt.Errorf("google: image part must provide Data or URL: %w", llm.ErrInvalidPart)

	case llm.PartTypeAudio:
		mimeType := p.MIMEType
		if mimeType != "" && !isSupportedAudioMIME(mimeType) {
			return nil, fmt.Errorf("google: unsupported audio MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
		if mimeType == "" {
			mimeType = "audio/wav"
		}

		if len(p.Data) > 0 {
			return genai.NewPartFromBytes(p.Data, mimeType), nil
		}
		if p.URL != "" {
			return genai.NewPartFromURI(p.URL, mimeType), nil
		}
		return nil, fmt.Errorf("google: audio part must provide Data or URL: %w", llm.ErrInvalidPart)

	case llm.PartTypeVideo:
		mimeType := p.MIMEType
		if mimeType != "" && !isSupportedVideoMIME(mimeType) {
			return nil, fmt.Errorf("google: unsupported video MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
		if mimeType == "" {
			mimeType = "video/mp4"
		}

		if len(p.Data) > 0 {
			return genai.NewPartFromBytes(p.Data, mimeType), nil
		}
		if p.URL != "" {
			return genai.NewPartFromURI(p.URL, mimeType), nil
		}
		return nil, fmt.Errorf("google: video part must provide Data or URL: %w", llm.ErrInvalidPart)

	case llm.PartTypeDocument:
		mimeType := p.MIMEType
		if mimeType != "" && !isSupportedDocMIME(mimeType) {
			return nil, fmt.Errorf("google: unsupported document MIME type %q: %w", mimeType, llm.ErrUnsupportedMIMEType)
		}
		if mimeType == "" {
			mimeType = "application/pdf"
		}

		if len(p.Data) > 0 {
			return genai.NewPartFromBytes(p.Data, mimeType), nil
		}
		if p.URL != "" {
			return genai.NewPartFromURI(p.URL, mimeType), nil
		}
		return nil, fmt.Errorf("google: document part must provide Data or URL: %w", llm.ErrInvalidPart)

	default:
		return nil, fmt.Errorf("google: unsupported part type %q: %w", p.Type, llm.ErrUnsupportedPartType)
	}
}

// convertToolPartToGemini translates a tool media part to a Google Gen AI FunctionResponsePart.
func convertToolPartToGemini(p llm.Part) (*genai.FunctionResponsePart, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	mimeType := p.MIMEType
	if mimeType == "" {
		switch p.Type {
		case llm.PartTypeImage:
			mimeType = "image/png"
		case llm.PartTypeDocument:
			mimeType = "application/pdf"
		case llm.PartTypeAudio:
			mimeType = "audio/wav"
		case llm.PartTypeVideo:
			mimeType = "video/mp4"
		default:
			mimeType = "application/octet-stream"
		}
	}

	if len(p.Data) > 0 {
		return genai.NewFunctionResponsePartFromBytes(p.Data, mimeType), nil
	}
	if p.URL != "" {
		return genai.NewFunctionResponsePartFromURI(p.URL, mimeType), nil
	}
	return nil, fmt.Errorf("google: tool part must provide Data or URL: %w", llm.ErrInvalidPart)
}
