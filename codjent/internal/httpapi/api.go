// Package httpapi exposes codjent's service over JSON and server-sent events.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/codjent/internal/provider/catalog"
)

const (
	maxRequestBody        = 1 << 20
	maxMessageRequestBody = 16 << 20
)

type api struct {
	service *app.Service
	catalog *catalog.Catalog
	origins map[string]struct{}
}

// New returns the codjent HTTP handler.
func New(service *app.Service, allowedOrigins []string) http.Handler {
	return NewWithStatic(service, allowedOrigins, nil)
}

// NewWithStatic returns the codjent HTTP handler. When frontend is not nil,
// non-API GET requests are served from it, with an index.html fallback for
// client-side routes.
func NewWithStatic(service *app.Service, allowedOrigins []string, frontend fs.FS) http.Handler {
	return NewWithCatalog(service, nil, allowedOrigins, frontend)
}

// NewWithCatalog returns the codjent HTTP handler with provider settings
// routes. A nil catalog disables those routes. A nil frontend serves the API
// only.
func NewWithCatalog(service *app.Service, providers *catalog.Catalog, allowedOrigins []string, frontend fs.FS) http.Handler {
	a := &api{
		service: service,
		catalog: providers,
		origins: make(map[string]struct{}, len(allowedOrigins)),
	}
	for _, origin := range allowedOrigins {
		a.origins[normalizeOrigin(origin)] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/providers", a.providers)
	if a.catalog != nil {
		mux.HandleFunc("GET /api/settings/providers", a.listProviderConfigs)
		mux.HandleFunc("POST /api/settings/providers", a.createProviderConfig)
		mux.HandleFunc("PUT /api/settings/providers/{providerID}", a.updateProviderConfig)
		mux.HandleFunc("DELETE /api/settings/providers/{providerID}", a.deleteProviderConfig)
	}
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
	if frontend != nil {
		mux.Handle("GET /", frontendHandler(frontend))
	}

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

func (a *api) listProviderConfigs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.catalog.Configs())
}

func (a *api) createProviderConfig(w http.ResponseWriter, r *http.Request) {
	var input provider.ConfigInput
	if err := readJSON(w, r, &input); err != nil {
		writeError(w, err)
		return
	}
	config, err := a.catalog.Create(r.Context(), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, config)
}

func (a *api) updateProviderConfig(w http.ResponseWriter, r *http.Request) {
	var input provider.ConfigInput
	if err := readJSON(w, r, &input); err != nil {
		writeError(w, err)
		return
	}
	config, err := a.catalog.Update(r.Context(), r.PathValue("providerID"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (a *api) deleteProviderConfig(w http.ResponseWriter, r *http.Request) {
	if err := a.catalog.Delete(r.Context(), r.PathValue("providerID")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	var input app.StartMessageInput
	if err := readJSONLimit(w, r, &input, maxMessageRequestBody); err != nil {
		writeError(w, err)
		return
	}
	run, err := a.service.StartMessageWith(r.Context(), r.PathValue("threadID"), input)
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
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
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
	return readJSONLimit(w, r, target, maxRequestBody)
}

func readJSONLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	if r.ContentLength != 0 {
		contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
		if !strings.HasPrefix(contentType, "application/json") {
			return fmt.Errorf("%w: Content-Type must be application/json", app.ErrInvalidInput)
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
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
	var maxBytesError *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytesError):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, app.ErrThreadNotFound),
		errors.Is(err, app.ErrRunNotFound),
		errors.Is(err, app.ErrApprovalNotFound),
		errors.Is(err, provider.ErrConfigNotFound):
		status = http.StatusNotFound
	case errors.Is(err, app.ErrThreadBusy),
		errors.Is(err, app.ErrRunFinished),
		errors.Is(err, provider.ErrConfigInUse):
		status = http.StatusConflict
	case errors.Is(err, app.ErrInvalidInput),
		errors.Is(err, provider.ErrInvalidConfig),
		errors.Is(err, provider.ErrUnknownProvider),
		errors.Is(err, provider.ErrUnknownModel):
		status = http.StatusBadRequest
	case errors.Is(err, app.ErrClosed):
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
