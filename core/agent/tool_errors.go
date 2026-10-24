package agent

import (
	"encoding/json"
	"errors"
	"fmt"
)

// recoveryError renders a panic as a provider-safe tool result.
func recoveryError(err error) string {
	var panicErr *PanicError
	if errors.As(err, &panicErr) {
		if panicErr == nil {
			return "panic: <nil>"
		}
		return fmt.Sprintf("panic: %v", panicErr.Value)
	}
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

// toolErrorPayload renders an error as the JSON object delivered to a model.
func toolErrorPayload(err error) string {
	if err == nil {
		return `{"error":"unknown error"}`
	}
	encoded, marshalErr := json.Marshal(map[string]string{"error": recoveryError(err)})
	if marshalErr != nil {
		return `{"error":"tool failed"}`
	}
	return string(encoded)
}
