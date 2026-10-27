package httpapi

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// frontendHandler serves a built static frontend. Unknown extensionless paths
// fall back to index.html so client-side routes can be opened directly.
func frontendHandler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		cleaned := path.Clean("/" + r.URL.Path)
		if cleaned != "/" {
			requested := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
			if info, err := os.Stat(requested); err == nil {
				if !info.IsDir() {
					files.ServeHTTP(w, r)
					return
				}
				// Next.js writes each static route to <route>/index.html.
				page := filepath.Join(requested, "index.html")
				if pageInfo, pageErr := os.Stat(page); pageErr == nil && !pageInfo.IsDir() {
					http.ServeFile(w, r, page)
					return
				}
			}
			if strings.Contains(path.Base(cleaned), ".") {
				http.NotFound(w, r)
				return
			}
		}

		http.ServeFile(w, r, index)
	})
}
