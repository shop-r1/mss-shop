package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/config"
	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/importer"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	settings, err := config.Load(os.LookupEnv)
	if err == nil {
		err = importer.Run(ctx, settings, os.Stdout)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "legacy import failed:", err)
		os.Exit(1)
	}
}
