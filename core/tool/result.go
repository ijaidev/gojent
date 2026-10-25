package tool

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/ijaidev/gojent/core/llm"
)

// Result represents the execution output of a tool, supporting both textual and rich multipart components.
type Result struct {
	// Content contains the message parts returned by the tool (text, images, audio, video, documents).
	Content []llm.Part `json:"content"`
	// Data contains optional structured result data for programmatic consumption.
	Data any `json:"data,omitempty"`
}

// Text returns the aggregated textual output of all text parts in the result.
func (r Result) Text() string {
	return llm.TextFromParts(r.Content)
}

// Parts returns the content to deliver to the model. If Content is empty and
// Data is set, Data is encoded as a JSON text part.
func (r Result) Parts() ([]llm.Part, error) {
	if len(r.Content) > 0 {
		return r.Content, nil
	}
	if r.Data == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(r.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOutputEncode, err)
	}
	return []llm.Part{llm.NewTextPart(string(encoded))}, nil
}

// NewFileResult creates a tool result with optional text and a file part.
func NewFileResult(text string, part llm.Part) Result {
	parts := make([]llm.Part, 0, 2)
	if text != "" {
		parts = append(parts, llm.NewTextPart(text))
	}
	parts = append(parts, part)
	return Result{Content: parts}
}

func normalizeResult(result Result) (Result, error) {
	if len(result.Content) > 0 || result.Data == nil {
		return result, nil
	}
	parts, err := result.Parts()
	if err != nil {
		return Result{}, err
	}
	result.Content = parts
	return result, nil
}

// resultFromOutput converts a typed handler output into the normalized result
// contract. Typed values become both structured Data and JSON text content.
func resultFromOutput[Out any](out Out) (Result, error) {
	switch value := any(out).(type) {
	case Result:
		return normalizeResult(value)
	case *Result:
		if value == nil {
			return Result{}, nil
		}
		return normalizeResult(*value)
	case llm.Part:
		return Result{Content: []llm.Part{value}}, nil
	case []llm.Part:
		return Result{Content: append([]llm.Part(nil), value...)}, nil
	case string:
		return Result{Content: []llm.Part{llm.NewTextPart(value)}}, nil
	case []byte:
		return Result{Content: []llm.Part{llm.NewTextPart(string(value))}}, nil
	case nil:
		return Result{}, nil
	}

	typ := reflect.TypeOf(out)
	if typ != nil && !implementsJSONMarshaler(typ) {
		switch typ.Kind() {
		case reflect.String:
			return Result{Content: []llm.Part{llm.NewTextPart(reflect.ValueOf(out).String())}}, nil
		case reflect.Slice:
			if typ.Elem().Kind() == reflect.Uint8 {
				return Result{Content: []llm.Part{llm.NewTextPart(string(reflect.ValueOf(out).Bytes()))}}, nil
			}
		}
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrOutputEncode, err)
	}
	return normalizeResult(Result{
		Content: []llm.Part{llm.NewTextPart(string(encoded))},
		Data:    out,
	})
}
