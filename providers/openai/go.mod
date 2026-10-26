module github.com/ijaidev/gojent/providers/openai

go 1.25.5

require (
	github.com/ijaidev/gojent v0.0.0-00010101000000-000000000000
	github.com/openai/openai-go v1.12.0
)

require (
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
)

// Local development: resolve the unpublished root module from this checkout.
// Remove this replace when the modules are first tagged and published.
replace github.com/ijaidev/gojent => ../..
