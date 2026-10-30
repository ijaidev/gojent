// Package frontend exposes the built codjent web UI.
package frontend

import "embed"

// Assets is the static Next.js export produced by `pnpm build` in this
// directory. It is committed so `go install` and `go build` produce a
// self-contained binary without requiring Node.js.
//
// The all: prefix includes files and directories beginning with "_" or ".",
// which covers Next.js's _next assets.
//
//go:embed all:out
var Assets embed.FS
