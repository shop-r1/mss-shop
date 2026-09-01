package importer

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/config"
	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/manifest"
)

const importAdvisoryLock int64 = 0x6d7373696d706f72

// Run performs one all-or-nothing import. It never changes the source
// database, and it refuses any target other than a marked, empty mss_shop_dev.
func Run(ctx context.Context, settings config.Config, output io.Writer) error {
	if output == nil {
		return errors.New("receipt output is not configured")
	}
	tables, err := manifest.Load()
	if err != nil {
		return errors.New("load compiled legacy import manifest failed")
	}
	sourceConfig, err := buildConnectionConfig(settings.Source, true)
	if err != nil {
		return err
	}
	targetConfig, err := buildConnectionConfig(settings.Target, false)
	if err != nil {
		return err
	}
	if sourceConfig.Host == targetConfig.Host || sourceConfig.Database == targetConfig.Database {
		return errors.New("source and target database boundaries overlap")
	}

	source, err := pgx.ConnectConfig(ctx, sourceConfig)
	if err != nil {
		return errors.New("connect read-only legacy source failed")
	}
	defer source.Close(context.WithoutCancel(ctx))
	if err := validateSourceConnection(ctx, source); err != nil {
		return err
	}

	target, err := pgx.ConnectConfig(ctx, targetConfig)
	if err != nil {
		return errors.New("connect isolated import target failed")
	}
	defer target.Close(context.WithoutCancel(ctx))

	sourceTx, err := source.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return errors.New("begin read-only repeatable legacy snapshot failed")
	}
	defer sourceTx.Rollback(context.WithoutCancel(ctx))
	for _, sql := range []string{
		"SET LOCAL transaction_read_only = on",
		"SET LOCAL event_triggers = false",
		"SET LOCAL search_path = pg_catalog",
		"SET LOCAL enable_seqscan = on",
		"SET LOCAL enable_indexscan = off",
		"SET LOCAL enable_bitmapscan = off",
		"SET LOCAL enable_indexonlyscan = off",
		"SET LOCAL enable_parallel_append = off",
		"SET LOCAL enable_parallel_hash = off",
		"SET LOCAL max_parallel_workers_per_gather = 0",
	} {
		if _, err := sourceTx.Exec(ctx, sql); err != nil {
			return errors.New("establish read-only legacy snapshot settings failed")
		}
	}
	if _, err := sourceTx.Exec(ctx, sourceLockSQL()); err != nil {
		return errors.New("lock reviewed source tables for snapshot failed")
	}
	sourceInventory, err := inspectSourceCatalog(ctx, sourceTx, tables)
	if err != nil {
		return err
	}
	if err := validateSourceCatalog(sourceInventory, tables); err != nil {
		return err
	}

	targetTx, err := target.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return errors.New("begin isolated target import transaction failed")
	}
	defer targetTx.Rollback(context.WithoutCancel(ctx))
	for _, sql := range []string{
		"SET LOCAL event_triggers = false",
		"SET LOCAL search_path = pg_catalog",
		"SET LOCAL enable_seqscan = on",
		"SET LOCAL enable_indexscan = off",
		"SET LOCAL enable_bitmapscan = off",
		"SET LOCAL enable_indexonlyscan = off",
		"SET LOCAL max_parallel_workers_per_gather = 0",
	} {
		if _, err := targetTx.Exec(ctx, sql); err != nil {
			return errors.New("establish isolated target settings failed")
		}
	}
	if _, err := targetTx.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock($1)", importAdvisoryLock); err != nil {
		return errors.New("acquire isolated import lock failed")
	}
	boundary, err := inspectTargetBoundary(ctx, targetTx)
	if err != nil {
		return err
	}
	if err := validateTargetBoundary(boundary); err != nil {
		return err
	}
	for _, sql := range []string{
		"ALTER SCHEMA public OWNER TO CURRENT_USER",
		"REVOKE ALL ON SCHEMA public FROM PUBLIC",
	} {
		if _, err := targetTx.Exec(ctx, sql); err != nil {
			return errors.New("establish target public schema ownership failed")
		}
	}

	createStatements, err := createTableStatements(tables)
	if err != nil {
		return err
	}
	for _, item := range createStatements {
		if _, err := targetTx.Exec(ctx, item.SQL); err != nil {
			return fmt.Errorf("create sanitized target table for %q failed", item.Name)
		}
	}

	evidence := make(map[string]tableEvidence, len(tables))
	sourceCopy := pgCopyTo{connection: sourceTx.Conn().PgConn()}
	targetCopy := pgCopyFrom{connection: targetTx.Conn().PgConn()}
	for _, table := range tables {
		if !table.CopyRows {
			sourceEvidence, err := hashBinaryCopy(ctx, sourceCopy, sourceCopySQL(table))
			if err != nil {
				return fmt.Errorf("hash structure-only source table %q failed", table.Name)
			}
			targetEvidence, err := hashBinaryCopy(
				ctx,
				pgCopyTo{connection: targetTx.Conn().PgConn()},
				sourceCopySQL(table),
			)
			if err != nil || targetEvidence.Rows != 0 {
				return fmt.Errorf("structure-only boundary for %q failed", table.Name)
			}
			evidence[table.Name] = tableEvidence{
				SourceRows:   sourceEvidence.Rows,
				TargetRows:   targetEvidence.Rows,
				SourceSHA256: hex.EncodeToString(sourceEvidence.SHA256[:]),
				TargetSHA256: hex.EncodeToString(targetEvidence.SHA256[:]),
			}
			continue
		}
		copyEvidence, err := streamBinaryCopy(
			ctx,
			sourceCopy,
			targetCopy,
			sourceCopySQL(table),
			targetCopySQL(table),
		)
		if err != nil {
			return fmt.Errorf("copy reviewed source table %q failed", table.Name)
		}
		targetEvidence, err := hashBinaryCopy(
			ctx,
			pgCopyTo{connection: targetTx.Conn().PgConn()},
			sourceCopySQL(table),
		)
		if err != nil || targetEvidence.Rows != copyEvidence.Rows ||
			subtle.ConstantTimeCompare(targetEvidence.SHA256[:], copyEvidence.SHA256[:]) != 1 {
			return fmt.Errorf("verify copied stream for %q failed", table.Name)
		}
		evidence[table.Name] = tableEvidence{
			SourceRows:   copyEvidence.Rows,
			TargetRows:   targetEvidence.Rows,
			SourceSHA256: hex.EncodeToString(copyEvidence.SHA256[:]),
			TargetSHA256: hex.EncodeToString(targetEvidence.SHA256[:]),
		}
	}

	indexStatements, err := createIndexStatements(tables)
	if err != nil {
		return err
	}
	for _, item := range indexStatements {
		if _, err := targetTx.Exec(ctx, item.SQL); err != nil {
			return fmt.Errorf("create reviewed target index for %q failed", item.Name)
		}
	}
	receipt, err := buildReceipt(tables, evidence)
	if err != nil {
		return err
	}
	encodedReceipt, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return errors.New("encode import receipt failed")
	}
	encodedReceipt = append(encodedReceipt, '\n')

	if err := sourceTx.Commit(ctx); err != nil {
		return errors.New("close read-only legacy snapshot failed")
	}
	marker := importedMarkerPrefix + receipt.SHA256
	commentSQL := "COMMENT ON DATABASE " + quoteIdentifier(targetDatabase) + " IS " + quoteLiteral(marker)
	if _, err := targetTx.Exec(ctx, commentSQL); err != nil {
		return errors.New("write isolated import marker failed")
	}
	if err := targetTx.Commit(ctx); err != nil {
		return errors.New("commit isolated target import failed")
	}
	var storedMarker string
	if err := target.QueryRow(ctx, `
SELECT COALESCE(pg_catalog.shobj_description(
  (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database()),
  'pg_database'
), '')
`).Scan(&storedMarker); err != nil || storedMarker != marker {
		return errors.New("verify committed isolated import marker failed")
	}
	if err := writeCompleteReceipt(output, encodedReceipt); err != nil {
		return errors.New("write safe import receipt failed")
	}
	return nil
}

func validateSourceConnection(ctx context.Context, connection *pgx.Conn) error {
	var (
		postgres17            bool
		eventTriggersDisabled bool
		readOnly              bool
		ssl                   bool
		databaseExact         bool
		sessionIdentityExact  bool
		indexScanDisabled     bool
		bitmapScanDisabled    bool
		indexOnlyDisabled     bool
		parallelDisabled      bool
		extensions            []string
	)
	if err := connection.QueryRow(ctx, `
SELECT current_setting('server_version_num')::integer / 10000 = 17,
       NOT current_setting('event_triggers')::boolean,
       current_setting('transaction_read_only')::boolean,
       COALESCE((SELECT ssl FROM pg_catalog.pg_stat_ssl WHERE pid = pg_catalog.pg_backend_pid()), false),
       current_database() = 'r1shop_dev',
       session_user = current_user,
       NOT current_setting('enable_indexscan')::boolean,
       NOT current_setting('enable_bitmapscan')::boolean,
       NOT current_setting('enable_indexonlyscan')::boolean,
       current_setting('max_parallel_workers_per_gather')::integer = 0
`).Scan(
		&postgres17,
		&eventTriggersDisabled,
		&readOnly,
		&ssl,
		&databaseExact,
		&sessionIdentityExact,
		&indexScanDisabled,
		&bitmapScanDisabled,
		&indexOnlyDisabled,
		&parallelDisabled,
	); err != nil {
		return errors.New("verify read-only legacy source connection failed")
	}
	if err := connection.QueryRow(ctx, `
SELECT COALESCE(array_agg(extname::text ORDER BY extname), ARRAY[]::text[])
FROM pg_catalog.pg_extension
`).Scan(&extensions); err != nil {
		return errors.New("verify legacy source extensions failed")
	}
	if !postgres17 || !eventTriggersDisabled || !readOnly || ssl || !databaseExact ||
		!sessionIdentityExact || !indexScanDisabled || !bitmapScanDisabled ||
		!indexOnlyDisabled || !parallelDisabled ||
		!containsString(extensions, "plpgsql") || !containsString(extensions, "timescaledb") {
		return errors.New("legacy source connection is outside the read-only reviewed boundary")
	}
	return nil
}

func sourceLockSQL() string {
	names := manifest.ImportNames()
	names = append(names, manifest.SourceIdentityNames()...)
	sort.Strings(names)
	qualified := make([]string, 0, len(names))
	for _, name := range names {
		qualified = append(qualified, "ONLY "+qualifiedTable(name))
	}
	return "LOCK TABLE " + joinComma(qualified) + " IN ACCESS SHARE MODE"
}

func joinComma(values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ", "
		}
		result += value
	}
	return result
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
