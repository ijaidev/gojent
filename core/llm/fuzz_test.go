package llm_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func FuzzDecodeMessage(f *testing.F) {
	seeds := []string{
		`{"role":"user","content":[{"type":"text","text":"hi"}]}`,
		`{"role":"assistant","content":[{"type":"reasoning","text":"r"},{"type":"tool_call","call_id":"c","name":"x","input":{"q":1}}]}`,
		`{"role":"user","content":[{"type":"tool_result","call_id":"c","status":"completed","content":[{"type":"text","text":"ok"}]}]}`,
		`{"role":"assistant","content":[{"type":"file","mime_type":"image/png","data":"aGk="}]}`,
		`{"role":"assistant","content":[{"type":"refusal","text":"no"}]}`,
		`{"role":"assistant","content":[{"type":"unknown"}]}`,
		`{"role":"user","content":[]}`,
		`{}`,
		`null`,
		`{"role":"user","content":"text"}`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var message llm.Message
		err := json.Unmarshal(data, &message)
		if err != nil {
			if errors.Is(err, llm.ErrUnknownPartType) {
				return
			}
			return
		}
		// Decoding must never panic and must produce a message that can be
		// re-encoded. Validation may reject semantically invalid input.
		_ = message.Validate()
		encoded, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			t.Fatalf("Marshal after successful Unmarshal: %v", marshalErr)
		}
		var roundTrip llm.Message
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatalf("Unmarshal(Marshal(message)): %v", err)
		}
	})
}
