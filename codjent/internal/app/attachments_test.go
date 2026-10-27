package app

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ijaidev/gojent/core/llm"
)

var (
	testPNG = append(
		[]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		make([]byte, 32)...,
	)
	testJPEG = append(
		[]byte{0xff, 0xd8, 0xff, 0xe0},
		make([]byte, 32)...,
	)
	testWebP = append(
		[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
		make([]byte, 32)...,
	)
)

func pngOfSize(size int) []byte {
	data := make([]byte, size)
	copy(data, testPNG)
	return data
}

func TestBuildImagePartsAcceptsSupportedImages(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		wantMIME string
		data     []byte
	}{
		{name: "png", mimeType: "image/png", wantMIME: "image/png", data: testPNG},
		{name: "jpeg", mimeType: "image/jpeg", wantMIME: "image/jpeg", data: testJPEG},
		{name: "jpeg alias", mimeType: "image/jpg", wantMIME: "image/jpeg", data: testJPEG},
		{name: "webp", mimeType: "image/webp", wantMIME: "image/webp", data: testWebP},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parts, total, err := buildImageParts([]AttachmentInput{{
				Filename: "screen.png",
				MIMEType: test.mimeType,
				Data:     test.data,
			}})
			if err != nil {
				t.Fatalf("buildImageParts: %v", err)
			}
			if total != len(test.data) {
				t.Fatalf("total = %d, want %d", total, len(test.data))
			}
			if len(parts) != 1 {
				t.Fatalf("parts = %+v", parts)
			}
			file, ok := parts[0].(*llm.FilePart)
			if !ok {
				t.Fatalf("part type = %T, want *llm.FilePart", parts[0])
			}
			if file.MIMEType != test.wantMIME {
				t.Fatalf("MIME type = %q, want %q", file.MIMEType, test.wantMIME)
			}
			if file.Filename != "screen.png" {
				t.Fatalf("filename = %q", file.Filename)
			}
		})
	}
}

func TestBuildImagePartsRejectsInvalidAttachments(t *testing.T) {
	fiveImages := make([]AttachmentInput, 0, maxImageAttachments+1)
	for range maxImageAttachments + 1 {
		fiveImages = append(fiveImages, AttachmentInput{
			MIMEType: "image/png",
			Data:     testPNG,
		})
	}

	tests := []struct {
		name  string
		parts []AttachmentInput
	}{
		{
			name:  "too many images",
			parts: fiveImages,
		},
		{
			name: "empty data",
			parts: []AttachmentInput{{
				MIMEType: "image/png",
			}},
		},
		{
			name: "missing MIME type",
			parts: []AttachmentInput{{
				Data: testPNG,
			}},
		},
		{
			name: "declared MIME mismatch",
			parts: []AttachmentInput{{
				MIMEType: "image/jpeg",
				Data:     testPNG,
			}},
		},
		{
			name: "unsupported GIF",
			parts: []AttachmentInput{{
				MIMEType: "image/gif",
				Data:     append([]byte("GIF89a"), make([]byte, 32)...),
			}},
		},
		{
			name: "oversized image",
			parts: []AttachmentInput{{
				MIMEType: "image/png",
				Data:     pngOfSize(maxImageBytes + 1),
			}},
		},
		{
			name: "oversized message",
			parts: []AttachmentInput{
				{MIMEType: "image/png", Data: pngOfSize(maxImageBytes)},
				{MIMEType: "image/png", Data: pngOfSize(maxImageBytes)},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := buildImageParts(test.parts); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestAttachmentFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		mimeType string
		want     string
	}{
		{
			name:     "windows path",
			filename: `C:\Users\jaidev\screen.png`,
			mimeType: "image/png",
			want:     "screen.png",
		},
		{
			name:     "control characters",
			filename: "screen\x00\n.png",
			mimeType: "image/png",
			want:     "screen.png",
		},
		{
			name:     "empty name",
			mimeType: "image/webp",
			want:     "image.webp",
		},
		{
			name:     "long name",
			filename: strings.Repeat("a", maxAttachmentNameLength+20) + ".png",
			mimeType: "image/png",
			want:     strings.Repeat("a", maxAttachmentNameLength),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := attachmentFilename(test.filename, test.mimeType)
			if got != test.want {
				t.Fatalf("attachmentFilename = %q, want %q", got, test.want)
			}
			if utf8.RuneCountInString(got) > maxAttachmentNameLength {
				t.Fatalf("filename has %d runes", utf8.RuneCountInString(got))
			}
		})
	}
}

func TestMessageTitle(t *testing.T) {
	named := llm.FileFromBytes(testPNG, "image/png")
	named.Filename = "screen.png"
	unnamed := llm.FileFromBytes(testPNG, "image/png")

	tests := []struct {
		name    string
		content string
		images  []llm.Part
		want    string
	}{
		{name: "text", content: "fix this", images: []llm.Part{named}, want: "fix this"},
		{name: "one named image", images: []llm.Part{named}, want: "screen.png"},
		{name: "one unnamed image", images: []llm.Part{unnamed}, want: "1 image"},
		{name: "many images", images: []llm.Part{named, unnamed}, want: "2 images"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := messageTitle(test.content, test.images); got != test.want {
				t.Fatalf("messageTitle = %q, want %q", got, test.want)
			}
		})
	}
}
