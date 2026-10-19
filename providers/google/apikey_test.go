package google

import (
	"testing"

	"google.golang.org/genai"
)

func TestResolveAPIKey_Precedence(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-gemini")
	t.Setenv("GOOGLE_API_KEY", "env-google")

	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "provider APIKey wins over ClientConfig and environment",
			cfg: Config{
				APIKey:       "explicit",
				ClientConfig: &genai.ClientConfig{APIKey: "client"},
			},
			want: "explicit",
		},
		{
			name: "ClientConfig APIKey wins over environment",
			cfg:  Config{ClientConfig: &genai.ClientConfig{APIKey: "client"}},
			want: "client",
		},
		{
			name: "GEMINI_API_KEY wins over GOOGLE_API_KEY",
			cfg:  Config{},
			want: "env-gemini",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAPIKey(tt.cfg); got != tt.want {
				t.Errorf("resolveAPIKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveAPIKey_Fallbacks(t *testing.T) {
	t.Run("GOOGLE_API_KEY fallback", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("GOOGLE_API_KEY", "env-google")

		if got := resolveAPIKey(Config{}); got != "env-google" {
			t.Errorf("resolveAPIKey() = %q, want %q", got, "env-google")
		}
	})

	t.Run("no key configured", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("GOOGLE_API_KEY", "")

		if got := resolveAPIKey(Config{}); got != "" {
			t.Errorf("resolveAPIKey() = %q, want empty", got)
		}
	})
}
