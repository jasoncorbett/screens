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
	"time"

	"github.com/jasoncorbett/screens/api"
	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/config"
	"github.com/jasoncorbett/screens/internal/db"
	"github.com/jasoncorbett/screens/internal/logging"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/version"
	"github.com/jasoncorbett/screens/internal/widget"

	_ "github.com/jasoncorbett/screens/internal/widget/text" // registers the placeholder text widget at startup

	"github.com/jasoncorbett/screens/views"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	logging.Setup(cfg.Log.Level, cfg.Log.DevMode)

	slog.Info("starting screens", "version", version.Version)

	sqlDB, err := db.Open(db.DBConfig{
		Path:            cfg.DB.Path,
		MaxOpenConns:    cfg.DB.MaxOpenConns,
		MaxIdleConns:    cfg.DB.MaxIdleConns,
		ConnMaxLifetime: cfg.DB.ConnMaxLifetime,
	})
	if err != nil {
		log.Fatalf("database open: %v", err)
	}

	if err := db.Migrate(context.Background(), sqlDB); err != nil {
		db.Close(sqlDB)
		log.Fatalf("database migration: %v", err)
	}

	api.RegisterHealthCheck(func() api.HealthCheck {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := sqlDB.PingContext(ctx)
		status := api.Status{Ok: true}
		if err != nil {
			status = api.Status{Ok: false, Message: "error: " + err.Error()}
		}
		return api.HealthCheck{Name: "database", Status: status}
	})

	authSvc := auth.NewService(sqlDB, auth.Config{
		AdminEmail:             cfg.Auth.AdminEmail,
		SessionDuration:        cfg.Auth.SessionDuration,
		CookieName:             cfg.Auth.CookieName,
		SecureCookie:           !cfg.Log.DevMode,
		DeviceCookieName:       cfg.Auth.DeviceCookieName,
		DeviceLastSeenInterval: cfg.Auth.DeviceLastSeenInterval,
		DeviceLandingURL:       cfg.Auth.DeviceLandingURL,
	})

	googleClient := auth.NewGoogleClient(
		cfg.Auth.GoogleClientID,
		cfg.Auth.GoogleClientSecret,
		cfg.Auth.GoogleRedirectURL,
	)

	themesSvc := themes.NewService(sqlDB, themes.Config{
		DefaultName: cfg.Theme.DefaultName,
	})
	if err := themesSvc.EnsureDefault(context.Background()); err != nil {
		db.Close(sqlDB)
		log.Fatalf("seed default theme: %v", err)
	}

	mux := http.NewServeMux()
	api.AddRoutes(mux)

	views.AddRoutes(mux, &views.Deps{
		Auth:             authSvc,
		Google:           googleClient,
		ClientID:         cfg.Auth.GoogleClientID,
		CookieName:       cfg.Auth.CookieName,
		DeviceCookieName: cfg.Auth.DeviceCookieName,
		DeviceLandingURL: cfg.Auth.DeviceLandingURL,
		SecureCookie:     !cfg.Log.DevMode,
		Themes:           themesSvc,
		Widgets:          widget.Default(),
	})

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

	if err := db.Close(sqlDB); err != nil {
		slog.Error("database close failed", "err", err)
	}

	slog.Info("shutdown complete")
}
