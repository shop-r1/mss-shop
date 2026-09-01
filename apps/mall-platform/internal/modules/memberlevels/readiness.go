package memberlevels

import (
	"context"
	"fmt"
	"sort"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/adminprojection"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
)

var requiredLegacyColumns = []struct {
	table   string
	columns []string
}{
	{table: "member_levels", columns: []string{"id", "created_at", "updated_at", "deleted_at", "tenant_id", "name", "has_market", "change_courier", "payment_ids", "ratio", "init", "status"}},
	{table: "members", columns: []string{"id", "deleted_at", "tenant_id", "level_id"}},
	{table: "activities", columns: []string{"id", "deleted_at", "tenant_id", "member_level_ids_data"}},
	{table: "coupon_parents", columns: []string{"id", "deleted_at", "tenant_id", "member_level_ids_data"}},
	{table: "goods", columns: []string{"id", "deleted_at", "tenant_id"}},
	{table: "goods_shipping_warehouses", columns: []string{"id", "goods_id", "member_level_price_data"}},
}

func verifyRuntimeReadiness(ctx context.Context, database *gorm.DB, binding fixedbinding.Binding) error {
	if ctx == nil || database == nil || database.Dialector == nil {
		return ErrSchemaNotReady
	}
	if err := binding.Validate(); err != nil {
		return ErrSchemaNotReady
	}
	if err := adminprojection.VerifyReadiness(ctx, database, authorizationProjection); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaNotReady, err)
	}
	if err := verifyLegacySchema(ctx, database, binding); err != nil {
		return err
	}
	return nil
}

func verifyLegacySchema(ctx context.Context, database *gorm.DB, binding fixedbinding.Binding) error {
	switch database.Dialector.Name() {
	case "postgres":
		var schemaCount int64
		if err := database.WithContext(ctx).Raw(
			"SELECT COUNT(*) FROM pg_namespace WHERE nspname = ?", binding.BusinessSchema,
		).Scan(&schemaCount).Error; err != nil || schemaCount != 1 {
			return fmt.Errorf("%w: fixed business schema is unavailable", ErrSchemaNotReady)
		}
		var currentSchema string
		if err := database.WithContext(ctx).Raw("SELECT current_schema()").Scan(&currentSchema).Error; err != nil {
			return fmt.Errorf("%w: inspect current schema", ErrSchemaNotReady)
		}
		if currentSchema == binding.BusinessSchema {
			return fmt.Errorf("%w: MSS core schema must be separate from the business schema", ErrSchemaNotReady)
		}
	case "sqlite":
		var schemas []struct {
			Name string `gorm:"column:name"`
		}
		if err := database.WithContext(ctx).Raw("PRAGMA database_list").Scan(&schemas).Error; err != nil {
			return fmt.Errorf("%w: inspect SQLite schemas", ErrSchemaNotReady)
		}
		found := false
		for _, schema := range schemas {
			if schema.Name == binding.BusinessSchema {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: fixed SQLite business schema is unavailable", ErrSchemaNotReady)
		}
	default:
		return fmt.Errorf("%w: unsupported dialect", ErrSchemaNotReady)
	}

	for _, requirement := range requiredLegacyColumns {
		columns, err := inspectColumns(ctx, database, binding.BusinessSchema, requirement.table)
		if err != nil {
			return err
		}
		available := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			available[column] = struct{}{}
		}
		missing := make([]string, 0)
		for _, column := range requirement.columns {
			if _, exists := available[column]; !exists {
				missing = append(missing, column)
			}
		}
		if len(missing) != 0 {
			sort.Strings(missing)
			return fmt.Errorf("%w: %s missing columns %v", ErrSchemaNotReady, requirement.table, missing)
		}
	}
	return nil
}

func inspectColumns(ctx context.Context, database *gorm.DB, schema, table string) ([]string, error) {
	switch database.Dialector.Name() {
	case "postgres":
		query := `SELECT relation.relkind::text AS relation_kind, attribute.attname AS column_name FROM pg_catalog.pg_class AS relation JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace JOIN pg_catalog.pg_attribute AS attribute ON attribute.attrelid = relation.oid WHERE namespace.nspname = ? AND relation.relname = ? AND attribute.attnum > 0 AND NOT attribute.attisdropped ORDER BY attribute.attnum`
		var rows []struct {
			RelationKind string `gorm:"column:relation_kind"`
			ColumnName   string `gorm:"column:column_name"`
		}
		if err := database.WithContext(ctx).Raw(query, schema, table).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("%w: inspect %s", ErrSchemaNotReady, table)
		}
		if len(rows) == 0 || !supportedPostgresRelationKind(rows[0].RelationKind) {
			return nil, fmt.Errorf("%w: %s has an unsupported or missing PostgreSQL relation kind", ErrSchemaNotReady, table)
		}
		columns := make([]string, 0, len(rows))
		for _, row := range rows {
			if row.RelationKind != rows[0].RelationKind {
				return nil, fmt.Errorf("%w: %s relation kind changed while inspected", ErrSchemaNotReady, table)
			}
			columns = append(columns, row.ColumnName)
		}
		return columns, nil
	case "sqlite":
		var rows []struct {
			ColumnName string `gorm:"column:name"`
		}
		query := `PRAGMA "` + schema + `".table_info("` + table + `")`
		if err := database.WithContext(ctx).Raw(query).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("%w: inspect %s", ErrSchemaNotReady, table)
		}
		columns := make([]string, 0, len(rows))
		for _, row := range rows {
			columns = append(columns, row.ColumnName)
		}
		return columns, nil
	default:
		return nil, fmt.Errorf("%w: unsupported dialect", ErrSchemaNotReady)
	}
}

func supportedPostgresRelationKind(kind string) bool {
	switch kind {
	case "r", "p", "v":
		// The isolated reconciler projects all six current member-level
		// dependencies as fixed-tenant security-barrier views. Ordinary and
		// partitioned tables remain valid for a later physical cutover.
		return true
	default:
		return false
	}
}
