package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shop-r1/mss-shop/services/storefront-api/internal/config"
	"github.com/shop-r1/mss-shop/services/storefront-api/internal/httpapi"
)

func main() {
	defaultConfig := os.Getenv("R1SHOP_STOREFRONT_CONFIG")
	if defaultConfig == "" {
		defaultConfig = "config/examples/storefront-tenants.json"
	}
	configPath := flag.String("config", defaultConfig, "path to strict storefront tenant configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("storefront configuration is invalid", "error", err)
		os.Exit(1)
	}
	handler, err := httpapi.New(cfg)
	if err != nil {
		slog.Error("storefront directory is invalid", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownContext.Done()
		context, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(context); err != nil {
			slog.Error("storefront shutdown failed", "error", err)
		}
	}()

	slog.Info("storefront API listening", "address", cfg.ListenAddress)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("storefront API stopped", "error", err)
		os.Exit(1)
	}
}
