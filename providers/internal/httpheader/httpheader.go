// Package httpheader normalizes and merges HTTP headers for provider
// adapters. It validates names and values before they reach an SDK so a bad
// header fails the call instead of surfacing as an opaque transport error.
package httpheader

import (
	"fmt"
	"net/http"
	"net/textproto"
)

// Merge combines header layers in order. Later layers replace earlier values
// for the same canonical header name. Nil layers are ignored. The result is a
// fresh map; it returns nil when no layer has any headers.
//
// Names are canonicalized and validated. Duplicate names that differ only in
// case within one layer are rejected because Go map iteration would otherwise
// make the winner unpredictable.
func Merge(layers ...http.Header) (http.Header, error) {
	var merged http.Header
	for _, layer := range layers {
		if len(layer) == 0 {
			continue
		}
		seen := make(map[string]struct{}, len(layer))
		for name, values := range layer {
			canonical, err := canonicalName(name)
			if err != nil {
				return nil, err
			}
			if _, duplicate := seen[canonical]; duplicate {
				return nil, fmt.Errorf("header %q: duplicate name (header names are case-insensitive)", canonical)
			}
			seen[canonical] = struct{}{}
			if len(values) == 0 {
				continue
			}
			for _, value := range values {
				if !validValue(value) {
					return nil, fmt.Errorf("header %q: invalid value", canonical)
				}
			}
			if merged == nil {
				merged = make(http.Header, len(layer))
			}
			merged[canonical] = append([]string(nil), values...)
		}
	}
	return merged, nil
}

// canonicalName validates a header field name and returns its canonical form.
func canonicalName(name string) (string, error) {
	if name == "" || !validName(name) {
		return "", fmt.Errorf("header %q: invalid name", name)
	}
	return textproto.CanonicalMIMEHeaderKey(name), nil
}

// validName reports whether name is an RFC 7230 token.
func validName(name string) bool {
	for i := 0; i < len(name); i++ {
		switch c := name[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' ||
			c == '\'' || c == '*' || c == '+' || c == '-' || c == '.' ||
			c == '^' || c == '_' || c == '`' || c == '|' || c == '~':
		default:
			return false
		}
	}
	return true
}

// validValue rejects control bytes that cannot appear in a header field
// value. Horizontal tabs are allowed; CR, LF, and NUL are not.
func validValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\r' || c == '\n' || c == 0 {
			return false
		}
		if c < 0x20 && c != '\t' {
			return false
		}
		if c == 0x7f {
			return false
		}
	}
	return true
}
