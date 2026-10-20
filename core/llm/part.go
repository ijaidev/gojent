package llm

import "fmt"

// PartType defines the category of content within a message part.
type PartType string

const (
	// PartTypeText represents plain text content.
	PartTypeText PartType = "text"
	// PartTypeImage represents image data (PNG, JPEG, WEBP, GIF, etc.).
	PartTypeImage PartType = "image"
	// PartTypeAudio represents audio recordings (WAV, MP3, etc.).
	PartTypeAudio PartType = "audio"
	// PartTypeVideo represents video recordings (MP4, WebM, etc.).
	PartTypeVideo PartType = "video"
	// PartTypeDocument represents document files (PDF, TXT, CSV, etc.).
	PartTypeDocument PartType = "document"
	// PartTypeFile represents arbitrary binary files.
	PartTypeFile PartType = "file"
)

// Part represents an individual multimodal component of a message (text, image, audio, video, or document).
type Part struct {
	Type     PartType `json:"type"`
	Text     string   `json:"text,omitempty"`
	MIMEType string   `json:"mime_type,omitempty"`
	Data     []byte   `json:"data,omitempty"`
	URL      string   `json:"url,omitempty"`
	Path     string   `json:"path,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// Validate checks that the part contains valid data for its declared type.
func (p Part) Validate() error {
	switch p.Type {
	case PartTypeText:
		if p.Text == "" {
			return fmt.Errorf("%w: text part cannot be empty", ErrInvalidPart)
		}
		return nil
	case PartTypeImage, PartTypeAudio, PartTypeVideo, PartTypeDocument, PartTypeFile:
		if len(p.Data) == 0 && p.URL == "" && p.Path == "" {
			return fmt.Errorf("%w: %s part must provide Data, URL, or Path", ErrInvalidPart, p.Type)
		}
		return nil
	case "":
		return fmt.Errorf("%w: part type cannot be empty", ErrInvalidPart)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedPartType, p.Type)
	}
}

// TextPart creates a new text part.
func TextPart(text string) Part {
	return Part{Type: PartTypeText, Text: text}
}

// ImageFromBytes creates an image part from raw bytes and an explicit MIME type.
func ImageFromBytes(data []byte, mimeType string) Part {
	return Part{Type: PartTypeImage, Data: data, MIMEType: mimeType}
}

// ImageFromURL creates an image part referencing a remote URL or data URI.
func ImageFromURL(url string) Part {
	return Part{Type: PartTypeImage, URL: url}
}

// AudioFromBytes creates an audio part from raw bytes and MIME type.
func AudioFromBytes(data []byte, mimeType string) Part {
	return Part{Type: PartTypeAudio, Data: data, MIMEType: mimeType}
}

// VideoFromBytes creates a video part from raw bytes and MIME type.
func VideoFromBytes(data []byte, mimeType string) Part {
	return Part{Type: PartTypeVideo, Data: data, MIMEType: mimeType}
}

// DocumentFromBytes creates a document part from raw bytes and MIME type.
func DocumentFromBytes(data []byte, mimeType string) Part {
	return Part{Type: PartTypeDocument, Data: data, MIMEType: mimeType}
}

// PartFromURI creates a media part pointing to a URI (e.g. gs://, https://, or Files API URI).
func PartFromURI(partType PartType, uri, mimeType string) Part {
	return Part{
		Type:     partType,
		URL:      uri,
		MIMEType: mimeType,
	}
}
