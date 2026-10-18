// Package llm provides interfaces, message definitions, and provider abstractions
// for interacting with Large Language Models.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

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

// PartType defines the category of content within a message part.
type PartType string

const (
	// PartTypeText represents plain text content.
	PartTypeText PartType = "text"
	// PartTypeImage represents image data (PNG, JPEG, WEBP, GIF, etc.).
	PartTypeImage PartType = "image"
	// PartTypeAudio represents audio recordings (WAV, MP3, etc.).
	PartTypeAudio PartType = "audio"
	// PartTypeVideo represents video recordings (MP4, WebM, etc.).
	PartTypeVideo PartType = "video"
	// PartTypeDocument represents document files (PDF, TXT, CSV, etc.).
	PartTypeDocument PartType = "document"
	// PartTypeFile represents arbitrary binary files.
	PartTypeFile PartType = "file"
)

// Part represents an individual multimodal component of a message (text, image, audio, video, or document).
type Part struct {
	Type     PartType `json:"type"`
	Text     string   `json:"text,omitempty"`
	MIMEType string   `json:"mime_type,omitempty"`
	Data     []byte   `json:"data,omitempty"`
	URL      string   `json:"url,omitempty"`
	Path     string   `json:"path,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// Validate checks that the part contains valid data for its declared type.
func (p Part) Validate() error {
	switch p.Type {
	case PartTypeText:
		if p.Text == "" {
			return fmt.Errorf("%w: text part cannot be empty", ErrInvalidPart)
		}
		return nil
	case PartTypeImage, PartTypeAudio, PartTypeVideo, PartTypeDocument, PartTypeFile:
		if len(p.Data) == 0 && p.URL == "" && p.Path == "" {
			return fmt.Errorf("%w: %s part must provide Data, URL, or Path", ErrInvalidPart, p.Type)
		}
		return nil
	case "":
		return fmt.Errorf("%w: part type cannot be empty", ErrInvalidPart)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedPartType, p.Type)
	}
}

// TextPart creates a new text part.
func TextPart(text string) Part {
	return Part{Type: PartTypeText, Text: text}
}

// ImageFromBytes creates an image part from raw bytes and an explicit MIME type.
func ImageFromBytes(data []byte, mimeType string) Part {
	return Part{Type: PartTypeImage, Data: data, MIMEType: mimeType}
}

// ImageFromURL creates an image part referencing a remote URL or data URI.
func ImageFromURL(url string) Part {
	return Part{Type: PartTypeImage, URL: url}
}

// ImageFromFile loads an image from the local filesystem, detecting its MIME type automatically if possible.
func ImageFromFile(path string) (Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Part{}, fmt.Errorf("llm: failed to read image file: %w", err)
	}
	mimeType := detectMIMEType(path, data)
	return Part{
		Type:     PartTypeImage,
		Data:     data,
		Path:     path,
		MIMEType: mimeType,
	}, nil
}

// AudioFromBytes creates an audio part from raw bytes and MIME type.
func AudioFromBytes(data []byte, mimeType string) Part {
	return Part{Type: PartTypeAudio, Data: data, MIMEType: mimeType}
}

// AudioFromFile loads an audio file from the filesystem.
func AudioFromFile(path string) (Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Part{}, fmt.Errorf("llm: failed to read audio file: %w", err)
	}
	mimeType := detectMIMEType(path, data)
	return Part{
		Type:     PartTypeAudio,
		Data:     data,
		Path:     path,
		MIMEType: mimeType,
	}, nil
}

// VideoFromBytes creates a video part from raw bytes and MIME type.
func VideoFromBytes(data []byte, mimeType string) Part {
	return Part{Type: PartTypeVideo, Data: data, MIMEType: mimeType}
}

// VideoFromFile loads a video file from the filesystem.
func VideoFromFile(path string) (Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Part{}, fmt.Errorf("llm: failed to read video file: %w", err)
	}
	mimeType := detectMIMEType(path, data)
	return Part{
		Type:     PartTypeVideo,
		Data:     data,
		Path:     path,
		MIMEType: mimeType,
	}, nil
}

// DocumentFromBytes creates a document part from raw bytes and MIME type.
func DocumentFromBytes(data []byte, mimeType string) Part {
	return Part{Type: PartTypeDocument, Data: data, MIMEType: mimeType}
}

// DocumentFromFile loads a document file from the filesystem.
func DocumentFromFile(path string) (Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Part{}, fmt.Errorf("llm: failed to read document file: %w", err)
	}
	mimeType := detectMIMEType(path, data)
	return Part{
		Type:     PartTypeDocument,
		Data:     data,
		Path:     path,
		MIMEType: mimeType,
	}, nil
}

// PartFromURI creates a media part pointing to a URI (e.g. gs://, https://, or Files API URI).
func PartFromURI(partType PartType, uri, mimeType string) Part {
	return Part{
		Type:     partType,
		URL:      uri,
		MIMEType: mimeType,
	}
}

func detectMIMEType(path string, data []byte) string {
	ext := filepath.Ext(path)
	if ext != "" {
		if m := mime.TypeByExtension(ext); m != "" {
			return m
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}

// Role represents the role of a message participant.
type Role string

const (
	// RoleSystem is for high-level instructions directing model behavior.
	RoleSystem Role = "system"
	// RoleUser represents the end-user or human input.
	RoleUser Role = "user"
	// RoleAssistant represents responses from the model.
	RoleAssistant Role = "assistant"
	// RoleTool represents tool execution outputs returned to the model.
	RoleTool Role = "tool"
)

// ToolCall represents a model-requested invocation of an external tool or function.
type ToolCall struct {
	// ID is the unique identifier for this tool call event.
	ID string `json:"id"`
	// Name is the name of the tool requested by the model.
	Name string `json:"name"`
	// Arguments is the JSON-encoded string of arguments provided by the model.
	Arguments string `json:"arguments"`
}

// UnmarshalArgs unmarshals the tool call's JSON arguments into the target destination.
func (tc ToolCall) UnmarshalArgs(target any) error {
	return json.Unmarshal([]byte(tc.Arguments), target)
}

// ToolDefinition describes a tool schema exposed to the model for function calling.
type ToolDefinition struct {
	// Name is the unique name of the tool.
	Name string `json:"name"`
	// Description explains what the tool does and when it should be invoked.
	Description string `json:"description"`
	// Parameters specifies the JSON Schema defining the expected inputs.
	Parameters any `json:"parameters,omitempty"`
}

// Message represents a single chat message in a conversation.
// Content is multipart by default, representing one or more multimodal parts.
type Message struct {
	Role       Role       `json:"role"`
	Content    []Part     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Raw holds provider-specific message/content payload (e.g. Gemini thought signatures or OpenAI metadata).
	Raw any `json:"raw,omitempty"`
}

// Text returns the aggregated textual content of all text parts in the message.
func (m Message) Text() string {
	var sb strings.Builder
	for _, p := range m.Content {
		if p.Type == PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// UnmarshalJSON customizes JSON unmarshaling to accept either a string or an array of parts for Content.
func (m *Message) UnmarshalJSON(data []byte) error {
	type rawMessage struct {
		Role       Role            `json:"role"`
		Content    json.RawMessage `json:"content"`
		Name       string          `json:"name,omitempty"`
		ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
		Raw        any             `json:"raw,omitempty"`
	}
	var raw rawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	m.Name = raw.Name
	m.ToolCalls = raw.ToolCalls
	m.ToolCallID = raw.ToolCallID
	m.Raw = raw.Raw

	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		m.Content = nil
		return nil
	}

	var parts []Part
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		m.Content = parts
		return nil
	}

	var str string
	if err := json.Unmarshal(raw.Content, &str); err == nil {
		m.Content = []Part{TextPart(str)}
		return nil
	}

	return fmt.Errorf("llm: invalid content format in message JSON")
}

// UserMessage creates a user message with the given text content.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: []Part{TextPart(content)}}
}

// UserMessageWithMedia creates a user message combining a text prompt with one or more media parts.
func UserMessageWithMedia(prompt string, media ...Part) Message {
	parts := make([]Part, 0, len(media)+1)
	if prompt != "" {
		parts = append(parts, TextPart(prompt))
	}
	parts = append(parts, media...)
	return Message{
		Role:    RoleUser,
		Content: parts,
	}
}

// UserMessageWithParts creates a user message containing explicit parts.
func UserMessageWithParts(parts ...Part) Message {
	return Message{
		Role:    RoleUser,
		Content: parts,
	}
}

// SystemMessage creates a system instruction message with the given content.
func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: []Part{TextPart(content)}}
}

// AssistantMessage creates an assistant message with the given content.
func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: []Part{TextPart(content)}}
}

// AssistantMessageWithParts creates an assistant message with explicit parts.
func AssistantMessageWithParts(parts ...Part) Message {
	return Message{Role: RoleAssistant, Content: parts}
}

// ToolResultMessage creates a tool execution output message for a specific tool call ID.
func ToolResultMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Content:    []Part{TextPart(content)},
	}
}

// ToolResultMessageWithParts creates a tool execution output message containing rich multipart results.
func ToolResultMessageWithParts(toolCallID, name string, parts ...Part) Message {
	return Message{
		Role:       RoleTool,
		Name:       name,
		ToolCallID: toolCallID,
		Content:    parts,
	}
}

// FinishReason represents why the LLM stopped generating tokens.
type FinishReason string

// Finish reasons returned by an LLM generation.
const (
	FinishReasonStop   FinishReason = "stop"
	FinishReasonLength FinishReason = "length"
	FinishReasonTool   FinishReason = "tool_calls"
	FinishReasonError  FinishReason = "error"
)

// Usage contains token consumption statistics for a generation request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// GenerateRequest contains the input parameters for a model generation.
type GenerateRequest struct {
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	StopWords   []string         `json:"stop_words,omitempty"`
}

// GenerateResponse represents the output from a model generation.
type GenerateResponse struct {
	Text         string       `json:"text"`
	ToolCalls    []ToolCall   `json:"tool_calls,omitempty"`
	FinishReason FinishReason `json:"finish_reason"`
	Usage        Usage        `json:"usage"`
	Raw          any          `json:"raw,omitempty"`
}

// LLM is the core swappable interface for Large Language Models.
// Any provider (OpenAI, Anthropic, Gemini, Ollama, local models, or mocks)
// can be implemented behind this interface.
type LLM interface {
	// Generate generates a non-streaming response for the given request.
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
	// Stream generates a streaming response for the given request.
	Stream(ctx context.Context, req GenerateRequest) (Stream, error)
	// ModelID returns the identifier of the model (e.g. \"gpt-4o\", \"claude-3-5-sonnet\", \"mock-llm\").
	ModelID() string
}

// Model is an alias for LLM.
type Model = LLM
