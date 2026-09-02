package postgres

import (
	"context"
	"errors"
	"net"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

// StageDatabaseBoundarySummary contains counts only. It is safe for the
// trusted operator command to log and never contains a DSN, object definition,
// credential, or business value.
type StageDatabaseBoundarySummary struct {
	LoginEventTriggers     int
	DDLEventTriggers       int
	Publications           int
	PublicSecurityDefiners int
}

type stageDatabaseBoundary struct {
	ServerVersionNumber  string
	SSL                  bool
	DatabaseName         string
	SessionIdentityExact bool
	DatabaseOwnerCurrent bool
	DatabaseMarker       string
	DatabaseInventory    []string
	ExtensionInventory   []string
	SourceTableInventory []string
	OrdersRows           int64
	OrderGoodsRows       int64
	PublicSchemaSafe     bool
	DatabaseSettings     int
	ForeignPublicConnect int
	Subscriptions        int
	Summary              StageDatabaseBoundarySummary
}

// DialFunc matches pgx's connection dial hook. stage-secrets uses it to retain
// the fixed service DNS as the authenticated DSN host while dialing the
// separately validated ClusterIP from an operator host outside cluster DNS.
type DialFunc = pgconn.DialFunc

var expectedImportReceipt = regexp.MustCompile(`^[0-9a-f]{64}$`)

const isolatedImportMarkerPrefix = "mss-shop-isolated-dev:legacy-import:v1:"

// PreflightStageDatabase performs the independent, catalog-only operator gate
// before the reconciler can run any DDL. The startup packet disables event
// triggers before PostgreSQL's login event can run; the dedicated vanilla
// PostgreSQL 17 catalog is then treated as a hard gate.
func PreflightStageDatabase(
	ctx context.Context,
	dsn string,
	importReceiptSHA256 string,
) (StageDatabaseBoundarySummary, error) {
	return PreflightStageDatabaseWithDial(ctx, dsn, importReceiptSHA256, nil)
}

func PreflightStageDatabaseWithDial(
	ctx context.Context,
	dsn string,
	importReceiptSHA256 string,
	dial DialFunc,
) (StageDatabaseBoundarySummary, error) {
	if !validExpectedImportReceipt(importReceiptSHA256) {
		return StageDatabaseBoundarySummary{}, errors.New("database boundary preflight import receipt is invalid")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil || parsed.Host != stage.DatabaseHost || parsed.Port != stage.DatabasePort ||
		parsed.Database != stage.DatabaseName || len(parsed.Fallbacks) != 0 ||
		net.ParseIP(parsed.Host) != nil {
		return StageDatabaseBoundarySummary{}, errors.New("database boundary preflight target is invalid")
	}
	if _, supplied := parsed.RuntimeParams["event_triggers"]; supplied {
		return StageDatabaseBoundarySummary{}, errors.New("database boundary preflight target is invalid")
	}
	if _, supplied := parsed.RuntimeParams["options"]; supplied {
		return StageDatabaseBoundarySummary{}, errors.New("database boundary preflight target is invalid")
	}
	if parsed.RuntimeParams == nil {
		parsed.RuntimeParams = make(map[string]string)
	}
	parsed.RuntimeParams["event_triggers"] = "false"
	if dial != nil {
		parsed.DialFunc = dial
	}
	connection, err := pgx.ConnectConfig(ctx, parsed)
	if err != nil {
		return StageDatabaseBoundarySummary{}, errors.New("connect database boundary preflight failed")
	}
	defer connection.Close(context.WithoutCancel(ctx))

	boundary := stageDatabaseBoundary{}
	var eventTriggers string
	if err := connection.QueryRow(ctx, `
SELECT current_setting('server_version_num'),
       current_setting('event_triggers'),
       current_database(),
       session_user = current_user
         AND COALESCE((SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user), false),
       COALESCE((SELECT datdba = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user)
                   FROM pg_catalog.pg_database WHERE datname = current_database()), false),
       COALESCE(pg_catalog.shobj_description(
         (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database()),
         'pg_database'
       ), ''),
       COALESCE((SELECT ssl FROM pg_catalog.pg_stat_ssl WHERE pid = pg_catalog.pg_backend_pid()), false)
`).Scan(
		&boundary.ServerVersionNumber,
		&eventTriggers,
		&boundary.DatabaseName,
		&boundary.SessionIdentityExact,
		&boundary.DatabaseOwnerCurrent,
		&boundary.DatabaseMarker,
		&boundary.SSL,
	); err != nil || eventTriggers != "off" {
		return StageDatabaseBoundarySummary{}, errors.New("database boundary preflight connection is not safely trigger-disabled")
	}

	transaction, err := connection.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return StageDatabaseBoundarySummary{}, errors.New("begin read-only database boundary preflight failed")
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))

	if err := transaction.QueryRow(ctx, `
SELECT COALESCE(array_agg(datname::text ORDER BY datname), ARRAY[]::text[])
FROM pg_catalog.pg_database
`).Scan(&boundary.DatabaseInventory); err != nil {
		return StageDatabaseBoundarySummary{}, errors.New("read database inventory boundary failed")
	}
	if err := transaction.QueryRow(ctx, `
SELECT COALESCE(array_agg(
         extension.extname || ':' || extension.extversion || ':' || namespace.nspname || ':'
         || (extension.extowner = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user))::text
         ORDER BY extension.extname
       ), ARRAY[]::text[])
FROM pg_catalog.pg_extension AS extension
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = extension.extnamespace
`).Scan(&boundary.ExtensionInventory); err != nil {
		return StageDatabaseBoundarySummary{}, errors.New("read database extension boundary failed")
	}
	if err := transaction.QueryRow(ctx, `
SELECT COALESCE(array_agg(relation.relname::text ORDER BY relation.relname), ARRAY[]::text[])
FROM pg_catalog.pg_class AS relation
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'public' AND relation.relkind = 'r'
`).Scan(&boundary.SourceTableInventory); err != nil {
		return StageDatabaseBoundarySummary{}, errors.New("read imported source-table boundary failed")
	}
	if err := transaction.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ONLY "public"."orders"),
       (SELECT count(*) FROM ONLY "public"."order_goods")
`).Scan(&boundary.OrdersRows, &boundary.OrderGoodsRows); err != nil {
		return StageDatabaseBoundarySummary{}, errors.New("read structure-only order boundary failed")
	}
	if err := transaction.QueryRow(ctx, `
SELECT namespace.nspowner = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user)
       AND NOT pg_catalog.has_schema_privilege('public', namespace.oid, 'USAGE')
       AND NOT pg_catalog.has_schema_privilege('public', namespace.oid, 'CREATE')
FROM pg_catalog.pg_namespace AS namespace
WHERE namespace.nspname = 'public'
`).Scan(&boundary.PublicSchemaSafe); err != nil {
		return StageDatabaseBoundarySummary{}, errors.New("read public schema boundary failed")
	}
	if err := transaction.QueryRow(ctx, `
SELECT (SELECT count(*) FROM pg_catalog.pg_db_role_setting),
       (SELECT count(*)
          FROM pg_catalog.pg_database AS database
         WHERE database.datallowconn
           AND database.datname <> current_database()
           AND pg_catalog.has_database_privilege('public', database.datname, 'CONNECT')),
       (SELECT count(*) FROM pg_catalog.pg_subscription),
       (SELECT count(*) FROM pg_catalog.pg_event_trigger WHERE evtevent = 'login'),
       (SELECT count(*) FROM pg_catalog.pg_event_trigger WHERE evtevent <> 'login'),
       (SELECT count(*) FROM pg_catalog.pg_publication),
       (SELECT count(*)
          FROM pg_catalog.pg_proc AS routine
          JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
         WHERE routine.prokind IN ('f','p') AND routine.prosecdef
           AND namespace.nspname NOT LIKE 'pg_%%'
           AND namespace.nspname <> 'information_schema'
           AND pg_catalog.has_function_privilege('public', routine.oid, 'EXECUTE'))
`).Scan(
		&boundary.DatabaseSettings,
		&boundary.ForeignPublicConnect,
		&boundary.Subscriptions,
		&boundary.Summary.LoginEventTriggers,
		&boundary.Summary.DDLEventTriggers,
		&boundary.Summary.Publications,
		&boundary.Summary.PublicSecurityDefiners,
	); err != nil {
		return StageDatabaseBoundarySummary{}, errors.New("read isolated database capability boundary failed")
	}
	if err := validateStageDatabaseBoundary(boundary, importReceiptSHA256); err != nil {
		return StageDatabaseBoundarySummary{}, err
	}
	return boundary.Summary, nil
}

func validateStageDatabaseBoundary(boundary stageDatabaseBoundary, importReceiptSHA256 string) error {
	if !validExpectedImportReceipt(importReceiptSHA256) {
		return errors.New("database catalog boundary is not the reviewed isolated mss-shop-dev shape")
	}
	expectedMarker := isolatedImportMarkerPrefix + importReceiptSHA256
	wantDatabases := []string{stage.DatabaseName, "postgres", "template0", "template1"}
	sort.Strings(wantDatabases)
	wantSources := allLegacyResourceNames()
	sort.Strings(wantSources)
	actualSources := append([]string(nil), boundary.SourceTableInventory...)
	sort.Strings(actualSources)
	if boundary.ServerVersionNumber != "170006" || !boundary.SSL ||
		boundary.DatabaseName != stage.DatabaseName || !boundary.SessionIdentityExact ||
		!boundary.DatabaseOwnerCurrent || boundary.DatabaseMarker != expectedMarker ||
		!reflect.DeepEqual(boundary.DatabaseInventory, wantDatabases) ||
		!reflect.DeepEqual(boundary.ExtensionInventory, []string{"plpgsql:1.0:pg_catalog:true"}) ||
		!reflect.DeepEqual(actualSources, wantSources) || boundary.OrdersRows != 0 ||
		boundary.OrderGoodsRows != 0 || !boundary.PublicSchemaSafe ||
		boundary.DatabaseSettings != 0 || boundary.ForeignPublicConnect != 0 ||
		boundary.Subscriptions != 0 || boundary.Summary.LoginEventTriggers != 0 ||
		boundary.Summary.DDLEventTriggers != 0 || boundary.Summary.Publications != 0 ||
		boundary.Summary.PublicSecurityDefiners != 0 {
		return errors.New("database catalog boundary is not the reviewed isolated mss-shop-dev shape")
	}
	return nil
}

func validExpectedImportReceipt(value string) bool {
	return expectedImportReceipt.MatchString(value) && strings.Trim(value, "0") != ""
}
