package llm

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// ImageFromFile loads an image from the local filesystem, detecting its MIME type automatically if possible.
func ImageFromFile(path string) (Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Part{}, fmt.Errorf("llm: failed to read image file: %w", err)
	}
	return Part{
		Type:     PartTypeImage,
		Data:     data,
		MIMEType: detectMIMEType(path, data),
	}, nil
}

func detectMIMEType(path string, data []byte) string {
	ext := filepath.Ext(path)
	if ext != "" {
		if m := mime.TypeByExtension(ext); m != "" {
			return m
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}
