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
	mimeType := detectMIMEType(path, data)
	return Part{
		Type:     PartTypeImage,
		Data:     data,
		Path:     path,
		MIMEType: mimeType,
	}, nil
}

// AudioFromFile loads an audio file from the filesystem.
func AudioFromFile(path string) (Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Part{}, fmt.Errorf("llm: failed to read audio file: %w", err)
	}
	mimeType := detectMIMEType(path, data)
	return Part{
		Type:     PartTypeAudio,
		Data:     data,
		Path:     path,
		MIMEType: mimeType,
	}, nil
}

// VideoFromFile loads a video file from the filesystem.
func VideoFromFile(path string) (Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Part{}, fmt.Errorf("llm: failed to read video file: %w", err)
	}
	mimeType := detectMIMEType(path, data)
	return Part{
		Type:     PartTypeVideo,
		Data:     data,
		Path:     path,
		MIMEType: mimeType,
	}, nil
}

// DocumentFromFile loads a document file from the filesystem.
func DocumentFromFile(path string) (Part, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Part{}, fmt.Errorf("llm: failed to read document file: %w", err)
	}
	mimeType := detectMIMEType(path, data)
	return Part{
		Type:     PartTypeDocument,
		Data:     data,
		Path:     path,
		MIMEType: mimeType,
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
