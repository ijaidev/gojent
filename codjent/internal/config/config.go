// Package config resolves codjent runtime configuration from environment
// variables and command-line flags.
package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ijaidev/gojent/codjent/internal/app"
	googleprovider "github.com/ijaidev/gojent/codjent/internal/provider/google"
)

const (
	// DefaultAddr keeps the agent on loopback: it can execute commands.
	DefaultAddr = "127.0.0.1:8787"
	// DefaultProvider is the only provider implemented today.
	DefaultProvider = "google"
	// DefaultMaxSteps bounds the ReAct loop.
	DefaultMaxSteps = 20
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Addr           string
	DataDir        string
	Workspace      string
	Provider       string
	Model          string
	GoogleModels   []string
	MaxSteps       int
	AccessMode     app.AccessMode
	AllowedOrigins []string
	FrontendDir    string
}

// Load reads configuration from the environment and applies defaults.
func Load() (Config, error) {
	dataDir, err := DataDir()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Addr:           envOr("CODJENT_ADDR", DefaultAddr),
		DataDir:        dataDir,
		Workspace:      strings.TrimSpace(os.Getenv("CODJENT_WORKSPACE")),
		Provider:       envOr("CODJENT_PROVIDER", DefaultProvider),
		Model:          strings.TrimSpace(os.Getenv("CODJENT_MODEL")),
		GoogleModels:   splitCSV(os.Getenv("CODJENT_GOOGLE_MODELS")),
		MaxSteps:       DefaultMaxSteps,
		AccessMode:     app.AccessMode(envOr("CODJENT_ACCESS_MODE", string(app.DefaultAccessMode))),
		AllowedOrigins: splitCSV(envOr("CODJENT_ALLOWED_ORIGINS", defaultAllowedOrigins())),
		FrontendDir:    strings.TrimSpace(os.Getenv("CODJENT_FRONTEND_DIR")),
	}

	if raw := strings.TrimSpace(os.Getenv("CODJENT_MAX_STEPS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: CODJENT_MAX_STEPS: %w", err)
		}
		cfg.MaxSteps = n
	}
	if len(cfg.GoogleModels) == 0 {
		cfg.GoogleModels = nil
	}
	return cfg, cfg.Validate()
}

// Parse loads environment defaults, then applies flags.
func Parse(args []string, output io.Writer) (Config, error) {
	cfg, err := Load()
	if err != nil {
		return Config{}, err
	}

	models := strings.Join(cfg.GoogleModels, ",")
	origins := strings.Join(cfg.AllowedOrigins, ",")
	accessMode := string(cfg.AccessMode)

	fs := flag.NewFlagSet("codjent", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "directory for the SQLite database")
	fs.StringVar(&cfg.Workspace, "workspace", cfg.Workspace, "agent workspace (defaults to the current directory)")
	fs.StringVar(&cfg.Provider, "provider", cfg.Provider, "default model provider")
	fs.StringVar(&cfg.Model, "model", cfg.Model, "default model ID")
	fs.StringVar(&models, "google-models", models, "comma-separated Google model IDs")
	fs.IntVar(&cfg.MaxSteps, "max-steps", cfg.MaxSteps, "maximum ReAct steps per run")
	fs.StringVar(&accessMode, "access-mode", accessMode, "default access mode: ask or full")
	fs.StringVar(&origins, "allowed-origins", origins, "comma-separated browser origins allowed by CORS")
	fs.StringVar(&cfg.FrontendDir, "frontend-dir", cfg.FrontendDir, "directory containing the built frontend (optional)")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	cfg.GoogleModels = splitCSV(models)
	cfg.AllowedOrigins = splitCSV(origins)
	cfg.AccessMode = app.AccessMode(strings.TrimSpace(accessMode))
	return cfg, cfg.Validate()
}

// Validate checks values that can be checked without touching the filesystem.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return fmt.Errorf("config: addr is required")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("config: data directory is required")
	}
	if strings.TrimSpace(c.Provider) == "" {
		return fmt.Errorf("config: provider is required")
	}
	if c.MaxSteps <= 0 {
		return fmt.Errorf("config: max steps must be positive")
	}
	if !c.AccessMode.Valid() {
		return fmt.Errorf("config: access mode %q is invalid (want ask or full)", c.AccessMode)
	}
	return nil
}

// DatabasePath returns the SQLite database path inside DataDir.
func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "codjent.db")
}

// DataDir resolves an OS-appropriate application data directory. The
// CODJENT_DATA_DIR environment variable overrides the platform default.
func DataDir() (string, error) {
	if raw := strings.TrimSpace(os.Getenv("CODJENT_DATA_DIR")); raw != "" {
		return expandHome(raw)
	}

	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: resolve home directory: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "codjent"), nil
	case "windows":
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "codjent"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: resolve home directory: %w", err)
		}
		return filepath.Join(home, "AppData", "Local", "codjent"), nil
	default:
		if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
			return filepath.Join(xdg, "codjent"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: resolve home directory: %w", err)
		}
		return filepath.Join(home, ".local", "share", "codjent"), nil
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(raw string) []string {
	var values []string
	seen := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		values = append(values, part)
	}
	return values
}

func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

func defaultAllowedOrigins() string {
	return "http://localhost:3000,http://127.0.0.1:3000"
}

// GoogleProviderConfig adapts codjent config to the Google provider adapter.
func (c Config) GoogleProviderConfig() googleprovider.Config {
	return googleprovider.Config{
		Models:       c.GoogleModels,
		DefaultModel: c.Model,
	}
}
