package legacydb

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
)

// VerifyReadiness performs only catalog reads. It never creates, alters, or
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
	if len(definitions) != ExpectedMallResourceCount {
		return fmt.Errorf("%w: reviewed resource count is %d, want %d", ErrSchemaNotReady, len(definitions), ExpectedMallResourceCount)
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
	if binding.BusinessSchema == binding.SharedSchema {
		return fmt.Errorf("%w: business and shared schemas must be distinct", ErrSchemaNotReady)
	}
	var schemaCount int64
	if err := db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM pg_namespace WHERE nspname IN ?",
		[]string{binding.BusinessSchema, binding.SharedSchema},
	).Scan(&schemaCount).Error; err != nil {
		return fmt.Errorf("%w: inspect schemas", ErrSchemaNotReady)
	}
	if schemaCount != 2 {
		return fmt.Errorf("%w: fixed business/shared schemas are unavailable", ErrSchemaNotReady)
	}
	var currentSchema string
	if err := db.WithContext(ctx).Raw("SELECT current_schema()").Scan(&currentSchema).Error; err != nil {
		return fmt.Errorf("%w: inspect current schema", ErrSchemaNotReady)
	}
	if currentSchema == binding.BusinessSchema || currentSchema == binding.SharedSchema {
		return fmt.Errorf("%w: MSS core schema must be separate from legacy schemas", ErrSchemaNotReady)
	}
	for _, definition := range definitions {
		var rows []struct {
			ColumnName string `gorm:"column:column_name"`
		}
		query := `SELECT attribute.attname AS column_name FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace JOIN pg_attribute AS attribute ON attribute.attrelid = relation.oid WHERE namespace.nspname = ? AND relation.relname = ? AND relation.relkind IN ('r','p','v','m','f') AND attribute.attnum > 0 AND NOT attribute.attisdropped ORDER BY attribute.attnum`
		if err := db.WithContext(ctx).Raw(query, binding.BusinessSchema, definition.Resource.Name).Scan(&rows).Error; err != nil {
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
	// The mall compatibility repository reads only the business schema. The
	// distinct shared schema remains mandatory in the immutable binding, but a
	// SQLite attachment would be connection-local and therefore cannot model
	// deployment isolation. PostgreSQL readiness above still proves that core,
	// business and shared schemas are distinct and available.
	if _, ok := availableSchemas[binding.BusinessSchema]; !ok {
		return fmt.Errorf("%w: SQLite business schema %q is unavailable", ErrSchemaNotReady, binding.BusinessSchema)
	}
	for _, definition := range definitions {
		query := "PRAGMA " + quoteIdentifier(binding.BusinessSchema) + ".table_info(" + quoteIdentifier(definition.Resource.Name) + ")"
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
	if definition.Inherited != nil {
		// The parent relation is itself in the reviewed registry; its own pass
		// validates tenant and soft-delete columns.
		if definition.Inherited.ParentTable == "" {
			return fmt.Errorf("%w: %s inherited scope is incomplete", ErrSchemaNotReady, definition.Resource.Name)
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

func IsSchemaNotReady(err error) bool {
	return errors.Is(err, ErrSchemaNotReady)
}
