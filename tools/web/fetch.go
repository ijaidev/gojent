// Package web provides readymade tools for fetching web content and searching the web.
package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ijaidev/gojent/core/tool"
)

const (
	defaultFetchTimeout  = 15 * time.Second
	defaultMaxFetchBytes = 2 * 1024 * 1024 // 2MB
)

// FetchConfig configures the web_fetch tool.
type FetchConfig struct {
	Client        *http.Client
	MaxBytes      int64
	DefaultHeader map[string]string
}

// FetchInput defines the input parameters for fetching a URL.
type FetchInput struct {
	URL            string            `json:"url" description:"The HTTP or HTTPS URL to fetch"`
	Headers        map[string]string `json:"headers,omitempty" description:"Optional HTTP headers to send with the request"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" description:"Optional timeout in seconds (default 15)"`
	ExtractText    bool              `json:"extract_text,omitempty" description:"If true, strips HTML tags to return clean readable text"`
}

// FetchOutput represents the fetched web content.
type FetchOutput struct {
	URL         string `json:"url"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
}

var (
	scriptTagRegex  = regexp.MustCompile(`(?is)<script.*?>.*?</script>`)
	styleTagRegex   = regexp.MustCompile(`(?is)<style.*?>.*?</style>`)
	htmlTagRegex    = regexp.MustCompile(`<[^>]+>`)
	whitespaceRegex = regexp.MustCompile(`\n{3,}`)
)

func cleanHTML(html string) string {
	noScripts := scriptTagRegex.ReplaceAllString(html, "")
	noStyles := styleTagRegex.ReplaceAllString(noScripts, "")
	textOnly := htmlTagRegex.ReplaceAllString(noStyles, " ")
	lines := strings.Split(textOnly, "\n")
	var cleaned []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	result := strings.Join(cleaned, "\n")
	return whitespaceRegex.ReplaceAllString(result, "\n\n")
}

// NewWebFetchTool creates a Tool for fetching content from web pages.
func NewWebFetchTool(cfg ...FetchConfig) (tool.Tool, error) {
	var c FetchConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	client := c.Client
	if client == nil {
		client = &http.Client{}
	}
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxFetchBytes
	}

	return tool.New(tool.Config[FetchInput, FetchOutput]{
		ID:          "web_fetch",
		Description: "Fetch content from a URL via HTTP GET with optional HTML text extraction.",
		Execute: func(ctx context.Context, in FetchInput) (FetchOutput, error) {
			if in.URL == "" {
				return FetchOutput{}, fmt.Errorf("web_fetch: url cannot be empty")
			}

			timeout := defaultFetchTimeout
			if in.TimeoutSeconds > 0 {
				timeout = time.Duration(in.TimeoutSeconds) * time.Second
			}

			reqCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, in.URL, http.NoBody)
			if err != nil {
				return FetchOutput{}, fmt.Errorf("web_fetch: invalid request: %w", err)
			}

			req.Header.Set("User-Agent", "Gojent-Bot/1.0 (+https://github.com/ijaidev/gojent)")
			for k, v := range c.DefaultHeader {
				req.Header.Set(k, v)
			}
			for k, v := range in.Headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			if err != nil {
				return FetchOutput{}, fmt.Errorf("web_fetch: request failed: %w", err)
			}
			defer func() {
				_ = resp.Body.Close()
			}()

			bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
			if err != nil {
				return FetchOutput{}, fmt.Errorf("web_fetch: failed reading response: %w", err)
			}

			contentType := resp.Header.Get("Content-Type")
			rawContent := string(bodyBytes)
			content := rawContent

			if in.ExtractText || strings.Contains(strings.ToLower(contentType), "text/html") {
				content = cleanHTML(rawContent)
			}

			return FetchOutput{
				URL:         resp.Request.URL.String(),
				StatusCode:  resp.StatusCode,
				ContentType: contentType,
				Content:     content,
			}, nil
		},
	})
}
