package provider

import (
	"errors"
	"fmt"
	"net/textproto"
	"regexp"
	"strings"

	"github.com/ijaidev/gojent/core/session"
)

const (
	maxHeaderCount       = 32
	maxHeaderNameLength  = 256
	maxHeaderValueLength = 8192
)

// Header is one HTTP header applied to provider requests. Static headers are
// sent verbatim. A dynamic header's value is a template resolved from the run
// context before every model call.
type Header struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Dynamic bool   `json:"dynamic"`
}

// headerPlaceholders is the set of supported dynamic template tokens. They are
// resolved from the session the agent runner installs in the request context.
var headerPlaceholders = map[string]struct{}{
	"session.id":          {},
	"session.resource_id": {},
}

var headerPlaceholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.]+)\s*\}\}`)

// normalizeHeaders trims, canonicalizes, and validates a header list. It
// rejects duplicate names case-insensitively and unknown dynamic placeholders
// so configuration errors surface in settings instead of during a run.
func normalizeHeaders(format Format, headers []Header) ([]Header, error) {
	if len(headers) > maxHeaderCount {
		return nil, fmt.Errorf("%w: at most %d headers are allowed", ErrInvalidConfig, maxHeaderCount)
	}

	normalized := make([]Header, 0, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for index, header := range headers {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: header %d has no name", ErrInvalidConfig, index+1)
		}
		if len(name) > maxHeaderNameLength {
			return nil, fmt.Errorf("%w: header %d name is too long", ErrInvalidConfig, index+1)
		}
		if !validHeaderName(name) {
			return nil, fmt.Errorf("%w: header %q has an invalid name", ErrInvalidConfig, name)
		}

		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if _, duplicate := seen[canonical]; duplicate {
			return nil, fmt.Errorf("%w: duplicate header %q (header names are case-insensitive)", ErrInvalidConfig, canonical)
		}
		seen[canonical] = struct{}{}

		// The Google SDK applies x-goog-api-key after request headers, so a
		// configured value would be silently ignored. Reject it instead.
		if format == FormatGoogle && canonical == "X-Goog-Api-Key" {
			return nil, fmt.Errorf("%w: the google format cannot override x-goog-api-key", ErrInvalidConfig)
		}

		value := strings.TrimSpace(header.Value)
		if len(value) > maxHeaderValueLength {
			return nil, fmt.Errorf("%w: header %q value is too long", ErrInvalidConfig, canonical)
		}
		if !validHeaderValue(value) {
			return nil, fmt.Errorf("%w: header %q has an invalid value", ErrInvalidConfig, canonical)
		}
		if header.Dynamic {
			if err := validateHeaderTemplate(canonical, value); err != nil {
				return nil, err
			}
		}

		normalized = append(normalized, Header{
			Name:    canonical,
			Value:   value,
			Dynamic: header.Dynamic,
		})
	}
	return normalized, nil
}

// ResolveHeaderValue expands a dynamic header template for one session.
func ResolveHeaderValue(template string, sess session.Session) (string, error) {
	if err := templateError(template); err != nil {
		return "", err
	}
	if !headerPlaceholderPattern.MatchString(template) {
		return template, nil
	}
	if sess.ID == "" {
		return "", errors.New("session is unavailable")
	}

	var resolveErr error
	resolved := headerPlaceholderPattern.ReplaceAllStringFunc(template, func(match string) string {
		if resolveErr != nil {
			return match
		}
		parts := headerPlaceholderPattern.FindStringSubmatch(match)
		switch parts[1] {
		case "session.id":
			return sess.ID
		case "session.resource_id":
			return sess.ResourceID
		default:
			resolveErr = fmt.Errorf("unknown placeholder %q", parts[1])
			return match
		}
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	if !validHeaderValue(resolved) {
		return "", errors.New("resolved value contains invalid characters")
	}
	return resolved, nil
}

func validateHeaderTemplate(name, value string) error {
	if err := templateError(value); err != nil {
		return fmt.Errorf("%w: header %q: %w", ErrInvalidConfig, name, err)
	}
	return nil
}

func templateError(value string) error {
	remaining := headerPlaceholderPattern.ReplaceAllString(value, "")
	if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
		return errors.New("invalid dynamic template")
	}
	for _, match := range headerPlaceholderPattern.FindAllStringSubmatch(value, -1) {
		if _, ok := headerPlaceholders[match[1]]; !ok {
			return fmt.Errorf("unknown placeholder %q", match[1])
		}
	}
	return nil
}

// validHeaderName reports whether name is an RFC 7230 token.
func validHeaderName(name string) bool {
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

// validHeaderValue rejects control bytes that cannot appear in a header field
// value. Horizontal tabs are allowed; CR, LF, and NUL are not.
func validHeaderValue(value string) bool {
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
