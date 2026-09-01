package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	postgresdriver "github.com/shop-r1/mss-shop/services/reconciler/internal/driver/postgres"
	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const (
	environmentDatabaseDSN            = "R1SHOP_RECONCILER_DATABASE_DSN"
	environmentTenantMigratorPassword = "R1SHOP_TENANT_MIGRATOR_PASSWORD"
	environmentTenantRuntimePassword  = "R1SHOP_TENANT_RUNTIME_PASSWORD"
	environmentMallMigratorPassword   = "R1SHOP_MALL_MIGRATOR_PASSWORD"
	environmentMallRuntimePassword    = "R1SHOP_MALL_RUNTIME_PASSWORD"
	environmentImportReceiptSHA256    = "R1SHOP_IMPORT_RECEIPT_SHA256"
)

type options struct {
	config      stage.Config
	credentials postgresdriver.Credentials
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("development reconciliation failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	options, err := parseOptions(arguments, os.Getenv)
	if err != nil {
		return err
	}
	if err := options.config.Validate(); err != nil {
		return err
	}
	if _, err := postgresdriver.PreflightStageDatabase(
		ctx,
		options.config.DatabaseDSN,
		options.config.ImportReceiptSHA256,
	); err != nil {
		return err
	}

	plan, err := postgresdriver.BuildPlan(options.config, options.credentials)
	if err != nil {
		return err
	}
	postgresReconciler, err := postgresdriver.Open(ctx, options.config.DatabaseDSN)
	if err != nil {
		return err
	}
	defer postgresReconciler.Close()
	if err := postgresReconciler.Apply(ctx, plan); err != nil {
		return err
	}
	summary := plan.Summary()
	slog.Info(
		"mss-shop-dev reconciliation completed",
		"namespace", stage.Namespace,
		"databaseHost", stage.DatabaseHost,
		"databaseName", stage.DatabaseName,
		"sqlBatches", summary.Batches,
		"sqlStatements", summary.Statements,
		"views", summary.Views,
		"snapshots", summary.Snapshots,
	)
	return nil
}

func parseOptions(arguments []string, getenv func(string) string) (options, error) {
	if getenv == nil {
		return options{}, errors.New("environment reader is required")
	}
	flags := flag.NewFlagSet("mss-shop-reconciler", flag.ContinueOnError)
	flags.SetOutput(ioDiscard{})
	var result options
	flags.StringVar(&result.config.Environment, "environment", getenv("R1SHOP_RECONCILER_ENVIRONMENT"), "fixed reconciliation environment")
	flags.StringVar(&result.config.Namespace, "namespace", getenv("POD_NAMESPACE"), "fixed Kubernetes namespace")
	if err := flags.Parse(arguments); err != nil {
		return options{}, fmt.Errorf("parse reconciler options: %w", err)
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("reconciler does not accept positional arguments")
	}
	result.config.DatabaseDSN = getenv(environmentDatabaseDSN)
	result.config.TenantID = stage.TenantID
	result.config.TenantKey = stage.TenantKey
	result.config.LegacyTenantID = stage.LegacyTenantID
	result.config.ImportReceiptSHA256 = getenv(environmentImportReceiptSHA256)
	result.credentials = postgresdriver.Credentials{
		TenantMigratorPassword: []byte(getenv(environmentTenantMigratorPassword)),
		TenantRuntimePassword:  []byte(getenv(environmentTenantRuntimePassword)),
		MallMigratorPassword:   []byte(getenv(environmentMallMigratorPassword)),
		MallRuntimePassword:    []byte(getenv(environmentMallRuntimePassword)),
	}
	if err := result.credentials.Validate(); err != nil {
		return options{}, err
	}
	return result, nil
}

// ioDiscard avoids echoing a flag parse failure to an uncontrolled stream;
// parseOptions returns one bounded error to the caller instead.
type ioDiscard struct{}

func (ioDiscard) Write(value []byte) (int, error) { return len(value), nil }
