package session_test

import (
	"testing"

	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/session/sessiontest"
)

func TestConformance(t *testing.T) {
	sessiontest.Run(t, func(t *testing.T) session.Store {
		return session.NewMemory()
	})
}
