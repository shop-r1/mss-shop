package stage

import (
	"errors"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Environment:         Environment,
		Namespace:           Namespace,
		DatabaseDSN:         "postgres://bootstrap:secret-value@" + DatabaseHost + ":5432/" + DatabaseName + "?sslmode=verify-full&sslrootcert=" + DatabaseCAPath,
		RedisPassword:       []byte("redis-secret-value"),
		TenantID:            TenantID,
		TenantKey:           TenantKey,
		LegacyTenantID:      LegacyTenantID,
		ImportReceiptSHA256: strings.Repeat("a", 64),
	}
}

func TestConfigAcceptsOnlyFixedDevelopmentBoundary(t *testing.T) {
	t.Parallel()
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "production environment", mutate: func(c *Config) { c.Environment = "r1shop-prod" }},
		{name: "other namespace", mutate: func(c *Config) { c.Namespace = "default" }},
		{name: "production database", mutate: func(c *Config) {
			c.DatabaseDSN = "postgres://bootstrap:secret-value@timescaledb-r1shop-prod.database.svc:5432/r1shop"
		}},
		{name: "other database", mutate: func(c *Config) {
			c.DatabaseDSN = "postgres://bootstrap:secret-value@" + DatabaseHost + ":5432/postgres"
		}},
		{name: "fallback database host", mutate: func(c *Config) {
			c.DatabaseDSN = "postgres://bootstrap:secret-value@" + DatabaseHost + ":5432,timescaledb-r1shop-prod.database.svc:5432/" + DatabaseName + "?sslmode=verify-full&sslrootcert=" + DatabaseCAPath
		}},
		{name: "disabled TLS", mutate: func(c *Config) { c.DatabaseDSN = strings.Replace(c.DatabaseDSN, "verify-full", "disable", 1) }},
		{name: "encryption without identity verification", mutate: func(c *Config) { c.DatabaseDSN = strings.Replace(c.DatabaseDSN, "verify-full", "require", 1) }},
		{name: "CA without hostname verification", mutate: func(c *Config) { c.DatabaseDSN = strings.Replace(c.DatabaseDSN, "verify-full", "verify-ca", 1) }},
		{name: "missing CA", mutate: func(c *Config) { c.DatabaseDSN = strings.Split(c.DatabaseDSN, "&sslrootcert=")[0] }},
		{name: "other CA", mutate: func(c *Config) {
			c.DatabaseDSN = strings.Replace(c.DatabaseDSN, DatabaseCAPath, "/tmp/other-ca.crt", 1)
		}},
		{name: "unapproved bootstrap runtime parameter", mutate: func(c *Config) { c.DatabaseDSN += "&default_table_access_method=unsafe_am" }},
		{name: "duplicate TLS parameter", mutate: func(c *Config) { c.DatabaseDSN += "&sslmode=verify-full" }},
		{name: "other tenant identity", mutate: func(c *Config) { c.TenantID = "tenant-other-dev" }},
		{name: "other tenant key", mutate: func(c *Config) { c.TenantKey = "stage001" }},
		{name: "temporary key", mutate: func(c *Config) { c.TenantKey = "temporary01" }},
		{name: "other legacy identity", mutate: func(c *Config) { c.LegacyTenantID = "legacy-tenant-001" }},
		{name: "unsafe legacy identity", mutate: func(c *Config) { c.LegacyTenantID = "x' OR true --" }},
		{name: "missing import receipt", mutate: func(c *Config) { c.ImportReceiptSHA256 = "" }},
		{name: "short import receipt", mutate: func(c *Config) { c.ImportReceiptSHA256 = strings.Repeat("a", 63) }},
		{name: "uppercase import receipt", mutate: func(c *Config) { c.ImportReceiptSHA256 = strings.Repeat("A", 64) }},
		{name: "zero import receipt", mutate: func(c *Config) { c.ImportReceiptSHA256 = strings.Repeat("0", 64) }},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := validConfig()
			test.mutate(&config)
			if err := config.Validate(); !errors.Is(err, ErrUnsafeTarget) {
				t.Fatalf("error = %v, want ErrUnsafeTarget", err)
			}
		})
	}
}

func TestImportReceiptValidationErrorDoesNotExposeReceipt(t *testing.T) {
	t.Parallel()
	config := validConfig()
	const receipt = "receipt-value-that-must-not-appear-in-an-error"
	config.ImportReceiptSHA256 = receipt
	err := config.Validate()
	if err == nil {
		t.Fatal("invalid import receipt unexpectedly accepted")
	}
	if strings.Contains(err.Error(), receipt) {
		t.Fatalf("validation error exposed the import receipt: %v", err)
	}
}

func TestConfigErrorsNeverContainCredentials(t *testing.T) {
	t.Parallel()
	config := validConfig()
	const secret = "do-not-print-this-secret"
	config.DatabaseDSN = "postgres://bootstrap:" + secret + "@%zz"
	err := config.Validate()
	if err == nil {
		t.Fatal("malformed DSN unexpectedly accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("validation error exposed the DSN credential: %v", err)
	}
}

func TestDerivedNamesContainNoCallerSelectedSchema(t *testing.T) {
	t.Parallel()
	config := validConfig()
	schemas := config.Schemas()
	if schemas.TenantCore != "mss_t_dev_core" || schemas.TenantShared != "mss_t_dev_shared" {
		t.Fatalf("control schemas = %+v", schemas)
	}
	if schemas.MallCore != "mss_m_aussibuy_core" || schemas.MallBusiness != "mss_m_aussibuy_biz" {
		t.Fatalf("mall schemas = %+v", schemas)
	}
	for _, value := range []string{
		schemas.TenantCore,
		schemas.TenantShared,
		schemas.MallCore,
		schemas.MallBusiness,
	} {
		if strings.Contains(value, "tmp") || strings.Contains(value, "prod") {
			t.Fatalf("unsafe derived schema %q", value)
		}
	}

	roles := config.Roles()
	if roles.TenantMigrator != "mss_t_dev_migrator" ||
		roles.TenantRuntime != "mss_t_dev_runtime" ||
		roles.TenantCompatibilityOwner != "mss_t_dev_compat_owner" {
		t.Fatalf("control roles = %+v", roles)
	}
	if roles.MallMigrator != "mss_m_aussibuy_migrator" ||
		roles.MallRuntime != "mss_m_aussibuy_runtime" ||
		roles.MallCompatibilityOwner != "mss_m_aussibuy_compat_owner" {
		t.Fatalf("mall roles = %+v", roles)
	}
	for _, value := range []string{
		roles.TenantMigrator,
		roles.TenantRuntime,
		roles.TenantCompatibilityOwner,
		roles.MallMigrator,
		roles.MallRuntime,
		roles.MallCompatibilityOwner,
	} {
		if strings.Contains(value, "tmp") || strings.Contains(value, "prod") || strings.HasPrefix(value, "r1_") {
			t.Fatalf("unsafe derived role %q", value)
		}
	}
}
