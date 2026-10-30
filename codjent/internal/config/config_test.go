package config

import (
	"errors"
	"flag"
	"io"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ijaidev/gojent/codjent/internal/app"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CODJENT_ADDR",
		"CODJENT_DATA_DIR",
		"CODJENT_WORKSPACE",
		"CODJENT_PROVIDER",
		"CODJENT_MODEL",
		"CODJENT_GOOGLE_MODELS",
		"CODJENT_MAX_STEPS",
		"CODJENT_ACCESS_MODE",
		"CODJENT_ALLOWED_ORIGINS",
		"CODJENT_FRONTEND_DIR",
		"CODJENT_NO_FRONTEND",
		"XDG_DATA_HOME",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("CODJENT_DATA_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Fatalf("addr = %q", cfg.Addr)
	}
	if cfg.DataDir != dir {
		t.Fatalf("data dir = %q, want %q", cfg.DataDir, dir)
	}
	if cfg.Provider != "" || cfg.Model != "" {
		t.Fatalf("provider/model = %q/%q", cfg.Provider, cfg.Model)
	}
	if cfg.MaxSteps != DefaultMaxSteps || cfg.AccessMode != app.AccessAsk {
		t.Fatalf("steps/access = %d/%q", cfg.MaxSteps, cfg.AccessMode)
	}
	if cfg.DatabasePath() != filepath.Join(dir, "codjent.db") {
		t.Fatalf("database path = %q", cfg.DatabasePath())
	}
}

func TestLoadEnvironment(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CODJENT_ADDR", "127.0.0.1:9999")
	t.Setenv("CODJENT_DATA_DIR", "~/codjent-test")
	t.Setenv("CODJENT_WORKSPACE", "/tmp/workspace")
	t.Setenv("CODJENT_PROVIDER", "google")
	t.Setenv("CODJENT_MODEL", "gemini-test")
	t.Setenv("CODJENT_GOOGLE_MODELS", "gemini-a, gemini-b, gemini-a")
	t.Setenv("CODJENT_MAX_STEPS", "7")
	t.Setenv("CODJENT_ACCESS_MODE", "full")
	t.Setenv("CODJENT_ALLOWED_ORIGINS", "http://localhost:3000,http://example.test")
	t.Setenv("CODJENT_NO_FRONTEND", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9999" || cfg.Workspace != "/tmp/workspace" || cfg.Model != "gemini-test" {
		t.Fatalf("config = %+v", cfg)
	}
	if !cfg.NoFrontend {
		t.Fatal("no frontend = false, want true")
	}
	if cfg.MaxSteps != 7 || cfg.AccessMode != app.AccessFull {
		t.Fatalf("steps/access = %d/%q", cfg.MaxSteps, cfg.AccessMode)
	}
	if len(cfg.GoogleModels) != 2 || cfg.GoogleModels[0] != "gemini-a" || cfg.GoogleModels[1] != "gemini-b" {
		t.Fatalf("google models = %v", cfg.GoogleModels)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("origins = %v", cfg.AllowedOrigins)
	}
	if got := cfg.GoogleProviderConfig().DefaultModel; got != "gemini-test" {
		t.Fatalf("provider default model = %q", got)
	}
}

func TestParseFlagsOverrideEnvironment(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CODJENT_ADDR", "127.0.0.1:1111")
	t.Setenv("CODJENT_DATA_DIR", t.TempDir())
	t.Setenv("CODJENT_MAX_STEPS", "4")
	t.Setenv("CODJENT_ACCESS_MODE", "ask")
	t.Setenv("CODJENT_FRONTEND_DIR", "/tmp/codjent-env-frontend")

	cfg, err := Parse([]string{
		"--addr", "127.0.0.1:2222",
		"--data-dir", "/tmp/codjent-flags",
		"--max-steps", "9",
		"--access-mode", "full",
		"--google-models", "a,b",
		"--allowed-origins", "http://one.test,http://two.test",
		"--frontend-dir", "/tmp/codjent-flags-frontend",
	}, io.Discard)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Addr != "127.0.0.1:2222" || cfg.DataDir != "/tmp/codjent-flags" {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.FrontendDir != "/tmp/codjent-flags-frontend" {
		t.Fatalf("frontend dir = %q", cfg.FrontendDir)
	}
	if cfg.MaxSteps != 9 || cfg.AccessMode != app.AccessFull {
		t.Fatalf("steps/access = %d/%q", cfg.MaxSteps, cfg.AccessMode)
	}
	if len(cfg.GoogleModels) != 2 || len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("lists = %v / %v", cfg.GoogleModels, cfg.AllowedOrigins)
	}
}

func TestParseHelp(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CODJENT_DATA_DIR", t.TempDir())
	_, err := Parse([]string{"--help"}, io.Discard)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v", err)
	}
}

func TestParseNoFrontend(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CODJENT_DATA_DIR", t.TempDir())

	cfg, err := Parse([]string{"--no-frontend"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.NoFrontend {
		t.Fatal("no frontend = false, want true")
	}
}

func TestValidation(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CODJENT_DATA_DIR", t.TempDir())

	if _, err := Parse([]string{"--max-steps", "0"}, io.Discard); err == nil {
		t.Fatal("expected zero max steps error")
	}
	if _, err := Parse([]string{"--access-mode", "root"}, io.Discard); err == nil {
		t.Fatal("expected invalid access mode error")
	}
	if _, err := Parse([]string{"--addr", ""}, io.Discard); err == nil {
		t.Fatal("expected empty addr error")
	}
	if _, err := Parse([]string{"--no-frontend", "--frontend-dir", "/tmp/frontend"}, io.Discard); err == nil {
		t.Fatal("expected no-frontend/frontend-dir conflict error")
	}
}

func TestDataDirXDG(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("XDG is a Unix convention")
	}
	clearConfigEnv(t)
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if dir != "/tmp/xdg-data/codjent" {
		t.Fatalf("dir = %q", dir)
	}
}

func TestDataDirOverride(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CODJENT_DATA_DIR", "/tmp/codjent-override")
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if dir != "/tmp/codjent-override" {
		t.Fatalf("dir = %q", dir)
	}
}
