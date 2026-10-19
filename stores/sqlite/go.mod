module github.com/ijaidev/gojent/stores/sqlite

go 1.25.5

require (
	github.com/ijaidev/gojent v0.0.0-00010101000000-000000000000
	github.com/mattn/go-sqlite3 v1.14.28
)

// Local development: resolve the unpublished root module from this checkout.
// Remove this replace when the modules are first tagged and published.
replace github.com/ijaidev/gojent => ../..
