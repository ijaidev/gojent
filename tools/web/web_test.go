package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/tools/web"
)

func TestWebFetchTool_HTMLCleaning(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`
			<!DOCTYPE html>
			<html>
			<head>
				<title>Test Page</title>
				<style>body { font-size: 16px; }</style>
				<script>console.log("ignore me");</script>
			</head>
			<body>
				<h1>Welcome to Gojent</h1>
				<p>Autonomous AI agents in Go.</p>
			</body>
			</html>
		`))
	}))
	defer ts.Close()

	fetchTool, err := web.NewWebFetchTool()
	if err != nil {
		t.Fatalf("failed to create web_fetch tool: %v", err)
	}

	inputBytes, _ := json.Marshal(web.FetchInput{
		URL: ts.URL,
	})

	resAny, err := fetchTool.Execute(context.Background(), inputBytes)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	res := resAny.Data.(web.FetchOutput)
	if res.StatusCode != 200 {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	if strings.Contains(res.Content, "console.log") {
		t.Errorf("expected script tags to be stripped, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "font-size") {
		t.Errorf("expected style tags to be stripped, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Welcome to Gojent") {
		t.Errorf("expected page title in content, got: %s", res.Content)
	}
}

func TestWebSearchTool_Mock(t *testing.T) {
	mock := &web.MockSearchProvider{
		Results: []web.SearchResult{
			{Title: "Gojent Docs", URL: "https://gojent.dev", Snippet: "Build AI agents in Go"},
			{Title: "Go Docs", URL: "https://go.dev", Snippet: "The Go Programming Language"},
		},
	}

	searchTool, err := web.NewWebSearchTool(mock)
	if err != nil {
		t.Fatalf("failed to create search tool: %v", err)
	}

	inputBytes, _ := json.Marshal(web.SearchInput{
		Query:      "gojent agent",
		MaxResults: 1,
	})

	resAny, err := searchTool.Execute(context.Background(), inputBytes)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	res := resAny.Data.(web.SearchOutput)
	if res.Total != 1 {
		t.Errorf("expected 1 result, got %d", res.Total)
	}
	if res.Results[0].Title != "Gojent Docs" {
		t.Errorf("unexpected first result: %+v", res.Results[0])
	}
}
