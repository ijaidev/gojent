// Command codjent runs the codjent backend.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ijaidev/gojent/codjent/frontend"
	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/config"
	"github.com/ijaidev/gojent/codjent/internal/httpapi"
	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/codjent/internal/provider/catalog"
	googleprovider "github.com/ijaidev/gojent/codjent/internal/provider/google"
	"github.com/ijaidev/gojent/codjent/internal/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Parse(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}

	frontendFS, frontendLabel, err := resolveFrontend(cfg)
	if err != nil {
		return err
	}

	store, err := sqlite.New(context.Background(), cfg.DatabasePath())
	if err != nil {
		return err
	}

	providerCatalog, err := catalog.New(context.Background(), store)
	if err != nil {
		_ = store.Close()
		return err
	}
	if err := seedGoogleProvider(context.Background(), providerCatalog, store, cfg); err != nil {
		_ = store.Close()
		return err
	}

	service, err := app.New(app.Config{
		Store:             store,
		Models:            providerCatalog,
		Workspace:         cfg.Workspace,
		DefaultProvider:   cfg.Provider,
		DefaultModel:      cfg.Model,
		DefaultAccessMode: cfg.AccessMode,
		MaxSteps:          cfg.MaxSteps,
	})
	if err != nil {
		_ = store.Close()
		return err
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.NewWithCatalog(service, providerCatalog, cfg.AllowedOrigins, frontendFS),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	log.Printf("codjent listening on http://%s", cfg.Addr)
	log.Printf("database: %s", cfg.DatabasePath())
	log.Printf("workspace: %s", service.Workspace())
	log.Printf("frontend: %s", frontendLabel)
	if len(providerCatalog.Configs()) == 0 {
		log.Printf("providers: none configured; open /settings to add one")
	}

	select {
	case <-ctx.Done():
	case serveErr := <-errCh:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = service.Close(closeCtx)
			cancel()
			return serveErr
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := service.Close(shutdownCtx); err != nil {
		log.Printf("close service: %v", err)
	}
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}

// resolveFrontend selects the UI source: an explicit directory, the embedded
// production build, or no UI at all.
func resolveFrontend(cfg config.Config) (fs.FS, string, error) {
	if cfg.NoFrontend {
		return nil, "API only (--no-frontend)", nil
	}
	if dir := strings.TrimSpace(cfg.FrontendDir); dir != "" {
		index := filepath.Join(dir, "index.html")
		info, err := os.Stat(index)
		if err != nil {
			return nil, "", fmt.Errorf("frontend: %q does not contain index.html: %w", dir, err)
		}
		if info.IsDir() {
			return nil, "", fmt.Errorf("frontend: %q is a directory", index)
		}
		return os.DirFS(dir), dir, nil
	}

	assets, err := fs.Sub(frontend.Assets, "out")
	if err != nil {
		return nil, "", fmt.Errorf("frontend: embedded assets: %w", err)
	}
	info, err := fs.Stat(assets, "index.html")
	if err != nil {
		return nil, "", fmt.Errorf("frontend: embedded index.html: %w", err)
	}
	if info.IsDir() {
		return nil, "", fmt.Errorf("frontend: embedded index.html is a directory")
	}
	return assets, "embedded (use --frontend-dir to override)", nil
}

// seedGoogleProvider preserves the former static Google provider on first run.
// It only acts when no providers are configured and either Google settings are
// present or existing threads still reference the old "google" provider. Fresh
// installs can therefore open settings with no provider.
func seedGoogleProvider(ctx context.Context, cat *catalog.Catalog, store *sqlite.Store, cfg config.Config) error {
	if len(cat.Configs()) > 0 {
		return nil
	}

	models := slices.Clone(cfg.GoogleModels)
	if model := strings.TrimSpace(cfg.Model); model != "" && !slices.Contains(models, model) {
		models = append(models, model)
	}
	hasKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" ||
		strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != ""
	if len(models) == 0 && !hasKey {
		count, err := store.CountThreadsByProvider(ctx, googleprovider.ID)
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
	}
	if len(models) == 0 {
		models = []string{googleprovider.DefaultModel}
	}

	infos := make([]provider.ModelInfo, len(models))
	for index, id := range models {
		infos[index] = provider.ModelInfo{ID: id, Name: id}
	}
	_, err := cat.Seed(ctx, googleprovider.ID, provider.ConfigInput{
		Name:         "Google Gemini",
		Format:       provider.FormatGoogle,
		DefaultModel: cfg.Model,
		Models:       infos,
	})
	return err
}
