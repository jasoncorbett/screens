package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jasoncorbett/screens/api"
	"github.com/jasoncorbett/screens/internal/config"
	"github.com/jasoncorbett/screens/internal/logging"
	"github.com/jasoncorbett/screens/internal/version"

	"github.com/jasoncorbett/screens/views"

)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	logging.Setup(cfg.Log.Level, cfg.Log.DevMode)

	slog.Info("starting screens", "version", version.Version)

	mux := http.NewServeMux()
	api.AddRoutes(mux)

	views.AddRoutes(mux)
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))


	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port),
		Handler:      mux,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
	slog.Info("shutdown complete")
}
