package google

import "strings"

func isSupportedImageMIME(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/heic", "image/heif":
		return true
	default:
		return false
	}
}

func isSupportedAudioMIME(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "audio/wav", "audio/mp3", "audio/mpeg", "audio/aac", "audio/flac", "audio/ogg", "audio/aiff", "audio/x-m4a":
		return true
	default:
		return false
	}
}

func isSupportedVideoMIME(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "video/mp4", "video/mpeg", "video/mov", "video/quicktime", "video/avi", "video/x-flv", "video/webm", "video/wmv", "video/3gpp":
		return true
	default:
		return false
	}
}

func isSupportedDocMIME(m string) bool {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "application/pdf", "text/plain", "text/html", "text/css", "text/csv", "text/markdown", "application/rtf", "text/xml", "application/json":
		return true
	default:
		return false
	}
}
