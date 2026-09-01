package main

import (
	"strings"
	"testing"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const testImportReceiptSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestParseOptionsKeepsSecretsOutOfArguments(t *testing.T) {
	t.Parallel()
	environment := map[string]string{
		"R1SHOP_RECONCILER_ENVIRONMENT":   stage.Environment,
		"POD_NAMESPACE":                   stage.Namespace,
		environmentDatabaseDSN:            "postgres://bootstrap:database-secret@" + stage.DatabaseHost + ":5432/" + stage.DatabaseName + "?sslmode=verify-full&sslrootcert=" + stage.DatabaseCAPath,
		environmentTenantMigratorPassword: "tenant-migrator-password",
		environmentTenantRuntimePassword:  "tenant-runtime-password-1",
		environmentMallMigratorPassword:   "mall-migrator-password-01",
		environmentMallRuntimePassword:    "mall-runtime-password-001",
		environmentImportReceiptSHA256:    testImportReceiptSHA256,
	}
	getenv := func(key string) string { return environment[key] }
	options, err := parseOptions(nil, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := options.config.Validate(); err != nil {
		t.Fatal(err)
	}
	if options.config.TenantID != stage.TenantID || options.config.TenantKey != stage.TenantKey ||
		options.config.LegacyTenantID != stage.LegacyTenantID ||
		options.config.ImportReceiptSHA256 != testImportReceiptSHA256 {
		t.Fatal("stage binding is not compiled into options")
	}

	for _, forbidden := range []string{
		"--database-dsn",
		"--redis-password",
		"--schema",
		"--tenant-id",
		"--tenant-key",
		"--legacy-tenant-id",
		"--import-receipt-sha256",
		"--receipt",
	} {
		if _, err := parseOptions([]string{forbidden, "secret"}, getenv); err == nil {
			t.Fatalf("secret/schema option %s unexpectedly accepted", forbidden)
		}
	}
}

func TestParseOptionsErrorDoesNotExposeSecretEnvironment(t *testing.T) {
	t.Parallel()
	const secret = "never-print-this-dsn-secret"
	getenv := func(key string) string {
		if key == environmentDatabaseDSN {
			return "postgres://bootstrap:" + secret + "@%zz"
		}
		switch key {
		case environmentTenantMigratorPassword:
			return "tenant-migrator-password"
		case environmentTenantRuntimePassword:
			return "tenant-runtime-password-1"
		case environmentMallMigratorPassword:
			return "mall-migrator-password-01"
		case environmentMallRuntimePassword:
			return "mall-runtime-password-001"
		case environmentImportReceiptSHA256:
			return testImportReceiptSHA256
		}
		return ""
	}
	options, err := parseOptions(nil, getenv)
	if err != nil {
		t.Fatal(err)
	}
	err = options.config.Validate()
	if err == nil {
		t.Fatal("invalid environment unexpectedly accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed secret: %v", err)
	}
}

func TestParseOptionsReceiptErrorDoesNotExposeEnvironmentValue(t *testing.T) {
	t.Parallel()
	const receipt = "receipt-value-that-must-not-be-echoed"
	environment := map[string]string{
		"R1SHOP_RECONCILER_ENVIRONMENT":   stage.Environment,
		"POD_NAMESPACE":                   stage.Namespace,
		environmentDatabaseDSN:            "postgres://bootstrap:database-secret@" + stage.DatabaseHost + ":5432/" + stage.DatabaseName + "?sslmode=verify-full&sslrootcert=" + stage.DatabaseCAPath,
		environmentTenantMigratorPassword: "tenant-migrator-password",
		environmentTenantRuntimePassword:  "tenant-runtime-password-1",
		environmentMallMigratorPassword:   "mall-migrator-password-01",
		environmentMallRuntimePassword:    "mall-runtime-password-001",
		environmentImportReceiptSHA256:    receipt,
	}
	options, err := parseOptions(nil, func(key string) string { return environment[key] })
	if err != nil {
		t.Fatal(err)
	}
	err = options.config.Validate()
	if err == nil {
		t.Fatal("invalid import receipt unexpectedly accepted")
	}
	if strings.Contains(err.Error(), receipt) {
		t.Fatalf("error exposed import receipt: %v", err)
	}
}
