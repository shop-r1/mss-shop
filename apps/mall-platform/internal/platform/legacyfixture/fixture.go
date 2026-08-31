// Package legacyfixture creates a deliberately local-only SQLite surface for
// browser acceptance of the reviewed legacy Admin resources. It is not a data
// migration mechanism and has no PostgreSQL or remote-database mode.
package legacyfixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	// DatabaseFilename is intentionally fixed so this helper cannot be pointed
	// at an arbitrary database file.
	DatabaseFilename  = "mss-boot-admin-local.db"
	moduleDeclaration = "module github.com/shop-r1/mss-shop/apps/mall-platform"
)

var sqliteHeader = []byte("SQLite format 3\x00")

// Options contains every value that must be explicit before the fixture can
// write. RootDir is supplied by the command from its current working directory
// and is checked against the mall-platform module marker.
type Options struct {
	RootDir        string
	DatabasePath   string
	LegacyTenantID string
	Confirmed      bool
}

// Result is safe to print: it contains no credentials or database contents.
type Result struct {
	DatabasePath  string
	TableCount    int
	CreatedTables int
	InsertedRows  int64
}

// Apply creates missing reviewed tables and inserts non-sensitive demo rows.
// All database work happens in one transaction and uses only forward
// CREATE TABLE IF NOT EXISTS / INSERT ... ON CONFLICT DO NOTHING statements.
func Apply(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("local legacy fixture: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("local legacy fixture: context is unavailable: %w", err)
	}
	target, err := validateTarget(options)
	if err != nil {
		return Result{}, err
	}
	binding := fixedbinding.Binding{
		TenantID:       "local-ui-control",
		AdminTenantID:  fixedbinding.MSS137AdminTenantID,
		LegacyTenantID: options.LegacyTenantID,
		BusinessSchema: "main",
		SharedSchema:   "shared_demo",
	}
	if err := binding.Validate(); err != nil {
		return Result{}, fmt.Errorf("local legacy fixture: invalid legacy tenant ID: %w", err)
	}

	database, err := gorm.Open(sqlite.Open(target), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return Result{}, fmt.Errorf("local legacy fixture: open approved SQLite file: %w", err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		return Result{}, fmt.Errorf("local legacy fixture: access SQLite connection: %w", err)
	}
	defer sqlDatabase.Close()
	sqlDatabase.SetMaxOpenConns(1)

	if err := verifyOpenedTarget(ctx, database, target); err != nil {
		return Result{}, err
	}

	registry := legacydb.DefaultRegistry()
	definitions := registry.All()
	if len(definitions) != legacydb.ExpectedMallResourceCount {
		return Result{}, fmt.Errorf(
			"local legacy fixture: reviewed resource count is %d, want %d",
			len(definitions), legacydb.ExpectedMallResourceCount,
		)
	}
	existing, err := inspectExistingRelations(ctx, database, definitions)
	if err != nil {
		return Result{}, err
	}

	transaction := database.WithContext(ctx).Begin()
	if transaction.Error != nil {
		return Result{}, fmt.Errorf("local legacy fixture: begin transaction: %w", transaction.Error)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback().Error
		}
	}()

	for _, definition := range definitions {
		if err := transaction.Exec(createTableStatement(definition)).Error; err != nil {
			return Result{}, fmt.Errorf("local legacy fixture: create %s: %w", definition.Resource.Name, err)
		}
	}

	var inserted int64
	for _, row := range demoRows(options.LegacyTenantID) {
		definition, ok := registry.Lookup(row.Resource)
		if !ok {
			return Result{}, fmt.Errorf("local legacy fixture: demo resource %q is not reviewed", row.Resource)
		}
		statement, arguments, err := insertStatement(definition, row.Values)
		if err != nil {
			return Result{}, err
		}
		result := transaction.Exec(statement, arguments...)
		if result.Error != nil {
			return Result{}, fmt.Errorf("local legacy fixture: seed %s: %w", row.Resource, result.Error)
		}
		inserted += result.RowsAffected
	}

	if err := legacydb.VerifyReadiness(ctx, transaction, binding, registry); err != nil {
		return Result{}, fmt.Errorf("local legacy fixture: readiness after forward setup: %w", err)
	}
	if err := transaction.Commit().Error; err != nil {
		return Result{}, fmt.Errorf("local legacy fixture: commit: %w", err)
	}
	committed = true

	return Result{
		DatabasePath:  target,
		TableCount:    len(definitions),
		CreatedTables: len(definitions) - len(existing),
		InsertedRows:  inserted,
	}, nil
}

func validateTarget(options Options) (string, error) {
	if !options.Confirmed {
		return "", errors.New("local legacy fixture: --confirm-local-ui-fixture is required")
	}
	if options.LegacyTenantID == "" {
		return "", errors.New("local legacy fixture: --legacy-tenant-id is required")
	}
	if options.RootDir == "" || options.DatabasePath == "" {
		return "", errors.New("local legacy fixture: explicit root and SQLite file are required")
	}
	if looksLikeDSN(options.DatabasePath) {
		return "", errors.New("local legacy fixture: DSNs and database URLs are forbidden")
	}

	root, err := filepath.Abs(options.RootDir)
	if err != nil {
		return "", fmt.Errorf("local legacy fixture: resolve root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("local legacy fixture: resolve local root: %w", err)
	}
	if err := verifyMallModuleRoot(root); err != nil {
		return "", err
	}

	target, err := filepath.Abs(options.DatabasePath)
	if err != nil {
		return "", fmt.Errorf("local legacy fixture: resolve SQLite file: %w", err)
	}
	if filepath.Base(target) != DatabaseFilename {
		return "", fmt.Errorf("local legacy fixture: target must be exactly %s", filepath.Join(root, DatabaseFilename))
	}

	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", fmt.Errorf("local legacy fixture: resolve SQLite parent: %w", err)
	}
	if parent != root {
		return "", errors.New("local legacy fixture: SQLite parent escapes the mall-platform root")
	}
	target = filepath.Join(parent, DatabaseFilename)

	info, err := os.Lstat(target)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("local legacy fixture: symbolic-link database targets are forbidden")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("local legacy fixture: database target must be a regular file")
		}
		if err := verifyExistingSQLiteFile(target, info.Size()); err != nil {
			return "", err
		}
	case errors.Is(err, os.ErrNotExist):
		return "", errors.New("local legacy fixture: approved SQLite file does not exist; run mss setup first")
	default:
		return "", fmt.Errorf("local legacy fixture: inspect SQLite target: %w", err)
	}
	return target, nil
}

func verifyMallModuleRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("local legacy fixture: inspect root: %w", err)
	}
	if !info.IsDir() {
		return errors.New("local legacy fixture: root must be a directory")
	}
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("local legacy fixture: read mall-platform module marker: %w", err)
	}
	firstLine := strings.TrimSpace(strings.SplitN(string(contents), "\n", 2)[0])
	if firstLine != moduleDeclaration {
		return errors.New("local legacy fixture: current directory is not the mall-platform module root")
	}
	if _, err := os.Stat(filepath.Join(root, ".mss", "project.yaml")); err != nil {
		return fmt.Errorf("local legacy fixture: mall-platform MSS marker is unavailable: %w", err)
	}
	return nil
}

func looksLikeDSN(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return lower == ":memory:" || strings.Contains(lower, "://") ||
		strings.HasPrefix(lower, "file:") || strings.ContainsAny(lower, "?\x00") ||
		strings.HasPrefix(lower, "postgres") || strings.Contains(lower, "host=") ||
		strings.Contains(lower, "dbname=")
}

func verifyExistingSQLiteFile(path string, size int64) error {
	if size < int64(len(sqliteHeader)) {
		return errors.New("local legacy fixture: existing target is not a valid SQLite database")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("local legacy fixture: inspect existing SQLite header: %w", err)
	}
	defer file.Close()
	header := make([]byte, len(sqliteHeader))
	if _, err := file.Read(header); err != nil {
		return fmt.Errorf("local legacy fixture: read existing SQLite header: %w", err)
	}
	if !bytes.Equal(header, sqliteHeader) {
		return errors.New("local legacy fixture: existing target is not a valid SQLite database")
	}
	return nil
}

func verifyOpenedTarget(ctx context.Context, database *gorm.DB, target string) error {
	if database.Dialector.Name() != "sqlite" {
		return fmt.Errorf("local legacy fixture: dialect %q is forbidden", database.Dialector.Name())
	}
	var databases []struct {
		Name string `gorm:"column:name"`
		File string `gorm:"column:file"`
	}
	if err := database.WithContext(ctx).Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		return fmt.Errorf("local legacy fixture: inspect opened SQLite database: %w", err)
	}
	if len(databases) != 1 || databases[0].Name != "main" {
		return errors.New("local legacy fixture: attached or unexpected SQLite databases are forbidden")
	}
	opened, err := filepath.Abs(databases[0].File)
	if err != nil || filepath.Clean(opened) != filepath.Clean(target) {
		return errors.New("local legacy fixture: opened SQLite file does not match the approved target")
	}
	return nil
}

func inspectExistingRelations(ctx context.Context, database *gorm.DB, definitions []legacydb.Definition) (map[string]struct{}, error) {
	existing := make(map[string]struct{})
	for _, definition := range definitions {
		var object struct {
			Type string `gorm:"column:type"`
		}
		result := database.WithContext(ctx).Raw(
			"SELECT type FROM sqlite_master WHERE name = ? LIMIT 1",
			definition.Resource.Name,
		).Scan(&object)
		if result.Error != nil {
			return nil, fmt.Errorf("local legacy fixture: inspect %s: %w", definition.Resource.Name, result.Error)
		}
		if result.RowsAffected == 0 || object.Type == "" {
			continue
		}
		if object.Type != "table" {
			return nil, fmt.Errorf("local legacy fixture: %s exists but is not a table", definition.Resource.Name)
		}
		existing[definition.Resource.Name] = struct{}{}
		available, err := sqliteColumns(ctx, database, definition.Resource.Name)
		if err != nil {
			return nil, err
		}
		missing := make([]string, 0)
		for _, column := range definition.Resource.Columns {
			if _, ok := available[column.Name]; !ok {
				missing = append(missing, column.Name)
			}
		}
		if len(missing) != 0 {
			sort.Strings(missing)
			return nil, fmt.Errorf(
				"local legacy fixture: existing %s is incompatible; missing columns %v",
				definition.Resource.Name, missing,
			)
		}
	}
	return existing, nil
}

func sqliteColumns(ctx context.Context, database *gorm.DB, table string) (map[string]struct{}, error) {
	var rows []struct {
		Name string `gorm:"column:name"`
	}
	statement := "PRAGMA main.table_info(" + quoteIdentifier(table) + ")"
	if err := database.WithContext(ctx).Raw(statement).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("local legacy fixture: inspect columns for %s: %w", table, err)
	}
	columns := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		columns[row.Name] = struct{}{}
	}
	return columns, nil
}

func createTableStatement(definition legacydb.Definition) string {
	columns := make([]string, 0, len(definition.Resource.Columns)+1)
	for _, column := range definition.Resource.Columns {
		columns = append(columns, quoteIdentifier(column.Name)+" "+sqliteColumnType(column.Type))
	}
	primary := make([]string, 0, len(definition.Resource.PrimaryKey))
	for _, name := range definition.Resource.PrimaryKey {
		primary = append(primary, quoteIdentifier(name))
	}
	columns = append(columns, "PRIMARY KEY ("+strings.Join(primary, ", ")+")")
	return "CREATE TABLE IF NOT EXISTS \"main\"." + quoteIdentifier(definition.Resource.Name) +
		" (" + strings.Join(columns, ", ") + ")"
}

func sqliteColumnType(columnType legacydb.ColumnType) string {
	switch columnType {
	case legacydb.ColumnNumber:
		return "REAL"
	case legacydb.ColumnBoolean:
		return "INTEGER"
	default:
		return "TEXT"
	}
}

type demoRow struct {
	Resource string
	Values   map[string]any
}

func demoRows(tenantID string) []demoRow {
	const createdAt = "2026-09-01T00:00:00Z"
	return []demoRow{
		{Resource: "function_circles", Values: map[string]any{
			"id": "910000000000000001", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "title": "本地演示推荐位", "type": "banner",
			"status": 1, "bg_color": "#1677ff", "content": "仅用于本地 UI 验收", "sort": 10,
		}},
		{Resource: "message_events", Values: map[string]any{
			"id": "910000000000000002", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "name": "本地订单创建事件", "app": "mall",
			"object": "order", "event": "created", "status": 1,
		}},
		{Resource: "message_templates", Values: map[string]any{
			"id": "910000000000000003", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "event_id": "910000000000000002", "name": "本地订单通知模板",
			"title": "订单已创建", "content": "这是一条不包含用户信息的本地演示消息。", "status": 1,
		}},
		{Resource: "show_categories", Values: map[string]any{
			"id": "910000000000000004", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "name": "本地精选", "status": 1,
			"description": "本地 UI 验收分类", "sort": 10,
		}},
		{Resource: "goods", Values: map[string]any{
			"id": "910000000000000005", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "show_category_id": "910000000000000004",
			"alias": "本地演示商品", "bar_code": "LOCAL-DEMO-001", "status": 1,
			"inventory": 20, "metadata": `{"fixture":"local-ui"}`,
		}},
		{Resource: "members", Values: map[string]any{
			"id": "910000000000000006", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "username": "local-demo-member", "nickname": "本地演示会员",
			"status": 1, "metadata": `{"fixture":"local-ui"}`,
		}},
		{Resource: "shipping_warehouses", Values: map[string]any{
			"id": "910000000000000007", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "name": "本地演示发货仓", "currency": "CNY",
			"region": "LOCAL", "address": "本地测试地址", "status": 1,
		}},
		{Resource: "orders", Values: map[string]any{
			"id": "910000000000000008", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "member_id": "910000000000000006", "order_status": "created",
			"money": 19.9, "currency": "CNY", "goods_name": "本地演示商品",
			"warehouse_id": "910000000000000007", "remark": "仅用于本地 UI 验收",
		}},
		{Resource: "real_warehouses", Values: map[string]any{
			"id": "910000000000000009", "tenant_id": tenantID, "created_at": createdAt,
			"updated_at": createdAt, "name": "本地演示实体仓", "region": "LOCAL",
			"address": "本地测试地址", "status": 1,
		}},
		{Resource: "inventories", Values: map[string]any{
			"tenant_id": tenantID, "goods_id": "910000000000000005",
			"real_warehouse_id": "910000000000000009", "alias": "本地演示商品",
			"bar_code": "LOCAL-DEMO-001", "quantity": 20, "created_at": createdAt, "updated_at": createdAt,
		}},
	}
}

func insertStatement(definition legacydb.Definition, values map[string]any) (string, []any, error) {
	available := make(map[string]struct{}, len(definition.Resource.Columns))
	for _, column := range definition.Resource.Columns {
		available[column.Name] = struct{}{}
	}
	for name := range values {
		if _, ok := available[name]; !ok {
			return "", nil, fmt.Errorf("local legacy fixture: %s demo row has unknown column %q", definition.Resource.Name, name)
		}
	}
	for _, name := range definition.Resource.PrimaryKey {
		if _, ok := values[name]; !ok {
			return "", nil, fmt.Errorf("local legacy fixture: %s demo row is missing primary key %q", definition.Resource.Name, name)
		}
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	columns := make([]string, 0, len(names))
	placeholders := make([]string, 0, len(names))
	arguments := make([]any, 0, len(names))
	for _, name := range names {
		columns = append(columns, quoteIdentifier(name))
		placeholders = append(placeholders, "?")
		arguments = append(arguments, values[name])
	}
	statement := "INSERT INTO \"main\"." + quoteIdentifier(definition.Resource.Name) +
		" (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") +
		") ON CONFLICT DO NOTHING"
	return statement, arguments, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
