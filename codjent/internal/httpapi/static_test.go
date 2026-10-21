package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontendHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("codjent app"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	server := httptest.NewServer(frontendHandler(dir))
	t.Cleanup(server.Close)

	tests := []struct {
		path        string
		status      int
		body        string
		contentType string
	}{
		{path: "/", status: http.StatusOK, body: "codjent app", contentType: "text/html"},
		{path: "/assets/app.js", status: http.StatusOK, body: "console.log(1)", contentType: "text/javascript"},
		{path: "/threads/abc", status: http.StatusOK, body: "codjent app", contentType: "text/html"},
		{path: "/missing.js", status: http.StatusNotFound},
		{path: "/api/unknown", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			resp, err := server.Client().Get(server.URL + test.path)
			if err != nil {
				t.Fatalf("GET %s: %v", test.path, err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, test.status)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if test.body != "" && string(body) != test.body {
				t.Fatalf("body = %q, want %q", body, test.body)
			}
			if test.contentType != "" && !strings.HasPrefix(resp.Header.Get("Content-Type"), test.contentType) {
				t.Fatalf("content type = %q, want prefix %q", resp.Header.Get("Content-Type"), test.contentType)
			}
		})
	}
}
