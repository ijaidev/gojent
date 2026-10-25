package llm

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// FilePart is one media or document payload. MIMEType decides how an adapter
// encodes it, so there are no separate image, audio, video, or document part
// types (D2, D3).
//
// Exactly one source must be set: inline Data, a URL, or a provider FileID.
type FilePart struct {
	partBase
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data,omitempty"`
	URL      string `json:"url,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// FileFromBytes creates a file part from an in-memory payload.
func FileFromBytes(data []byte, mimeType string) *FilePart {
	return &FilePart{
		partBase: partBase{Type: PartTypeFile},
		MIMEType: mimeType,
		Data:     data,
	}
}

// FileFromURL creates a file part that references a remote URL.
func FileFromURL(url, mimeType string) *FilePart {
	return &FilePart{
		partBase: partBase{Type: PartTypeFile},
		MIMEType: mimeType,
		URL:      url,
	}
}

// FileFromID creates a file part that references a provider-side file.
func FileFromID(fileID, mimeType string) *FilePart {
	return &FilePart{
		partBase: partBase{Type: PartTypeFile},
		MIMEType: mimeType,
		FileID:   fileID,
	}
}

// FileFromFile loads a file from disk and detects its MIME type. The
// extension wins; content sniffing is the fallback.
func FileFromFile(path string) (*FilePart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("llm: read file %q: %w", path, err)
	}
	part := FileFromBytes(data, detectMIMEType(path, data))
	part.Filename = filepath.Base(path)
	return part, nil
}

func detectMIMEType(path string, data []byte) string {
	if ext := filepath.Ext(path); ext != "" {
		if inferred := mime.TypeByExtension(ext); inferred != "" {
			return inferred
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}

func (p *FilePart) validate() error {
	if err := p.partBase.validateType(PartTypeFile); err != nil {
		return err
	}
	if p.MIMEType == "" {
		return fmt.Errorf("%w: file is missing a MIME type", ErrInvalidPart)
	}
	if _, _, err := mime.ParseMediaType(p.MIMEType); err != nil {
		return fmt.Errorf("%w: invalid MIME type %q: %v", ErrInvalidPart, p.MIMEType, err)
	}
	sources := 0
	if len(p.Data) > 0 {
		sources++
	}
	if p.URL != "" {
		sources++
	}
	if p.FileID != "" {
		sources++
	}
	switch sources {
	case 0:
		return fmt.Errorf("%w: file needs data, url, or file_id", ErrInvalidPart)
	case 1:
		return nil
	default:
		return fmt.Errorf("%w: file has more than one source", ErrInvalidPart)
	}
}
