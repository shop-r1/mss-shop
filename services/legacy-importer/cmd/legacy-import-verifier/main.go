// Command legacy-import-verifier independently verifies the completed import
// receipt against the new isolated PostgreSQL database. It has no legacy
// source or Kubernetes client dependency and performs catalog/COPY reads only.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"

	"github.com/shop-r1/mss-shop/services/internal/legacyreceipt"
	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/manifest"
)

const (
	postgresUsernameEnvironment   = "MSS_VERIFY_POSTGRES_USERNAME"
	postgresPasswordEnvironment   = "MSS_VERIFY_POSTGRES_PASSWORD"
	postgresCAEnvironment         = "MSS_VERIFY_POSTGRES_TLS_CA_FILE"
	postgresServerNameEnvironment = "MSS_VERIFY_POSTGRES_TLS_SERVER_NAME"
	receiptPathEnvironment        = "MSS_VERIFY_RECEIPT_FILE"
	receiptSHAEnvironment         = "MSS_VERIFY_RECEIPT_SHA256"
	podNameEnvironment            = "POD_NAME"
	podNamespaceEnvironment       = "POD_NAMESPACE"
	podUIDEnvironment             = "POD_UID"
	imageRevisionEnvironment      = "MSS_IMAGE_REVISION"
	imageDigestEnvironment        = "MSS_IMAGE_DIGEST"
	postgresHost                  = "mss-shop-postgres.mss-shop-dev.svc"
	postgresDatabase              = "mss_shop_dev"
	markerPrefix                  = "mss-shop-isolated-dev:legacy-import:v1:"
	namespace                     = "mss-shop-dev"
	imageRepository               = "ghcr.io/shop-r1/mss-shop-legacy-importer"
	verifierPodPrefix             = "mss-shop-legacy-verify-"
	observedRevisionPrefix        = 32
)

var (
	identity     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	podName      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	podUID       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)
	imageDigest  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	receiptSHA   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type configuration struct {
	postgresDSN                                 string
	tlsConfig                                   *tls.Config
	receipt                                     legacyreceipt.Receipt
	podName, podUID, imageRevision, imageDigest string
}
type successRecord struct {
	Version         string `json:"version"`
	TargetDatabase  string `json:"targetDatabase"`
	DatabaseMarker  string `json:"databaseMarker"`
	ReceiptSHA256   string `json:"receiptSHA256"`
	ManifestSHA256  string `json:"manifestSHA256"`
	SchemaSHA256    string `json:"schemaSHA256"`
	TableCount      int    `json:"tableCount"`
	OrdersRows      int64  `json:"ordersRows"`
	OrderGoodsRows  int64  `json:"orderGoodsRows"`
	Namespace       string `json:"namespace"`
	PodName         string `json:"podName"`
	PodUID          string `json:"podUID"`
	Revision        string `json:"revision"`
	ImageRepository string `json:"imageRepository"`
	ImageDigest     string `json:"imageDigest"`
	ImageReference  string `json:"imageReference"`
}

type failureRecord struct {
	Version  string `json:"version"`
	Verified bool   `json:"verified"`
	Failure  string `json:"failure"`
}

type targetIndex struct {
	Table    string
	Name     string
	Primary  bool
	Unique   bool
	Columns  []string
	Reviewed bool
}

type targetConstraint struct {
	Table string
	Name  string
	Type  string
}

type targetSchema struct {
	Columns     map[string][]manifest.Column
	Indexes     []targetIndex
	Constraints []targetConstraint
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
		return errors.New("verification output is unavailable")
	}
	settings, err := loadConfiguration(arguments, lookup)
	var tables []manifest.Table
	if err == nil {
		tables, err = manifest.Load()
	}
	if err == nil {
		err = validateReceipt(settings.receipt, tables)
	}
	if err == nil {
		err = verifyDatabase(ctx, settings, tables)
	}
	if err == nil {
		response := successRecord{
			Version: "mss-shop-disposable-verification/v1", TargetDatabase: postgresDatabase,
			DatabaseMarker: markerPrefix + settings.receipt.SHA256, ReceiptSHA256: settings.receipt.SHA256,
			ManifestSHA256: settings.receipt.ManifestSHA256, SchemaSHA256: settings.receipt.SchemaSHA256,
			TableCount: len(tables), OrdersRows: tableRows(settings.receipt, manifest.OrdersTable),
			OrderGoodsRows: tableRows(settings.receipt, manifest.OrderGoodsTable), Namespace: namespace,
			PodName: settings.podName, PodUID: settings.podUID, Revision: settings.imageRevision,
			ImageRepository: imageRepository, ImageDigest: settings.imageDigest,
			ImageReference: imageRepository + ":" + settings.imageRevision + "@" + settings.imageDigest,
		}
		if writeErr := writeJSON(output, response); writeErr != nil {
			return writeErr
		}
	} else {
		if writeErr := writeJSON(output, failureRecord{Version: "mss-shop-disposable-verification-failure/v1", Verified: false, Failure: safeFailure(err)}); writeErr != nil {
			return writeErr
		}
	}
	return err
}

func loadConfiguration(arguments []string, lookup func(string) (string, bool)) (configuration, error) {
	if len(arguments) != 0 || lookup == nil {
		return configuration{}, errors.New("invalid invocation")
	}
	username, usernameSet := lookup(postgresUsernameEnvironment)
	password, passwordSet := lookup(postgresPasswordEnvironment)
	caPath, caSet := lookup(postgresCAEnvironment)
	serverName, serverNameSet := lookup(postgresServerNameEnvironment)
	path, pathSet := lookup(receiptPathEnvironment)
	expectedReceiptSHA, receiptSHASet := lookup(receiptSHAEnvironment)
	podNameValue, podNameSet := lookup(podNameEnvironment)
	podNamespace, podNamespaceSet := lookup(podNamespaceEnvironment)
	podUIDValue, podUIDSet := lookup(podUIDEnvironment)
	imageRevisionValue, revisionSet := lookup(imageRevisionEnvironment)
	imageDigestValue, digestSet := lookup(imageDigestEnvironment)
	if !usernameSet || !passwordSet || !caSet || !serverNameSet || !pathSet || !receiptSHASet || !podNameSet || !podNamespaceSet || !podUIDSet || !revisionSet || !digestSet || !identity.MatchString(username) || password == "" || strings.IndexByte(password, 0) >= 0 || serverName != postgresHost || !nonZeroReceiptSHA(expectedReceiptSHA) || !validVerifierPodName(podNameValue, imageRevisionValue) || podNamespace != namespace || !podUID.MatchString(podUIDValue) || !nonZeroRevision(imageRevisionValue) || !nonZeroImageDigest(imageDigestValue) {
		return configuration{}, errors.New("credentials unavailable")
	}
	tlsConfig, err := loadTLS(caPath, postgresHost)
	if err != nil {
		return configuration{}, errors.New("postgres tls unavailable")
	}
	dsn := fixedPostgresDSN(username, password, caPath)
	if err := validatePostgresDSN(dsn, tlsConfig, caPath); err != nil {
		return configuration{}, err
	}
	encoded, err := readReceipt(path)
	if err != nil {
		return configuration{}, err
	}
	receipt, err := legacyreceipt.Parse(encoded)
	if err != nil {
		return configuration{}, err
	}
	if receipt.SHA256 != expectedReceiptSHA {
		return configuration{}, errors.New("receipt binding rejected")
	}
	return configuration{postgresDSN: dsn, tlsConfig: tlsConfig, receipt: receipt, podName: podNameValue, podUID: podUIDValue, imageRevision: imageRevisionValue, imageDigest: imageDigestValue}, nil
}

func nonZeroRevision(value string) bool {
	return fullRevision.MatchString(value) && strings.Trim(value, "0") != ""
}

func nonZeroImageDigest(value string) bool {
	return imageDigest.MatchString(value) && strings.TrimPrefix(value, "sha256:") != strings.Repeat("0", 64)
}

func nonZeroReceiptSHA(value string) bool {
	return receiptSHA.MatchString(value) && strings.Trim(value, "0") != ""
}

func validVerifierPodName(name, revision string) bool {
	return nonZeroRevision(revision) && len(name) > len(verifierPodPrefix)+observedRevisionPrefix &&
		len(name) <= 63 && podName.MatchString(name) &&
		strings.HasPrefix(name, verifierPodPrefix+revision[:observedRevisionPrefix])
}

func fixedPostgresDSN(username, password, caPath string) string {
	query := url.Values{"sslmode": []string{"verify-full"}, "sslrootcert": []string{caPath}}
	return (&url.URL{Scheme: "postgres", User: url.UserPassword(username, password), Host: net.JoinHostPort(postgresHost, "5432"), Path: postgresDatabase, RawQuery: query.Encode()}).String()
}

func readReceipt(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("receipt path rejected")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, errors.New("receipt unavailable")
	}
	evidenceDirectory, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, errors.New("receipt path rejected")
	}
	relative, err := filepath.Rel(evidenceDirectory, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("receipt path rejected")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("receipt unavailable")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, errors.New("receipt unavailable")
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(encoded) == 0 || len(encoded) > 1<<20 {
		return nil, errors.New("receipt unavailable")
	}
	return encoded, nil
}

func validatePostgresDSN(value string, tlsConfig *tls.Config, caPath string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" || parsed.User == nil || parsed.User.Username() == "" || parsed.Fragment != "" || parsed.Hostname() != postgresHost || parsed.Port() != "5432" || parsed.Path != "/"+postgresDatabase || net.ParseIP(parsed.Hostname()) != nil {
		return errors.New("postgres endpoint rejected")
	}
	password, passwordSet := parsed.User.Password()
	query, queryErr := url.ParseQuery(parsed.RawQuery)
	if !passwordSet || password == "" || queryErr != nil || len(query) != 2 || len(query["sslmode"]) != 1 || len(query["sslrootcert"]) != 1 || query.Get("sslmode") != "verify-full" || query.Get("sslrootcert") != caPath || tlsConfig == nil || tlsConfig.ServerName != postgresHost {
		return errors.New("postgres endpoint rejected")
	}
	return nil
}

func loadTLS(path, serverName string) (*tls.Config, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
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

func validateReceipt(receipt legacyreceipt.Receipt, tables []manifest.Table) error {
	if err := legacyreceipt.Validate(receipt); err != nil {
		return err
	}
	encoded, err := json.Marshal(tables)
	if err != nil {
		return errors.New("compiled manifest unavailable")
	}
	digest := sha256.Sum256(encoded)
	if receipt.TargetDatabase != postgresDatabase || receipt.ManifestSHA256 != manifest.ReviewedColumnsSHA256 || receipt.SchemaSHA256 != hex.EncodeToString(digest[:]) || len(receipt.Tables) != len(tables) {
		return errors.New("receipt manifest does not match compiled inventory")
	}
	for index, table := range tables {
		evidence := receipt.Tables[index]
		mode := "copied"
		if !table.CopyRows {
			mode = "structure-only"
		}
		if evidence.Name != table.Name || evidence.Mode != mode || (!table.CopyRows && evidence.TargetRows != 0) {
			return errors.New("receipt table inventory does not match compiled manifest")
		}
	}
	if tableRows(receipt, manifest.OrdersTable) != 0 || tableRows(receipt, manifest.OrderGoodsTable) != 0 {
		return errors.New("receipt order boundary is nonempty")
	}
	return nil
}

func verifyDatabase(ctx context.Context, settings configuration, tables []manifest.Table) error {
	parsed, err := pgx.ParseConfig(settings.postgresDSN)
	if err != nil {
		return errors.New("postgres endpoint rejected")
	}
	parsed.TLSConfig, parsed.Fallbacks = settings.tlsConfig, nil
	parsed.RuntimeParams = map[string]string{
		"application_name":                "mss-shop-legacy-import-verifier",
		"default_transaction_read_only":   "on",
		"search_path":                     "pg_catalog",
		"enable_seqscan":                  "on",
		"enable_indexscan":                "off",
		"enable_bitmapscan":               "off",
		"enable_indexonlyscan":            "off",
		"enable_parallel_append":          "off",
		"enable_parallel_hash":            "off",
		"max_parallel_workers_per_gather": "0",
	}
	connection, err := pgx.ConnectConfig(ctx, parsed)
	if err != nil {
		return errors.New("postgres unavailable")
	}
	defer connection.Close(context.WithoutCancel(ctx))
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return errors.New("postgres transaction unavailable")
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, "SET LOCAL transaction_read_only = on"); err != nil {
		return errors.New("postgres transaction rejected")
	}
	var marker, version, database string
	var ssl bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE(pg_catalog.shobj_description((SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database()), 'pg_database'), ''), current_setting('server_version_num'), current_database(), COALESCE((SELECT ssl FROM pg_catalog.pg_stat_ssl WHERE pid = pg_catalog.pg_backend_pid()), false)`).Scan(&marker, &version, &database, &ssl); err != nil || marker != markerPrefix+settings.receipt.SHA256 || version != "170006" || database != postgresDatabase || !ssl {
		return errors.New("marker rejected")
	}
	var inventory []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(relation.relname::text ORDER BY relation.relname), ARRAY[]::text[]) FROM pg_catalog.pg_class AS relation JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace WHERE namespace.nspname = 'public' AND relation.relkind = 'r'`).Scan(&inventory); err != nil {
		return errors.New("inventory unavailable")
	}
	expected := make([]string, 0, len(tables))
	for _, table := range tables {
		expected = append(expected, table.Name)
	}
	sort.Strings(expected)
	if !sameStrings(inventory, expected) {
		return errors.New("inventory rejected")
	}
	schema, err := inspectTargetSchema(ctx, tx, tables)
	if err != nil || validateTargetSchema(schema, tables) != nil {
		return errors.New("schema rejected")
	}
	for _, table := range tables {
		rows, digest, err := hashBinaryCopy(ctx, tx, table)
		if err != nil {
			return errors.New("copy verification unavailable")
		}
		evidence := settings.receipt.Tables[tablePosition(tables, table.Name)]
		if rows != evidence.TargetRows || digest != evidence.TargetSHA256 {
			return errors.New("copy verification rejected")
		}
		if (table.Name == manifest.OrdersTable || table.Name == manifest.OrderGoodsTable) && rows != 0 {
			return errors.New("order boundary rejected")
		}
	}
	return tx.Commit(ctx)
}

func inspectTargetSchema(ctx context.Context, tx pgx.Tx, tables []manifest.Table) (targetSchema, error) {
	names := make([]string, 0, len(tables))
	for _, table := range tables {
		names = append(names, table.Name)
	}
	schema := targetSchema{Columns: make(map[string][]manifest.Column, len(tables))}
	rows, err := tx.Query(ctx, `
SELECT relation.relname::text, attribute.attnum::integer, attribute.attname::text,
       attribute.attisdropped, pg_catalog.format_type(attribute.atttypid, attribute.atttypmod),
       type_namespace.nspname::text, type_record.typname::text, type_record.typtype::text,
       attribute.atttypmod::integer, attribute.attnotnull, attribute.atthasdef,
       COALESCE(pg_catalog.pg_get_expr(default_record.adbin, default_record.adrelid, true), ''),
       attribute.attidentity::text, attribute.attgenerated::text, attribute.attstorage::text,
       attribute.attcompression::text, attribute.atthasmissing,
       COALESCE(collation_namespace.nspname::text, ''), COALESCE(collation.collname::text, ''),
       COALESCE(collation.collprovider::text, ''), COALESCE(collation.collisdeterministic, false),
       COALESCE(collation.collencoding, 0), COALESCE(attribute.attacl::text, '')
FROM pg_catalog.pg_class AS relation
JOIN pg_catalog.pg_namespace AS relation_namespace ON relation_namespace.oid = relation.relnamespace
JOIN pg_catalog.pg_attribute AS attribute ON attribute.attrelid = relation.oid
JOIN pg_catalog.pg_type AS type_record ON type_record.oid = attribute.atttypid
JOIN pg_catalog.pg_namespace AS type_namespace ON type_namespace.oid = type_record.typnamespace
LEFT JOIN pg_catalog.pg_attrdef AS default_record ON default_record.adrelid = relation.oid AND default_record.adnum = attribute.attnum
LEFT JOIN pg_catalog.pg_collation AS collation ON collation.oid = attribute.attcollation
LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation.collnamespace
WHERE relation_namespace.nspname = 'public' AND relation.relname = ANY($1::text[]) AND attribute.attnum > 0
ORDER BY relation.relname, attribute.attnum`, names)
	if err != nil {
		return targetSchema{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		var column manifest.Column
		if err := rows.Scan(&table, &column.Position, &column.Name, &column.Dropped, &column.Type, &column.TypeNamespace, &column.TypeName, &column.TypeKind, &column.TypeMod, &column.NotNull, &column.HasDefault, &column.DefaultExpression, &column.Identity, &column.Generated, &column.Storage, &column.Compression, &column.HasMissing, &column.CollationNamespace, &column.Collation, &column.CollationProvider, &column.CollationDeterministic, &column.CollationEncoding, &column.ColumnACL); err != nil {
			return targetSchema{}, err
		}
		schema.Columns[table] = append(schema.Columns[table], column)
	}
	if rows.Err() != nil {
		return targetSchema{}, rows.Err()
	}
	indexRows, err := tx.Query(ctx, `
SELECT relation.relname::text, index_relation.relname::text, index_record.indisprimary, index_record.indisunique,
       COALESCE((SELECT array_agg(attribute.attname::text ORDER BY key.ordinality) FILTER (WHERE attribute.attname IS NOT NULL)
                   FROM unnest(index_record.indkey) WITH ORDINALITY AS key(attnum, ordinality)
                   LEFT JOIN pg_catalog.pg_attribute AS attribute ON attribute.attrelid = relation.oid AND attribute.attnum = key.attnum), ARRAY[]::text[]),
       access_method.amname = 'btree' AND index_record.indnkeyatts = index_record.indnatts
         AND index_record.indexprs IS NULL AND index_record.indpred IS NULL
         AND index_record.indisvalid AND index_record.indisready AND index_record.indislive
FROM pg_catalog.pg_index AS index_record
JOIN pg_catalog.pg_class AS relation ON relation.oid = index_record.indrelid
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
JOIN pg_catalog.pg_class AS index_relation ON index_relation.oid = index_record.indexrelid
JOIN pg_catalog.pg_am AS access_method ON access_method.oid = index_relation.relam
WHERE namespace.nspname = 'public' AND relation.relname = ANY($1::text[])
ORDER BY relation.relname, index_relation.relname`, names)
	if err != nil {
		return targetSchema{}, err
	}
	defer indexRows.Close()
	for indexRows.Next() {
		var index targetIndex
		if err := indexRows.Scan(&index.Table, &index.Name, &index.Primary, &index.Unique, &index.Columns, &index.Reviewed); err != nil {
			return targetSchema{}, err
		}
		schema.Indexes = append(schema.Indexes, index)
	}
	if indexRows.Err() != nil {
		return targetSchema{}, indexRows.Err()
	}
	constraintRows, err := tx.Query(ctx, `
SELECT relation.relname::text, constraint.conname::text, constraint.contype::text
FROM pg_catalog.pg_constraint AS constraint
JOIN pg_catalog.pg_class AS relation ON relation.oid = constraint.conrelid
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'public' AND relation.relname = ANY($1::text[])
ORDER BY relation.relname, constraint.conname`, names)
	if err != nil {
		return targetSchema{}, err
	}
	defer constraintRows.Close()
	for constraintRows.Next() {
		var constraint targetConstraint
		if err := constraintRows.Scan(&constraint.Table, &constraint.Name, &constraint.Type); err != nil {
			return targetSchema{}, err
		}
		schema.Constraints = append(schema.Constraints, constraint)
	}
	if constraintRows.Err() != nil {
		return targetSchema{}, constraintRows.Err()
	}
	return schema, nil
}

func validateTargetSchema(actual targetSchema, tables []manifest.Table) error {
	if len(actual.Columns) != len(tables) {
		return errors.New("column inventory differs")
	}
	expectedIndexes := make([]targetIndex, 0)
	expectedConstraints := make([]targetConstraint, 0)
	for _, table := range tables {
		if columns, exists := actual.Columns[table.Name]; !exists || !equalColumns(columns, table.Columns) {
			return errors.New("column definition differs")
		}
		for position, index := range table.Indexes {
			name := "mss_import_" + table.Name + "_" + twoDigits(position) + "_idx"
			if index.Primary {
				name = "mss_import_" + table.Name + "_pkey"
				expectedConstraints = append(expectedConstraints, targetConstraint{Table: table.Name, Name: name, Type: "p"})
			}
			expectedIndexes = append(expectedIndexes, targetIndex{Table: table.Name, Name: name, Primary: index.Primary, Unique: index.Primary, Columns: append([]string(nil), index.Columns...), Reviewed: true})
		}
	}
	sortTargetIndexes(expectedIndexes)
	sortTargetIndexes(actual.Indexes)
	if len(actual.Indexes) != len(expectedIndexes) {
		return errors.New("index inventory differs")
	}
	for position := range expectedIndexes {
		if actual.Indexes[position].Table != expectedIndexes[position].Table || actual.Indexes[position].Name != expectedIndexes[position].Name || actual.Indexes[position].Primary != expectedIndexes[position].Primary || actual.Indexes[position].Unique != expectedIndexes[position].Unique || actual.Indexes[position].Reviewed != expectedIndexes[position].Reviewed || !sameStrings(actual.Indexes[position].Columns, expectedIndexes[position].Columns) {
			return errors.New("index definition differs")
		}
	}
	sortConstraints(expectedConstraints)
	sortConstraints(actual.Constraints)
	if len(actual.Constraints) != len(expectedConstraints) {
		return errors.New("constraint inventory differs")
	}
	for position := range expectedConstraints {
		if actual.Constraints[position] != expectedConstraints[position] {
			return errors.New("constraint definition differs")
		}
	}
	return nil
}

func equalColumns(left, right []manifest.Column) bool {
	if len(left) != len(right) {
		return false
	}
	for position := range left {
		if left[position] != right[position] {
			return false
		}
	}
	return true
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + string(rune('0'+value))
	}
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}
func sortTargetIndexes(indexes []targetIndex) {
	sort.Slice(indexes, func(left, right int) bool {
		if indexes[left].Table != indexes[right].Table {
			return indexes[left].Table < indexes[right].Table
		}
		return indexes[left].Name < indexes[right].Name
	})
}
func sortConstraints(constraints []targetConstraint) {
	sort.Slice(constraints, func(left, right int) bool {
		if constraints[left].Table != constraints[right].Table {
			return constraints[left].Table < constraints[right].Table
		}
		return constraints[left].Name < constraints[right].Name
	})
}

func hashBinaryCopy(ctx context.Context, tx pgx.Tx, table manifest.Table) (int64, string, error) {
	hasher := sha256.New()
	tag, err := tx.Conn().PgConn().CopyTo(ctx, hasher, copySQL(table))
	if err != nil || tag.RowsAffected() < 0 {
		return 0, "", errors.New("copy failed")
	}
	return tag.RowsAffected(), hex.EncodeToString(hasher.Sum(nil)), nil
}

func copySQL(table manifest.Table) string {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		columns = append(columns, quoteIdentifier(column.Name))
	}
	return "COPY (SELECT " + strings.Join(columns, ", ") + " FROM ONLY \"public\"." + quoteIdentifier(table.Name) + ") TO STDOUT (FORMAT binary)"
}
func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
func tablePosition(tables []manifest.Table, name string) int {
	for index, table := range tables {
		if table.Name == name {
			return index
		}
	}
	return -1
}
func tableRows(receipt legacyreceipt.Receipt, name string) int64 {
	for _, table := range receipt.Tables {
		if table.Name == name {
			return table.TargetRows
		}
	}
	return -1
}
func safeFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case strings.Contains(err.Error(), "receipt"):
		return "receipt"
	case strings.Contains(err.Error(), "marker"):
		return "marker"
	case strings.Contains(err.Error(), "inventory"):
		return "inventory"
	case strings.Contains(err.Error(), "copy"):
		return "copy"
	case strings.Contains(err.Error(), "order"):
		return "orders"
	case strings.Contains(err.Error(), "postgres"):
		return "postgres"
	default:
		return "configuration"
	}
}
func writeJSON(output io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode verification record")
	}
	encoded = append(encoded, '\n')
	written, err := output.Write(encoded)
	if err != nil || written != len(encoded) {
		return errors.New("write verification record")
	}
	return nil
}
