// Command codjent runs the codjent backend.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/config"
	"github.com/ijaidev/gojent/codjent/internal/httpapi"
	"github.com/ijaidev/gojent/codjent/internal/provider"
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

	store, err := sqlite.New(context.Background(), cfg.DatabasePath())
	if err != nil {
		return err
	}

	registry, err := provider.NewRegistry(
		googleprovider.New(cfg.GoogleProviderConfig()),
	)
	if err != nil {
		_ = store.Close()
		return err
	}

	service, err := app.New(app.Config{
		Store:             store,
		Models:            registry,
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
		Handler:           httpapi.New(service, cfg.AllowedOrigins),
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
	if os.Getenv("GEMINI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		log.Printf("warning: GEMINI_API_KEY or GOOGLE_API_KEY is not set; model runs will fail until configured")
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
