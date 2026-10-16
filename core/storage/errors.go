package storage

import "errors"

var (
	// ErrThreadNotFound is returned when a requested thread cannot be found.
	ErrThreadNotFound = errors.New("storage: thread not found")

	// ErrMessageNotFound is returned when a requested message cannot be found.
	ErrMessageNotFound = errors.New("storage: message not found")

	// ErrStateNotFound is returned when a requested thread state key cannot be found.
	ErrStateNotFound = errors.New("storage: thread state not found")

	// ErrThreadRequired is returned when a thread ID is missing or empty.
	ErrThreadRequired = errors.New("storage: thread ID is required")

	// ErrInvalidInput is returned when supplied storage arguments or options are invalid.
	ErrInvalidInput = errors.New("storage: invalid input")

	// ErrClosed is returned when an operation is performed on a closed storage backend.
	ErrClosed = errors.New("storage: store is closed")
)
