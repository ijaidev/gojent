package httpapi

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFrontendHandler(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":          {Data: []byte("codjent app")},
		"settings":            {Mode: fs.ModeDir | 0o755},
		"settings/index.html": {Data: []byte("settings page")},
		"assets":              {Mode: fs.ModeDir | 0o755},
		"assets/app.js":       {Data: []byte("console.log(1)")},
	}

	server := httptest.NewServer(frontendHandler(fsys))
	t.Cleanup(server.Close)

	tests := []struct {
		path        string
		status      int
		body        string
		contentType string
	}{
		{path: "/", status: http.StatusOK, body: "codjent app", contentType: "text/html"},
		{path: "/assets/app.js", status: http.StatusOK, body: "console.log(1)", contentType: "text/javascript"},
		{path: "/settings", status: http.StatusOK, body: "settings page", contentType: "text/html"},
		{path: "/settings/", status: http.StatusOK, body: "settings page", contentType: "text/html"},
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
