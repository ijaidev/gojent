package agent_test

import (
	"testing"

	"github.com/ijaidev/gojent/core/agent"
)

// drainStream consumes a stream and returns its final response.
func drainStream(t *testing.T, stream *agent.StreamResponse) (*agent.Response, error) {
	t.Helper()
	for _, err := range stream.All() {
		if err != nil {
			return nil, err
		}
	}
	return stream.Response()
}
