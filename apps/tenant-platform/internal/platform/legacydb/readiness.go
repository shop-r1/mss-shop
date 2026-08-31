package legacydb

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
)

// VerifyReadiness performs catalog reads only. It never creates, alters, or
// migrates a legacy relation.
func VerifyReadiness(ctx context.Context, db *gorm.DB, binding fixedbinding.Binding, registry Registry) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrSchemaNotReady)
	}
	if db == nil {
		return fmt.Errorf("%w: database is required", ErrSchemaNotReady)
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaNotReady, err)
	}
	definitions := registry.All()
	if len(definitions) != ExpectedSharedResourceCount {
		return fmt.Errorf("%w: reviewed resource count is %d, want %d", ErrSchemaNotReady, len(definitions), ExpectedSharedResourceCount)
	}
	switch db.Dialector.Name() {
	case "postgres":
		return verifyPostgresReadiness(ctx, db, binding, definitions)
	case "sqlite":
		return verifySQLiteReadiness(ctx, db, binding, definitions)
	default:
		return fmt.Errorf("%w: unsupported dialect %q", ErrSchemaNotReady, db.Dialector.Name())
	}
}

func verifyPostgresReadiness(ctx context.Context, db *gorm.DB, binding fixedbinding.Binding, definitions []Definition) error {
	var schemaCount int64
	if err := db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM pg_namespace WHERE nspname IN ?",
		[]string{binding.CoreSchema, binding.SharedSchema},
	).Scan(&schemaCount).Error; err != nil {
		return fmt.Errorf("%w: inspect fixed schemas", ErrSchemaNotReady)
	}
	if schemaCount != 2 {
		return fmt.Errorf("%w: fixed core/shared schemas are unavailable", ErrSchemaNotReady)
	}
	var currentSchema string
	if err := db.WithContext(ctx).Raw("SELECT current_schema()").Scan(&currentSchema).Error; err != nil {
		return fmt.Errorf("%w: inspect current schema", ErrSchemaNotReady)
	}
	if currentSchema != binding.CoreSchema {
		return fmt.Errorf("%w: current schema is not the fixed MSS core schema", ErrSchemaNotReady)
	}
	for _, definition := range definitions {
		var rows []struct {
			ColumnName string `gorm:"column:column_name"`
		}
		query := `SELECT attribute.attname AS column_name FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace JOIN pg_attribute AS attribute ON attribute.attrelid = relation.oid WHERE namespace.nspname = ? AND relation.relname = ? AND relation.relkind IN ('r','p','v','m','f') AND attribute.attnum > 0 AND NOT attribute.attisdropped ORDER BY attribute.attnum`
		if err := db.WithContext(ctx).Raw(query, binding.SharedSchema, definition.Resource.Name).Scan(&rows).Error; err != nil {
			return fmt.Errorf("%w: inspect %s", ErrSchemaNotReady, definition.Resource.Name)
		}
		available := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			available[row.ColumnName] = struct{}{}
		}
		if err := verifyColumns(definition, available); err != nil {
			return err
		}
	}
	return nil
}

func verifySQLiteReadiness(ctx context.Context, db *gorm.DB, binding fixedbinding.Binding, definitions []Definition) error {
	var databases []struct {
		Name string `gorm:"column:name"`
	}
	if err := db.WithContext(ctx).Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		return fmt.Errorf("%w: inspect SQLite databases", ErrSchemaNotReady)
	}
	availableSchemas := make(map[string]struct{}, len(databases))
	for _, database := range databases {
		availableSchemas[database.Name] = struct{}{}
	}
	for _, schema := range []string{binding.CoreSchema, binding.SharedSchema} {
		if _, ok := availableSchemas[schema]; !ok {
			return fmt.Errorf("%w: SQLite schema %q is unavailable", ErrSchemaNotReady, schema)
		}
	}
	for _, definition := range definitions {
		query := "PRAGMA " + quoteIdentifier(binding.SharedSchema) + ".table_info(" + quoteIdentifier(definition.Resource.Name) + ")"
		var rows []struct {
			ColumnName string `gorm:"column:name"`
		}
		if err := db.WithContext(ctx).Raw(query).Scan(&rows).Error; err != nil {
			return fmt.Errorf("%w: inspect %s", ErrSchemaNotReady, definition.Resource.Name)
		}
		available := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			available[row.ColumnName] = struct{}{}
		}
		if err := verifyColumns(definition, available); err != nil {
			return err
		}
	}
	return nil
}

func verifyColumns(definition Definition, available map[string]struct{}) error {
	missing := make([]string, 0)
	for _, column := range definition.Resource.Columns {
		if _, ok := available[column.Name]; !ok {
			missing = append(missing, column.Name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: %s missing columns %v", ErrSchemaNotReady, definition.Resource.Name, missing)
	}
	if len(available) == 0 {
		return fmt.Errorf("%w: relation %s is unavailable", ErrSchemaNotReady, definition.Resource.Name)
	}
	return nil
}

func IsSchemaNotReady(err error) bool { return errors.Is(err, ErrSchemaNotReady) }
