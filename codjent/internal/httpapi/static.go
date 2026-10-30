package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// frontendHandler serves a built static frontend from fsys. Unknown
// extensionless paths fall back to index.html so client-side routes can be
// opened directly.
func frontendHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		cleaned := path.Clean("/" + r.URL.Path)
		if cleaned != "/" {
			requested := strings.TrimPrefix(cleaned, "/")
			if info, err := fs.Stat(fsys, requested); err == nil {
				if !info.IsDir() {
					files.ServeHTTP(w, r)
					return
				}
				// Next.js writes each static route to <route>/index.html.
				page := path.Join(requested, "index.html")
				if pageInfo, pageErr := fs.Stat(fsys, page); pageErr == nil && !pageInfo.IsDir() {
					http.ServeFileFS(w, r, fsys, page)
					return
				}
			}
			if strings.Contains(path.Base(cleaned), ".") {
				http.NotFound(w, r)
				return
			}
		}

		http.ServeFileFS(w, r, fsys, "index.html")
	})
}
