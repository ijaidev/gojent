package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ijaidev/gojent/codjent/internal/app"
)

func (a *api) streamRun(w http.ResponseWriter, r *http.Request) {
	after, err := lastEventID(r)
	if err != nil {
		writeError(w, fmt.Errorf("%w: %w", app.ErrInvalidInput, err))
		return
	}

	backlog, live, cancel, err := a.service.Subscribe(r.PathValue("runID"), after)
	if err != nil {
		writeError(w, err)
		return
	}
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, errors.New("streaming is not supported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for _, event := range backlog {
		if err := writeEvent(w, event); err != nil {
			return
		}
		flusher.Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-live:
			if !open {
				return
			}
			if err := writeEvent(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func lastEventID(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("invalid event ID %q", raw)
	}
	return id, nil
}

func writeEvent(w http.ResponseWriter, event app.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, data); err != nil {
		return err
	}
	return nil
}
