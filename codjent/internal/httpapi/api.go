// Package httpapi exposes codjent's service over JSON and server-sent events.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/provider"
)

const maxRequestBody = 1 << 20

type api struct {
	service *app.Service
	origins map[string]struct{}
}

// New returns the codjent HTTP handler.
func New(service *app.Service, allowedOrigins []string) http.Handler {
	a := &api{
		service: service,
		origins: make(map[string]struct{}, len(allowedOrigins)),
	}
	for _, origin := range allowedOrigins {
		a.origins[normalizeOrigin(origin)] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/providers", a.providers)
	mux.HandleFunc("GET /api/threads", a.listThreads)
	mux.HandleFunc("POST /api/threads", a.createThread)
	mux.HandleFunc("GET /api/threads/{threadID}", a.getThread)
	mux.HandleFunc("PATCH /api/threads/{threadID}", a.updateThread)
	mux.HandleFunc("DELETE /api/threads/{threadID}", a.deleteThread)
	mux.HandleFunc("POST /api/threads/{threadID}/messages", a.startMessage)
	mux.HandleFunc("GET /api/runs/{runID}", a.getRun)
	mux.HandleFunc("GET /api/runs/{runID}/events", a.streamRun)
	mux.HandleFunc("POST /api/runs/{runID}/approvals/{approvalID}", a.resolveApproval)
	mux.HandleFunc("POST /api/runs/{runID}/cancel", a.cancelRun)

	return securityHeaders(a.cors(mux))
}

func (a *api) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"workspace": a.service.Workspace(),
	})
}

func (a *api) providers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.service.Providers())
}

func (a *api) listThreads(w http.ResponseWriter, r *http.Request) {
	threads, err := a.service.ListThreads(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if threads == nil {
		threads = []*app.Thread{}
	}
	writeJSON(w, http.StatusOK, threads)
}

func (a *api) createThread(w http.ResponseWriter, r *http.Request) {
	var input app.CreateThreadInput
	if err := readJSON(w, r, &input); err != nil {
		writeError(w, err)
		return
	}
	thread, err := a.service.CreateThread(r.Context(), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, thread)
}

func (a *api) getThread(w http.ResponseWriter, r *http.Request) {
	detail, err := a.service.GetThread(r.Context(), r.PathValue("threadID"))
	if err != nil {
		writeError(w, err)
		return
	}
	if detail.Messages == nil {
		detail.Messages = []*app.Message{}
	}
	writeJSON(w, http.StatusOK, detail)
}

func (a *api) updateThread(w http.ResponseWriter, r *http.Request) {
	var input app.UpdateThreadInput
	if err := readJSON(w, r, &input); err != nil {
		writeError(w, err)
		return
	}
	thread, err := a.service.UpdateThread(r.Context(), r.PathValue("threadID"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (a *api) deleteThread(w http.ResponseWriter, r *http.Request) {
	if err := a.service.DeleteThread(r.Context(), r.PathValue("threadID")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) startMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Content string `json:"content"`
	}
	if err := readJSON(w, r, &input); err != nil {
		writeError(w, err)
		return
	}
	run, err := a.service.StartMessage(r.Context(), r.PathValue("threadID"), input.Content)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (a *api) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.service.GetRun(r.PathValue("runID"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (a *api) resolveApproval(w http.ResponseWriter, r *http.Request) {
	var input app.ApprovalDecision
	if err := readJSON(w, r, &input); err != nil {
		writeError(w, err)
		return
	}
	if err := a.service.ResolveApproval(
		r.PathValue("runID"),
		r.PathValue("approvalID"),
		input,
	); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) cancelRun(w http.ResponseWriter, r *http.Request) {
	if err := a.service.CancelRun(r.PathValue("runID")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !a.originAllowed(origin, r.Host) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *api) originAllowed(origin, host string) bool {
	normalized := normalizeOrigin(origin)
	if _, ok := a.origins[normalized]; ok {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, host)
}

func normalizeOrigin(origin string) string {
	return strings.TrimRight(strings.TrimSpace(origin), "/")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if r.ContentLength != 0 {
		contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
		if !strings.HasPrefix(contentType, "application/json") {
			return fmt.Errorf("%w: Content-Type must be application/json", app.ErrInvalidInput)
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%w: %w", app.ErrInvalidInput, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body must contain one JSON value", app.ErrInvalidInput)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, app.ErrThreadNotFound),
		errors.Is(err, app.ErrRunNotFound),
		errors.Is(err, app.ErrApprovalNotFound):
		status = http.StatusNotFound
	case errors.Is(err, app.ErrThreadBusy),
		errors.Is(err, app.ErrRunFinished):
		status = http.StatusConflict
	case errors.Is(err, app.ErrInvalidInput),
		errors.Is(err, provider.ErrUnknownProvider),
		errors.Is(err, provider.ErrUnknownModel):
		status = http.StatusBadRequest
	case errors.Is(err, app.ErrClosed):
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
