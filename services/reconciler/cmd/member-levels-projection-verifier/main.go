// Command member-levels-projection-verifier performs the fixed, one-time
// runtime-side verification of the imported member-level projection. It has no
// Kubernetes client, accepts no arguments, uses only the mall runtime DSN and
// PostgreSQL CA, and emits exactly one bounded JSON object.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
)

const (
	databaseDSNEnvironment       = "MSS_PROJECTION_DATABASE_DSN"
	postgresCAEnvironment        = "MSS_PROJECTION_POSTGRES_TLS_CA_FILE"
	postgresServerEnvironment    = "MSS_PROJECTION_POSTGRES_TLS_SERVER_NAME"
	importReceiptEnvironment     = "MSS_PROJECTION_IMPORT_RECEIPT_SHA256"
	podNameEnvironment           = "POD_NAME"
	podNamespaceEnvironment      = "POD_NAMESPACE"
	podUIDEnvironment            = "POD_UID"
	imageRevisionEnvironment     = "MSS_IMAGE_REVISION"
	imageDigestEnvironment       = "MSS_IMAGE_DIGEST"
	namespace                    = "mss-shop-dev"
	postgresHost                 = "mss-shop-postgres.mss-shop-dev.svc"
	postgresDatabase             = "mss_shop_dev"
	postgresCAPath               = "/etc/mss-shop/postgres-tls/ca.crt"
	mallRuntimeRole              = "mss_m_aussibuy_runtime"
	mallCoreSchema               = "mss_m_aussibuy_core"
	mallBusinessSchema           = "mss_m_aussibuy_biz"
	auditView                    = "r1_member_levels_projection_audit"
	legacyTenantID               = "518729051064631297"
	imageRepository              = "ghcr.io/shop-r1/mss-shop-reconciler"
	projectionVerifierPodPrefix  = "mss-shop-ml-projection-"
	observedRevisionPrefixLength = 32
	markerPrefix                 = "mss-shop-isolated-dev:legacy-import:v1:"
)

var (
	fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)
	fullDigest   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	fullReceipt  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	podName      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	podUID       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

const auditMetricsSQL = `
SELECT public_member_levels_rows, business_member_levels_rows, difference_rows,
       cross_tenant_rows, flagged_default_rows, enabled_default_rows, invalid_default_rows,
       duplicate_active_name_groups,
       public_orders_rows, business_orders_rows, public_order_goods_rows, business_order_goods_rows,
       count(*) OVER ()
FROM mss_m_aussibuy_biz.r1_member_levels_projection_audit`

const businessProjectionSQL = `
SELECT count(*)::bigint,
       count(*) FILTER (WHERE tenant_id IS DISTINCT FROM '518729051064631297')::bigint,
       count(*) FILTER (WHERE init IS TRUE)::bigint,
       count(*) FILTER (WHERE init IS TRUE AND deleted_at IS NULL AND status = 1)::bigint,
       count(*) FILTER (WHERE init IS TRUE AND (deleted_at IS NOT NULL OR status IS DISTINCT FROM 1))::bigint,
       (SELECT count(*) FROM (
          SELECT name FROM mss_m_aussibuy_biz.member_levels
          WHERE deleted_at IS NULL
          GROUP BY name HAVING count(*) > 1
        ) AS duplicate_active_names)::bigint,
       (SELECT count(*) FROM mss_m_aussibuy_biz.orders)::bigint,
       (SELECT count(*) FROM mss_m_aussibuy_biz.order_goods)::bigint
FROM mss_m_aussibuy_biz.member_levels`

const publicPrivilegeSQL = `
SELECT relation.relname::text,
       (
         pg_catalog.has_table_privilege(current_user, relation.oid, 'SELECT')
         OR pg_catalog.has_table_privilege(current_user, relation.oid, 'INSERT')
         OR pg_catalog.has_table_privilege(current_user, relation.oid, 'UPDATE')
         OR pg_catalog.has_table_privilege(current_user, relation.oid, 'DELETE')
         OR pg_catalog.has_table_privilege(current_user, relation.oid, 'TRUNCATE')
         OR pg_catalog.has_table_privilege(current_user, relation.oid, 'REFERENCES')
         OR pg_catalog.has_table_privilege(current_user, relation.oid, 'TRIGGER')
         OR pg_catalog.has_table_privilege(current_user, relation.oid, 'MAINTAIN')
       ) AS table_privilege,
       (
         pg_catalog.has_any_column_privilege(current_user, relation.oid, 'SELECT')
         OR pg_catalog.has_any_column_privilege(current_user, relation.oid, 'INSERT')
         OR pg_catalog.has_any_column_privilege(current_user, relation.oid, 'UPDATE')
         OR pg_catalog.has_any_column_privilege(current_user, relation.oid, 'REFERENCES')
       ) AS column_privilege
FROM pg_catalog.pg_class AS relation
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'public'
  AND relation.relkind = 'r'
  AND relation.relname IN ('member_levels', 'orders', 'order_goods')
ORDER BY relation.relname`

type configuration struct {
	databaseDSN                            string
	tlsConfig                              *tls.Config
	podName, podUID                        string
	imageRevision, imageDigest, receiptSHA string
}

type metrics struct {
	PublicMemberLevelsRows   int64 `json:"publicMemberLevelsRows"`
	BusinessMemberLevelsRows int64 `json:"businessMemberLevelsRows"`
	DifferenceRows           int64 `json:"differenceRows"`
	CrossTenantRows          int64 `json:"crossTenantRows"`
	FlaggedDefaultRows       int64 `json:"flaggedDefaultRows"`
	EnabledDefaultRows       int64 `json:"enabledDefaultRows"`
	InvalidDefaultRows       int64 `json:"invalidDefaultRows"`
	DuplicateNameGroups      int64 `json:"duplicateNameGroups"`
	PublicOrdersRows         int64 `json:"publicOrdersRows"`
	BusinessOrdersRows       int64 `json:"businessOrdersRows"`
	PublicOrderGoodsRows     int64 `json:"publicOrderGoodsRows"`
	BusinessOrderGoodsRows   int64 `json:"businessOrderGoodsRows"`
}

type successRecord struct {
	Version                 string  `json:"version"`
	Verified                bool    `json:"verified"`
	TargetDatabase          string  `json:"targetDatabase"`
	BusinessSchema          string  `json:"businessSchema"`
	AuditView               string  `json:"auditView"`
	LegacyTenantID          string  `json:"legacyTenantID"`
	ImportReceiptSHA256     string  `json:"importReceiptSHA256"`
	Metrics                 metrics `json:"metrics"`
	RuntimePublicPrivileges bool    `json:"runtimePublicPrivileges"`
	Namespace               string  `json:"namespace"`
	PodName                 string  `json:"podName"`
	PodUID                  string  `json:"podUID"`
	Revision                string  `json:"revision"`
	ImageRepository         string  `json:"imageRepository"`
	ImageDigest             string  `json:"imageDigest"`
	ImageReference          string  `json:"imageReference"`
}

type failureRecord struct {
	Version  string `json:"version"`
	Verified bool   `json:"verified"`
	Failure  string `json:"failure"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, lookup func(string) (string, bool), output io.Writer) error {
	if output == nil {
		return errors.New("verification output unavailable")
	}
	settings, err := loadConfiguration(arguments, lookup)
	var observed metrics
	if err == nil {
		observed, err = verifyProjection(ctx, settings)
	}
	if err != nil {
		writeErr := writeJSON(output, failureRecord{
			Version:  "mss-shop-member-levels-projection-verification-failure/v1",
			Verified: false,
			Failure:  safeFailure(err),
		})
		if writeErr != nil {
			return writeErr
		}
		return err
	}
	record := successRecord{
		Version:                 "mss-shop-member-levels-projection-verification/v1",
		Verified:                true,
		TargetDatabase:          postgresDatabase,
		BusinessSchema:          mallBusinessSchema,
		AuditView:               auditView,
		LegacyTenantID:          legacyTenantID,
		ImportReceiptSHA256:     settings.receiptSHA,
		Metrics:                 observed,
		RuntimePublicPrivileges: false,
		Namespace:               namespace,
		PodName:                 settings.podName,
		PodUID:                  settings.podUID,
		Revision:                settings.imageRevision,
		ImageRepository:         imageRepository,
		ImageDigest:             settings.imageDigest,
		ImageReference:          imageRepository + ":" + settings.imageRevision + "@" + settings.imageDigest,
	}
	return writeJSON(output, record)
}

func loadConfiguration(arguments []string, lookup func(string) (string, bool)) (configuration, error) {
	if len(arguments) != 0 || lookup == nil {
		return configuration{}, errors.New("configuration rejected")
	}
	dsn, dsnSet := lookup(databaseDSNEnvironment)
	caPath, caSet := lookup(postgresCAEnvironment)
	serverName, serverSet := lookup(postgresServerEnvironment)
	receipt, receiptSet := lookup(importReceiptEnvironment)
	podNameValue, podNameSet := lookup(podNameEnvironment)
	podNamespace, namespaceSet := lookup(podNamespaceEnvironment)
	podUIDValue, podUIDSet := lookup(podUIDEnvironment)
	revision, revisionSet := lookup(imageRevisionEnvironment)
	digest, digestSet := lookup(imageDigestEnvironment)
	if !dsnSet || !caSet || !serverSet || !receiptSet || !podNameSet || !namespaceSet ||
		!podUIDSet || !revisionSet || !digestSet || caPath != postgresCAPath ||
		serverName != postgresHost || podNamespace != namespace ||
		!validReceipt(receipt) || !validRevision(revision) || !validDigest(digest) ||
		!validProjectionPodName(podNameValue, revision) || !podUID.MatchString(podUIDValue) {
		return configuration{}, errors.New("configuration rejected")
	}
	if err := validateRuntimeDSN(dsn, caPath); err != nil {
		return configuration{}, err
	}
	tlsConfig, err := loadTLS(caPath, serverName)
	if err != nil {
		return configuration{}, errors.New("postgres tls unavailable")
	}
	return configuration{
		databaseDSN: dsn, tlsConfig: tlsConfig, podName: podNameValue, podUID: podUIDValue,
		imageRevision: revision, imageDigest: digest, receiptSHA: receipt,
	}, nil
}

func validRevision(value string) bool {
	return fullRevision.MatchString(value) && strings.Trim(value, "0") != ""
}

func validDigest(value string) bool {
	return fullDigest.MatchString(value) && strings.TrimPrefix(value, "sha256:") != strings.Repeat("0", 64)
}

func validReceipt(value string) bool {
	return fullReceipt.MatchString(value) && strings.Trim(value, "0") != ""
}

func validProjectionPodName(name, revision string) bool {
	return validRevision(revision) && len(name) > len(projectionVerifierPodPrefix)+observedRevisionPrefixLength &&
		len(name) <= 63 && podName.MatchString(name) &&
		strings.HasPrefix(name, projectionVerifierPodPrefix+revision[:observedRevisionPrefixLength])
}

func validateRuntimeDSN(value, caPath string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.Scheme != "postgres" || parsed.Opaque != "" ||
		parsed.User == nil || parsed.User.Username() != mallRuntimeRole || parsed.Hostname() != postgresHost ||
		parsed.Port() != "5432" || parsed.Path != "/"+postgresDatabase || parsed.Fragment != "" ||
		net.ParseIP(parsed.Hostname()) != nil {
		return errors.New("postgres endpoint rejected")
	}
	password, passwordSet := parsed.User.Password()
	query, queryErr := url.ParseQuery(parsed.RawQuery)
	if !passwordSet || password == "" || strings.IndexByte(password, 0) >= 0 || queryErr != nil ||
		len(query) != 3 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "verify-full" ||
		len(query["sslrootcert"]) != 1 || query.Get("sslrootcert") != caPath ||
		len(query["search_path"]) != 1 || query.Get("search_path") != mallCoreSchema {
		return errors.New("postgres endpoint rejected")
	}
	return nil
}

func loadTLS(path, serverName string) (*tls.Config, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || serverName != postgresHost {
		return nil, errors.New("invalid certificate authority path")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid certificate authority")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: serverName}, nil
}

func verifyProjection(ctx context.Context, settings configuration) (metrics, error) {
	parsed, err := pgx.ParseConfig(settings.databaseDSN)
	if err != nil {
		return metrics{}, errors.New("postgres endpoint rejected")
	}
	parsed.TLSConfig, parsed.Fallbacks = settings.tlsConfig, nil
	if parsed.RuntimeParams == nil {
		parsed.RuntimeParams = make(map[string]string)
	}
	parsed.RuntimeParams["application_name"] = "mss-shop-member-levels-projection-verifier"
	parsed.RuntimeParams["default_transaction_read_only"] = "on"
	connection, err := pgx.ConnectConfig(ctx, parsed)
	if err != nil {
		return metrics{}, errors.New("postgres unavailable")
	}
	defer connection.Close(context.WithoutCancel(ctx))
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return metrics{}, errors.New("postgres transaction unavailable")
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	var database, currentUser, sessionUser, currentSchema, marker string
	var readOnly, ssl bool
	err = tx.QueryRow(ctx, `
SELECT current_database(), current_user, session_user, current_schema(),
       current_setting('transaction_read_only')::boolean,
       COALESCE((SELECT ssl FROM pg_catalog.pg_stat_ssl WHERE pid = pg_catalog.pg_backend_pid()), false),
       COALESCE(pg_catalog.shobj_description(
         (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database()),
         'pg_database'
       ), '')`).Scan(&database, &currentUser, &sessionUser, &currentSchema, &readOnly, &ssl, &marker)
	if err != nil || database != postgresDatabase || currentUser != mallRuntimeRole ||
		sessionUser != mallRuntimeRole || currentSchema != mallCoreSchema || !readOnly || !ssl ||
		marker != markerPrefix+settings.receiptSHA {
		return metrics{}, errors.New("postgres boundary rejected")
	}

	observed, err := readAuditMetrics(ctx, tx)
	if err != nil || validateExpectedMetrics(observed) != nil {
		return metrics{}, errors.New("projection audit rejected")
	}
	if err := verifyBusinessProjection(ctx, tx, observed); err != nil {
		return metrics{}, err
	}
	if err := verifyNoPublicPrivileges(ctx, tx); err != nil {
		return metrics{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return metrics{}, errors.New("postgres transaction unavailable")
	}
	return observed, nil
}

func readAuditMetrics(ctx context.Context, tx pgx.Tx) (metrics, error) {
	var result metrics
	var auditRows int64
	err := tx.QueryRow(ctx, auditMetricsSQL).Scan(
		&result.PublicMemberLevelsRows, &result.BusinessMemberLevelsRows, &result.DifferenceRows,
		&result.CrossTenantRows, &result.FlaggedDefaultRows, &result.EnabledDefaultRows,
		&result.InvalidDefaultRows, &result.DuplicateNameGroups,
		&result.PublicOrdersRows, &result.BusinessOrdersRows,
		&result.PublicOrderGoodsRows, &result.BusinessOrderGoodsRows, &auditRows,
	)
	if err != nil || auditRows != 1 {
		return metrics{}, errors.New("projection audit unavailable")
	}
	return result, nil
}

func validateExpectedMetrics(value metrics) error {
	expected := metrics{
		PublicMemberLevelsRows: 4, BusinessMemberLevelsRows: 4, DifferenceRows: 0,
		CrossTenantRows: 0, FlaggedDefaultRows: 1, EnabledDefaultRows: 1, InvalidDefaultRows: 0,
		DuplicateNameGroups: 0,
		PublicOrdersRows:    0, BusinessOrdersRows: 0, PublicOrderGoodsRows: 0, BusinessOrderGoodsRows: 0,
	}
	if value != expected {
		return errors.New("projection metrics rejected")
	}
	return nil
}

func verifyBusinessProjection(ctx context.Context, tx pgx.Tx, audited metrics) error {
	var memberLevels, crossTenant, flaggedDefault, enabledDefault, invalidDefault, duplicateNames int64
	var orders, orderGoods int64
	err := tx.QueryRow(ctx, businessProjectionSQL).Scan(
		&memberLevels, &crossTenant, &flaggedDefault, &enabledDefault, &invalidDefault,
		&duplicateNames, &orders, &orderGoods,
	)
	if err != nil || memberLevels != audited.BusinessMemberLevelsRows || crossTenant != audited.CrossTenantRows ||
		flaggedDefault != audited.FlaggedDefaultRows || enabledDefault != audited.EnabledDefaultRows ||
		invalidDefault != audited.InvalidDefaultRows || duplicateNames != audited.DuplicateNameGroups ||
		orders != audited.BusinessOrdersRows ||
		orderGoods != audited.BusinessOrderGoodsRows {
		return errors.New("business projection rejected")
	}
	return nil
}

func verifyNoPublicPrivileges(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, publicPrivilegeSQL)
	if err != nil {
		return errors.New("runtime public privilege rejected")
	}
	defer rows.Close()
	expected := map[string]bool{"member_levels": false, "orders": false, "order_goods": false}
	seen := make(map[string]struct{}, len(expected))
	for rows.Next() {
		var relation string
		var tablePrivilege, columnPrivilege bool
		if err := rows.Scan(&relation, &tablePrivilege, &columnPrivilege); err != nil {
			return errors.New("runtime public privilege rejected")
		}
		if _, approved := expected[relation]; !approved || tablePrivilege || columnPrivilege {
			return errors.New("runtime public privilege rejected")
		}
		if _, duplicate := seen[relation]; duplicate {
			return errors.New("runtime public privilege rejected")
		}
		seen[relation] = struct{}{}
	}
	if rows.Err() != nil || len(seen) != len(expected) {
		return errors.New("runtime public privilege rejected")
	}
	return nil
}

func safeFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case strings.Contains(err.Error(), "privilege"):
		return "privilege"
	case strings.Contains(err.Error(), "projection"):
		return "projection"
	case strings.Contains(err.Error(), "postgres"):
		return "postgres"
	default:
		return "configuration"
	}
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		return errors.New("write verification output")
	}
	return nil
}
