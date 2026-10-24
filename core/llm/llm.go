// Package llm defines the provider-neutral model contract, requests,
// responses, messages, and streams used by the agent runtime.
//
// The package depends only on the Go standard library. Provider adapters live
// in separate modules and implement Model, and optionally Streamer and
// InfoProvider.
package llm

import "context"

// Model is the provider contract required by the agent runner.
//
// A model must implement Generate. Providers that support incremental output
// also implement Streamer; the runner prefers streaming when it is available.
type Model interface {
	// ModelID returns the provider-facing model identifier.
	ModelID() string
	// Generate produces one complete assistant response.
	Generate(ctx context.Context, req *Request) (*Response, error)
}

// Streamer is an optional capability implemented by models that support
// incremental generation. A Streamer must also implement Model.
type Streamer interface {
	Stream(ctx context.Context, req *Request) (Stream, error)
}

// InfoProvider is an optional capability exposing model limits and pricing.
type InfoProvider interface {
	Info() Info
}

// Info describes static model metadata.
type Info struct {
	// ContextWindow is the maximum number of input tokens accepted, or 0 when
	// unknown.
	ContextWindow int
	// MaxOutput is the maximum number of output tokens, or 0 when unknown.
	MaxOutput int
	// Capabilities describes optional provider features.
	Capabilities Capabilities
	// Pricing is expressed in US dollars per million tokens.
	Pricing Pricing
}

// Capabilities describes optional model features.
type Capabilities struct {
	Streaming  bool
	Tools      bool
	JSONSchema bool
	Vision     bool
	Audio      bool
	Video      bool
	Documents  bool
	Reasoning  bool
}

// Pricing describes model cost in US dollars per million tokens.
type Pricing struct {
	InputPerMTokens  float64
	OutputPerMTokens float64
}
