module github.com/ijaidev/gojent/stores/postgres

go 1.25.5

require (
	github.com/DATA-DOG/go-sqlmock v1.5.2
	github.com/ijaidev/gojent v0.0.0-00010101000000-000000000000
	github.com/lib/pq v1.12.3
)

// Local development: resolve the unpublished root module from this checkout.
// Remove this replace when the modules are first tagged and published.
replace github.com/ijaidev/gojent => ../..
