// Package stage owns the deliberately narrow configuration for the isolated
// MSS Shop development environment. It is not a general environment or schema
// selector: every infrastructure boundary is compiled into this package and
// validation fails closed for the original r1shop-dev environment.
package stage

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	Environment            = "mss-shop-dev"
	Namespace              = "mss-shop-dev"
	DatabaseHost           = "mss-shop-postgres.mss-shop-dev.svc"
	DatabasePort    uint16 = 5432
	DatabaseName           = "mss_shop_dev"
	DatabaseCAPath         = "/etc/mss-shop/postgres-tls/ca.crt"
	LegacySchema           = "public"
	RedisAddress           = "mss-shop-redis.mss-shop-dev.svc:6379"
	TenantRedisDB          = 1
	MallRedisDB            = 2
	TenantAdminHost        = "tenant-admin.167.17.68.242.nip.io"
	MallAdminHost          = "mall-admin.167.17.68.242.nip.io"
	AdminTenantID          = "default"
	TenantID               = "tenant-aussibuy-dev"
	TenantKey              = "aussibuy"
	LegacyTenantID         = "518729051064631297"
)

var (
	ErrUnsafeTarget     = errors.New("unsafe development reconciliation target")
	tenantKey           = regexp.MustCompile(`^[a-z][a-z0-9]{5,19}$`)
	identity            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	importReceiptSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Config contains bootstrap inputs for exactly one isolated mss-shop-dev stage
// deployment. DatabaseDSN and RedisPassword are secret values and must never
// be formatted, logged, or returned in an error.
type Config struct {
	Environment         string
	Namespace           string
	DatabaseDSN         string
	RedisPassword       []byte
	TenantID            string
	TenantKey           string
	LegacyTenantID      string
	ImportReceiptSHA256 string
}

type Schemas struct {
	TenantCore   string
	TenantShared string
	MallCore     string
	MallBusiness string
}

type Roles struct {
	TenantMigrator           string
	TenantRuntime            string
	TenantCompatibilityOwner string
	MallMigrator             string
	MallRuntime              string
	MallCompatibilityOwner   string
}

type Names struct {
	TenantSecret string
	MallSecret   string
}

func (c Config) Validate() error {
	if c.Environment != Environment {
		return fmt.Errorf("%w: environment must be %q", ErrUnsafeTarget, Environment)
	}
	if c.Namespace != Namespace {
		return fmt.Errorf("%w: namespace must be %q", ErrUnsafeTarget, Namespace)
	}
	if c.TenantID != TenantID {
		return fmt.Errorf("%w: tenant identity must be the fixed development binding", ErrUnsafeTarget)
	}
	if c.TenantKey != TenantKey || !tenantKey.MatchString(c.TenantKey) || reservedKey(c.TenantKey) {
		return fmt.Errorf("%w: provisioning key must be the fixed development binding", ErrUnsafeTarget)
	}
	if c.LegacyTenantID != LegacyTenantID || !identity.MatchString(c.LegacyTenantID) {
		return fmt.Errorf("%w: legacy tenant identity must be the fixed development binding", ErrUnsafeTarget)
	}
	if !importReceiptSHA256.MatchString(c.ImportReceiptSHA256) || strings.Trim(c.ImportReceiptSHA256, "0") == "" {
		return fmt.Errorf("%w: legacy import receipt binding is invalid", ErrUnsafeTarget)
	}
	if err := validateDatabaseDSN(c.DatabaseDSN); err != nil {
		return err
	}
	return nil
}

func (c Config) Schemas() Schemas {
	return Schemas{
		TenantCore:   "mss_t_dev_core",
		TenantShared: "mss_t_dev_shared",
		MallCore:     "mss_m_" + c.TenantKey + "_core",
		MallBusiness: "mss_m_" + c.TenantKey + "_biz",
	}
}

func (c Config) Roles() Roles {
	return Roles{
		TenantMigrator:           "mss_t_dev_migrator",
		TenantRuntime:            "mss_t_dev_runtime",
		TenantCompatibilityOwner: "mss_t_dev_compat_owner",
		MallMigrator:             "mss_m_" + c.TenantKey + "_migrator",
		MallRuntime:              "mss_m_" + c.TenantKey + "_runtime",
		MallCompatibilityOwner:   "mss_m_" + c.TenantKey + "_compat_owner",
	}
}

func (c Config) Names() Names {
	return Names{
		TenantSecret: "mss-shop-tenant-admin-runtime",
		MallSecret:   "mss-shop-mall-admin-" + c.TenantKey + "-runtime",
	}
}

func validateDatabaseDSN(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: PostgreSQL bootstrap credential is required", ErrUnsafeTarget)
	}
	parsedURL, err := url.Parse(value)
	if err != nil || (parsedURL.Scheme != "postgres" && parsedURL.Scheme != "postgresql") ||
		parsedURL.Opaque != "" || parsedURL.Host == "" || parsedURL.Fragment != "" {
		return fmt.Errorf("%w: PostgreSQL bootstrap credential is invalid", ErrUnsafeTarget)
	}
	query, err := url.ParseQuery(parsedURL.RawQuery)
	if err != nil || len(query) != 2 || len(query["sslmode"]) != 1 ||
		query.Get("sslmode") != "verify-full" || len(query["sslrootcert"]) != 1 ||
		query.Get("sslrootcert") != DatabaseCAPath {
		return fmt.Errorf("%w: PostgreSQL bootstrap TLS parameters are not approved", ErrUnsafeTarget)
	}

	// pgx loads sslrootcert while parsing. Validate the connection shape with
	// the system pool here so Config validation stays deterministic before the
	// fixed CA Secret is mounted; the original exact path remains unchanged for
	// the real connection and was checked above.
	parseURL := *parsedURL
	parseQuery := parseURL.Query()
	parseQuery.Set("sslrootcert", "system")
	parseURL.RawQuery = parseQuery.Encode()
	parsed, err := pgx.ParseConfig(parseURL.String())
	if err != nil {
		// A parser error can contain fragments of the secret-bearing DSN. Keep
		// the cause deliberately out of the returned error.
		return fmt.Errorf("%w: PostgreSQL bootstrap credential is invalid", ErrUnsafeTarget)
	}
	if parsed.Host != DatabaseHost || parsed.Port != DatabasePort || parsed.Database != DatabaseName {
		return fmt.Errorf(
			"%w: PostgreSQL target must be %s:%d/%s",
			ErrUnsafeTarget,
			DatabaseHost,
			DatabasePort,
			DatabaseName,
		)
	}
	if len(parsed.Fallbacks) != 0 {
		return fmt.Errorf("%w: PostgreSQL fallback targets are forbidden", ErrUnsafeTarget)
	}
	if parsed.TLSConfig == nil || parsed.TLSConfig.ServerName != DatabaseHost {
		return fmt.Errorf("%w: PostgreSQL bootstrap TLS verification is not approved", ErrUnsafeTarget)
	}
	if strings.TrimSpace(parsed.User) == "" || strings.TrimSpace(parsed.Password) == "" {
		return fmt.Errorf("%w: PostgreSQL bootstrap user and password are required", ErrUnsafeTarget)
	}
	if len(parsed.RuntimeParams) != 0 {
		return fmt.Errorf("%w: PostgreSQL bootstrap runtime parameters are not approved", ErrUnsafeTarget)
	}
	return nil
}

func reservedKey(value string) bool {
	for _, token := range []string{"prod", "production", "tmp", "temp", "temporary", "compression"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}
