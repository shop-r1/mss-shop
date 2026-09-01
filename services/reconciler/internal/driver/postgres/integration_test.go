package postgres

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var disposableDatabaseNonce = regexp.MustCompile(`^[a-z0-9]{16,48}$`)

// TestCompletePlanRollsBackAtReviewedGraphGate is opt-in because it requires a
// disposable PostgreSQL 17 database with role-creation privilege. The cluster
// verification Pod supplies the DSN; normal local/unit runs skip it.
func TestCompletePlanRollsBackAtReviewedGraphGate(t *testing.T) {
	dsn := os.Getenv("R1SHOP_RECONCILER_INTEGRATION_DSN")
	if dsn == "" {
		t.Skip("disposable PostgreSQL DSN is not configured")
	}
	nonce := os.Getenv("R1SHOP_RECONCILER_INTEGRATION_NONCE")
	if !disposableDatabaseNonce.MatchString(nonce) {
		t.Fatal("disposable PostgreSQL nonce is missing or invalid")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("parse disposable PostgreSQL DSN")
	}
	expectedDatabase := "r1shop_reconciler_contract_" + nonce
	if parsed.Database != expectedDatabase || len(parsed.Fallbacks) != 0 ||
		(parsed.Host != "127.0.0.1" && parsed.Host != "localhost") {
		t.Fatal("integration DSN is not an exact local disposable database target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	connection, err := pgx.ConnectConfig(ctx, parsed)
	if err != nil {
		t.Fatal("connect disposable PostgreSQL")
	}
	defer connection.Close(ctx)
	validateDisposableDatabase(t, ctx, connection, expectedDatabase, nonce)
	seedLegacyCatalog(t, ctx, connection)

	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reachedGraphGate := false
	for _, statement := range plan.Batches[0].Statements {
		_, err = transaction.Exec(ctx, statement.SQL, statement.Arguments...)
		if statement.Name == "verify-snapshot-known-graph-profile" && err != nil {
			reachedGraphGate = true
			break
		}
		if err != nil {
			_ = transaction.Rollback(context.WithoutCancel(ctx))
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) {
				contextSQL := sqlPositionContext(statement.SQL, databaseError.Position)
				t.Fatalf(
					"complete plan stopped at statement %s: code=%s position=%d where=%s message=%s sql-context=%q",
					statement.Name, databaseError.Code, databaseError.Position, databaseError.Where, databaseError.Message, contextSQL,
				)
			}
			t.Fatalf("complete plan stopped at statement %s: %v", statement.Name, err)
		}
	}
	if !reachedGraphGate {
		_ = transaction.Rollback(context.WithoutCancel(ctx))
		t.Fatal("complete plan did not stop at the reviewed graph-profile gate")
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	for _, schema := range []string{
		testConfig().Schemas().TenantCore,
		testConfig().Schemas().TenantShared,
		testConfig().Schemas().MallCore,
		testConfig().Schemas().MallBusiness,
	} {
		var exists bool
		if err := connection.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = $1)", schema,
		).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("failed plan left schema %s behind", schema)
		}
	}
	for _, role := range []string{
		testConfig().Roles().TenantMigrator,
		testConfig().Roles().TenantRuntime,
		testConfig().Roles().TenantCompatibilityOwner,
		testConfig().Roles().MallMigrator,
		testConfig().Roles().MallRuntime,
		testConfig().Roles().MallCompatibilityOwner,
	} {
		var exists bool
		if err := connection.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = $1)", role,
		).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("failed plan left role %s behind", role)
		}
	}
}

func sqlPositionContext(statement string, position int32) string {
	if position <= 0 {
		return ""
	}
	index := int(position) - 1
	if index > len(statement) {
		index = len(statement)
	}
	start := index - 80
	if start < 0 {
		start = 0
	}
	end := index + 80
	if end > len(statement) {
		end = len(statement)
	}
	return statement[start:end]
}

func validateDisposableDatabase(t *testing.T, ctx context.Context, connection *pgx.Conn, expectedDatabase, nonce string) {
	t.Helper()
	var databaseName, sessionRole, databaseOwner, marker string
	if err := connection.QueryRow(ctx, `
SELECT database.datname,
       current_user,
       pg_catalog.pg_get_userbyid(database.datdba),
       COALESCE(pg_catalog.shobj_description(database.oid, 'pg_database'), '')
FROM pg_catalog.pg_database AS database
WHERE database.datname = current_database()
`).Scan(&databaseName, &sessionRole, &databaseOwner, &marker); err != nil {
		t.Fatal("inspect disposable PostgreSQL database boundary")
	}
	if databaseName != expectedDatabase || sessionRole != databaseOwner ||
		marker != "mss-shop-disposable-contract:"+nonce {
		t.Fatal("PostgreSQL integration database does not have the required disposable marker and owner")
	}
	var publicRelations, publicRoutines, managedSchemas, managedRoles int
	if err := connection.QueryRow(ctx, `
SELECT
  (SELECT count(*)
     FROM pg_catalog.pg_class AS relation
     JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'public'),
  (SELECT count(*)
     FROM pg_catalog.pg_proc AS routine
     JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
    WHERE namespace.nspname = 'public'),
  (SELECT count(*) FROM pg_catalog.pg_namespace
    WHERE nspname IN ('mss_t_dev_core','mss_t_dev_shared','mss_m_aussibuy_core','mss_m_aussibuy_biz')),
	  (SELECT count(*) FROM pg_catalog.pg_roles
	    WHERE rolname IN (
	      'mss_t_dev_migrator','mss_t_dev_runtime','mss_t_dev_compat_owner',
	      'mss_m_aussibuy_migrator','mss_m_aussibuy_runtime','mss_m_aussibuy_compat_owner'
	    ))
`).Scan(&publicRelations, &publicRoutines, &managedSchemas, &managedRoles); err != nil {
		t.Fatal("inspect disposable PostgreSQL object inventory")
	}
	if publicRelations != 0 || publicRoutines != 0 || managedSchemas != 0 || managedRoles != 0 {
		t.Fatal("PostgreSQL integration database is not an empty disposable boundary")
	}
}

func seedLegacyCatalog(t *testing.T, ctx context.Context, connection *pgx.Conn) {
	t.Helper()
	if _, err := connection.Exec(ctx, `
ALTER SCHEMA public OWNER TO CURRENT_USER;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
`); err != nil {
		t.Fatal(err)
	}
	snapshots := make(map[string]snapshotResource, len(mallSnapshots))
	for _, resource := range mallSnapshots {
		snapshots[resource.Name] = resource
	}
	columnDefinitions := reviewedLegacyColumnDefinitions(t)
	for _, relation := range allLegacyResourceNames() {
		resource, snapshot := snapshots[relation]
		definitions := append([]string(nil), columnDefinitions[relation]...)
		if snapshot {
			target := qualified("public", relation)
			primary := make([]string, 0, len(resource.PrimaryKey))
			for _, column := range resource.PrimaryKey {
				primary = append(primary, quoteIdentifier(column))
			}
			definitions = append(definitions, "PRIMARY KEY ("+strings.Join(primary, ", ")+")")
			statement := "CREATE TABLE " + target + " (" + strings.Join(definitions, ", ") + ") USING heap TABLESPACE pg_default"
			if _, err := connection.Exec(ctx, statement); err != nil {
				t.Fatalf("create snapshot source %s: %v", relation, err)
			}
			for index, columns := range resource.Indexes {
				quotedColumns := make([]string, 0, len(columns))
				for _, column := range columns {
					quotedColumns = append(quotedColumns, quoteIdentifier(column))
				}
				statement := fmt.Sprintf(
					"CREATE INDEX %s ON %s USING btree (%s) TABLESPACE pg_default",
					quoteIdentifier(fmt.Sprintf("seed_%s_%d_idx", relation, index)),
					target,
					strings.Join(quotedColumns, ", "),
				)
				if _, err := connection.Exec(ctx, statement); err != nil {
					t.Fatalf("create snapshot source index %s: %v", relation, err)
				}
			}
			continue
		}
		statement := "CREATE TABLE " + qualified("public", relation) + " (" + strings.Join(definitions, ", ") + ") USING heap TABLESPACE pg_default"
		if _, err := connection.Exec(ctx, statement); err != nil {
			t.Fatalf("create legacy source %s: %v", relation, err)
		}
	}
}

func reviewedLegacyColumnDefinitions(t *testing.T) map[string][]string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate PostgreSQL integration fixture")
	}
	path := filepath.Clean(filepath.Join(
		filepath.Dir(currentFile),
		"../../../../../docs/reviews/r1shop-dev-legacy-source-columns.csv",
	))
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open reviewed legacy source metadata: %v", err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		t.Fatalf("read reviewed legacy source metadata header: %v", err)
	}
	positions := make(map[string]int, len(header))
	for index, name := range header {
		positions[name] = index
	}
	required := []string{
		"table_name", "attnum", "attname", "attisdropped", "format_type",
		"type_namespace", "type_kind", "attnotnull", "atthasdef", "default_expression",
		"attidentity", "attgenerated", "attstorage", "attcompression",
		"collation_namespace", "collation_name", "collation_provider",
		"collation_deterministic", "collation_encoding", "column_acl",
	}
	for _, name := range required {
		if _, exists := positions[name]; !exists {
			t.Fatalf("reviewed legacy source metadata is missing %s", name)
		}
	}
	allowedTypes := map[string]struct{}{
		"bigint": {}, "boolean": {}, "bytea": {}, "character varying(10)": {},
		"character varying(20)": {}, "character varying(40)": {},
		"character varying(50)": {}, "character varying(100)": {},
		"character varying(255)": {}, "integer": {}, "json": {},
		"numeric": {}, "numeric(10,2)": {}, "text": {}, "timestamp with time zone": {},
	}
	result := make(map[string][]string, len(legacySourceColumns))
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatalf("read reviewed legacy source metadata: %v", readErr)
		}
		value := func(name string) string { return record[positions[name]] }
		table := value("table_name")
		columns, reviewed := legacySourceColumns[table]
		attnum, parseErr := strconv.Atoi(value("attnum"))
		if !reviewed || parseErr != nil || attnum != len(result[table])+1 || attnum > len(columns) ||
			columns[attnum-1] != value("attname") || value("attisdropped") != "f" ||
			value("type_namespace") != "pg_catalog" || value("type_kind") != "b" ||
			value("atthasdef") != "f" || value("default_expression") != "" ||
			value("attidentity") != "" || value("attgenerated") != "" ||
			value("attcompression") != "" || value("column_acl") != "" {
			t.Fatalf("reviewed legacy source metadata row for %s is not the compiled safe shape", table)
		}
		columnType := value("format_type")
		if _, allowed := allowedTypes[columnType]; !allowed {
			t.Fatalf("reviewed legacy source metadata has unapproved type for %s", table)
		}
		definition := quoteIdentifier(value("attname")) + " " + columnType
		if value("collation_namespace") != "" || value("collation_name") != "" {
			if value("collation_namespace") != "pg_catalog" || value("collation_name") != "default" ||
				value("collation_provider") != "d" || value("collation_deterministic") != "t" ||
				value("collation_encoding") != "-1" {
				t.Fatalf("reviewed legacy source metadata has unapproved collation for %s", table)
			}
			definition += ` COLLATE pg_catalog."default"`
		}
		if value("attnotnull") == "t" {
			definition += " NOT NULL"
		} else if value("attnotnull") != "f" {
			t.Fatalf("reviewed legacy source metadata has invalid nullability for %s", table)
		}
		result[table] = append(result[table], definition)
	}
	if len(result) != len(legacySourceColumns) {
		t.Fatalf("reviewed legacy source metadata tables=%d, want %d", len(result), len(legacySourceColumns))
	}
	for table, columns := range legacySourceColumns {
		if len(result[table]) != len(columns) {
			t.Fatalf("reviewed legacy source metadata columns for %s=%d, want %d", table, len(result[table]), len(columns))
		}
	}
	return result
}
