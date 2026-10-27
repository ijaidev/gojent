// Package llm defines the provider-neutral model contract, requests,
// responses, messages, and streams used by the agent runtime.
//
// The package depends only on the Go standard library. Provider adapters live
// in separate modules and implement Model.
package llm

import "context"

// Model is the provider contract required by the agent runner.
//
// Info is mandatory: the runner checks capabilities before building a request
// rather than probing for optional interfaces.
type Model interface {
	// ModelID returns the provider-facing model identifier.
	ModelID() string
	// Info reports static model metadata.
	Info() Info
	// Generate produces one complete assistant response.
	Generate(ctx context.Context, req *Request) (*Response, error)
}

// Streamer is an optional capability implemented by models that support
// incremental generation.
type Streamer interface {
	Model
	Stream(ctx context.Context, req *Request) (Stream, error)
}

// CacheMode describes prompt-caching support.
type CacheMode string

const (
	// CacheModeNone means the provider does not cache prompts.
	CacheModeNone CacheMode = "none"
	// CacheModeAutomatic means the provider caches without explicit markers.
	CacheModeAutomatic CacheMode = "automatic"
	// CacheModeExplicit means callers must mark cache breakpoints.
	CacheModeExplicit CacheMode = "explicit"
)

// Info describes static model metadata.
type Info struct {
	// Provider identifies the adapter, for example "google".
	Provider string `json:"provider"`
	// Model is the provider-facing model identifier.
	Model string `json:"model"`
	// Limits reports context and output limits.
	Limits Limits `json:"limits"`
	// Features reports optional capabilities.
	Features Features `json:"features"`
	// Pricing is expressed in US dollars per million tokens.
	Pricing Pricing `json:"pricing"`
}

// Limits describes token limits. Zero means unknown.
type Limits struct {
	// ContextWindow is the maximum number of input tokens accepted.
	ContextWindow int `json:"context_window"`
	// MaxOutput is the maximum number of output tokens.
	MaxOutput int `json:"max_output"`
}

// Features describes optional model capabilities. Adapters must report these
// accurately; the runner rejects unsupported requests with ErrUnsupported.
type Features struct {
	Streaming          bool      `json:"streaming"`
	Tools              bool      `json:"tools"`
	ParallelToolCalls  bool      `json:"parallel_tool_calls"`
	StrictJSONSchema   bool      `json:"strict_json_schema"`
	ServerState        bool      `json:"server_state"`
	HostedTools        bool      `json:"hosted_tools"`
	MCP                bool      `json:"mcp"`
	Reasoning          bool      `json:"reasoning"`
	ReasoningSummary   bool      `json:"reasoning_summary"`
	ReasoningEncrypted bool      `json:"reasoning_encrypted"`
	Vision             bool      `json:"vision"`
	AudioIn            bool      `json:"audio_in"`
	AudioOut           bool      `json:"audio_out"`
	Video              bool      `json:"video"`
	Documents          bool      `json:"documents"`
	PromptCaching      CacheMode `json:"prompt_caching"`
	Citations          bool      `json:"citations"`
}

// Pricing describes model cost in US dollars per million tokens. All fields
// are zero when pricing is unknown.
type Pricing struct {
	// InputPerMTokens is the cost of one million input tokens.
	InputPerMTokens float64 `json:"input_per_m_tokens"`
	// OutputPerMTokens is the cost of one million output tokens.
	OutputPerMTokens float64 `json:"output_per_m_tokens"`
}
