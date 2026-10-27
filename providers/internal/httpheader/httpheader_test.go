package httpheader_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/providers/internal/httpheader"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name   string
		layers []http.Header
		want   http.Header
	}{
		{
			name: "no layers",
			want: nil,
		},
		{
			name:   "empty layers",
			layers: []http.Header{nil, {}},
			want:   nil,
		},
		{
			name:   "canonicalizes names",
			layers: []http.Header{{"x-tenant": {"a"}}},
			want:   http.Header{"X-Tenant": {"a"}},
		},
		{
			name:   "keeps multiple values",
			layers: []http.Header{{"X-Test": {"one", "two"}}},
			want:   http.Header{"X-Test": {"one", "two"}},
		},
		{
			name:   "later layer overrides",
			layers: []http.Header{{"X-Test": {"one"}}, {"x-test": {"two", "three"}}},
			want:   http.Header{"X-Test": {"two", "three"}},
		},
		{
			name:   "skips empty values",
			layers: []http.Header{{"X-Empty": nil}, {"X-Test": {"one"}}},
			want:   http.Header{"X-Test": {"one"}},
		},
		{
			name:   "allows tabs in values",
			layers: []http.Header{{"X-Test": {"a\tb"}}},
			want:   http.Header{"X-Test": {"a\tb"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := httpheader.Merge(tc.layers...)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("headers = %+v, want %+v", got, tc.want)
			}
			for name, want := range tc.want {
				values := got[name]
				if len(values) != len(want) {
					t.Fatalf("%s = %v, want %v", name, values, want)
				}
				for i := range want {
					if values[i] != want[i] {
						t.Fatalf("%s[%d] = %q, want %q", name, i, values[i], want[i])
					}
				}
			}
		})
	}
}

func TestMerge_RejectsInvalidHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
		want    string
	}{
		{
			name:    "empty name",
			headers: http.Header{"": {"value"}},
			want:    "invalid name",
		},
		{
			name:    "space in name",
			headers: http.Header{"Bad Name": {"value"}},
			want:    "invalid name",
		},
		{
			name:    "colon in name",
			headers: http.Header{"X-Test:": {"value"}},
			want:    "invalid name",
		},
		{
			name:    "newline in value",
			headers: http.Header{"X-Test": {"a\nb"}},
			want:    "invalid value",
		},
		{
			name:    "carriage return in value",
			headers: http.Header{"X-Test": {"a\rb"}},
			want:    "invalid value",
		},
		{
			name:    "nul in value",
			headers: http.Header{"X-Test": {"a\x00b"}},
			want:    "invalid value",
		},
		{
			name:    "control byte in value",
			headers: http.Header{"X-Test": {"a\x01b"}},
			want:    "invalid value",
		},
		{
			name:    "case-insensitive duplicate",
			headers: http.Header{"X-Test": {"a"}, "x-test": {"b"}},
			want:    "duplicate name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := httpheader.Merge(tc.headers)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestMerge_ReturnsIndependentCopy(t *testing.T) {
	source := http.Header{"X-Test": {"one"}}
	merged, err := httpheader.Merge(source)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	source["X-Test"][0] = "changed"
	source["X-Other"] = []string{"other"}

	if got := merged.Get("X-Test"); got != "one" {
		t.Fatalf("merged value = %q, want one", got)
	}
	if got := merged.Get("X-Other"); got != "" {
		t.Fatalf("merged gained a header from the source: %q", got)
	}
}
