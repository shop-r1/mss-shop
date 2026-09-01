package mallsettings

import (
	"context"
	"fmt"
	"sort"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/adminprojection"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
)

var requiredSystemConfigColumns = []string{
	"id", "created_at", "updated_at", "deleted_at", "tenant_id", "name", "metadata",
}

var genericSystemConfigColumns = []string{
	"id", "created_at", "updated_at", "deleted_at", "tenant_id", "name", "metadata",
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
	if err := verifySystemConfigReadiness(ctx, database, binding); err != nil {
		return err
	}
	return nil
}

func verifySystemConfigReadiness(ctx context.Context, database *gorm.DB, binding fixedbinding.Binding) error {
	switch database.Dialector.Name() {
	case "postgres":
		return verifyPostgresSystemConfigReadiness(ctx, database, binding)
	case "sqlite":
		return verifySQLiteSystemConfigReadiness(ctx, database, binding)
	default:
		return fmt.Errorf("%w: unsupported dialect", ErrSchemaNotReady)
	}
}

func verifyPostgresSystemConfigReadiness(ctx context.Context, database *gorm.DB, binding fixedbinding.Binding) error {
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
	private, err := inspectPostgresSystemConfigRelation(
		ctx, database, binding.BusinessSchema, postgresPrivateRelation,
	)
	if err != nil {
		return err
	}
	if err := verifyPostgresReadOnlyView(private, requiredSystemConfigColumns); err != nil {
		return fmt.Errorf("%w: private mall-settings projection: %v", ErrSchemaNotReady, err)
	}

	// Recheck the generic compatibility surface here because this dedicated
	// workflow is the sole exception allowed to read raw metadata. It must not
	// accidentally widen the generic system_configs view or its runtime ACL.
	generic, err := inspectPostgresSystemConfigRelation(
		ctx, database, binding.BusinessSchema, "system_configs",
	)
	if err != nil {
		return err
	}
	if err := verifyPostgresReadOnlyView(generic, genericSystemConfigColumns); err != nil {
		return fmt.Errorf("%w: generic system_configs projection: %v", ErrSchemaNotReady, err)
	}
	return nil
}

type postgresSystemConfigRelation struct {
	RelationKind    string
	SecurityBarrier bool
	SecurityInvoker bool
	CanSelect       bool
	CanInsert       bool
	CanUpdate       bool
	CanDelete       bool
	CanTruncate     bool
	CanReferences   bool
	CanTrigger      bool
	CanMaintain     bool
	CanColumnInsert bool
	CanColumnUpdate bool
	CanColumnRefs   bool
	Columns         []string
}

func inspectPostgresSystemConfigRelation(
	ctx context.Context,
	database *gorm.DB,
	schema string,
	relationName string,
) (postgresSystemConfigRelation, error) {
	query := `SELECT relation.relkind::text AS relation_kind, COALESCE('security_barrier=true' = ANY(relation.reloptions), FALSE) AS security_barrier, COALESCE('security_invoker=true' = ANY(relation.reloptions), FALSE) AS security_invoker, has_table_privilege(current_user, relation.oid, 'SELECT') AS can_select, has_table_privilege(current_user, relation.oid, 'INSERT') AS can_insert, has_table_privilege(current_user, relation.oid, 'UPDATE') AS can_update, has_table_privilege(current_user, relation.oid, 'DELETE') AS can_delete, has_table_privilege(current_user, relation.oid, 'TRUNCATE') AS can_truncate, has_table_privilege(current_user, relation.oid, 'REFERENCES') AS can_references, has_table_privilege(current_user, relation.oid, 'TRIGGER') AS can_trigger, has_table_privilege(current_user, relation.oid, 'MAINTAIN') AS can_maintain, has_any_column_privilege(current_user, relation.oid, 'INSERT') AS can_column_insert, has_any_column_privilege(current_user, relation.oid, 'UPDATE') AS can_column_update, has_any_column_privilege(current_user, relation.oid, 'REFERENCES') AS can_column_refs FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace WHERE namespace.nspname = ? AND relation.relname = ?`
	var rows []struct {
		RelationKind    string `gorm:"column:relation_kind"`
		SecurityBarrier bool   `gorm:"column:security_barrier"`
		SecurityInvoker bool   `gorm:"column:security_invoker"`
		CanSelect       bool   `gorm:"column:can_select"`
		CanInsert       bool   `gorm:"column:can_insert"`
		CanUpdate       bool   `gorm:"column:can_update"`
		CanDelete       bool   `gorm:"column:can_delete"`
		CanTruncate     bool   `gorm:"column:can_truncate"`
		CanReferences   bool   `gorm:"column:can_references"`
		CanTrigger      bool   `gorm:"column:can_trigger"`
		CanMaintain     bool   `gorm:"column:can_maintain"`
		CanColumnInsert bool   `gorm:"column:can_column_insert"`
		CanColumnUpdate bool   `gorm:"column:can_column_update"`
		CanColumnRefs   bool   `gorm:"column:can_column_refs"`
	}
	if err := database.WithContext(ctx).Raw(query, schema, relationName).Scan(&rows).Error; err != nil {
		return postgresSystemConfigRelation{}, fmt.Errorf("%w: inspect %s", ErrSchemaNotReady, relationName)
	}
	if len(rows) != 1 {
		return postgresSystemConfigRelation{}, fmt.Errorf("%w: %s is unavailable or ambiguous", ErrSchemaNotReady, relationName)
	}
	columnsQuery := `SELECT attribute.attname AS column_name FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace JOIN pg_attribute AS attribute ON attribute.attrelid = relation.oid WHERE namespace.nspname = ? AND relation.relname = ? AND attribute.attnum > 0 AND NOT attribute.attisdropped ORDER BY attribute.attnum`
	var columnRows []struct {
		ColumnName string `gorm:"column:column_name"`
	}
	if err := database.WithContext(ctx).Raw(columnsQuery, schema, relationName).Scan(&columnRows).Error; err != nil {
		return postgresSystemConfigRelation{}, fmt.Errorf("%w: inspect %s columns", ErrSchemaNotReady, relationName)
	}
	columns := make([]string, 0, len(columnRows))
	for _, row := range columnRows {
		columns = append(columns, row.ColumnName)
	}
	row := rows[0]
	return postgresSystemConfigRelation{
		RelationKind: row.RelationKind, SecurityBarrier: row.SecurityBarrier,
		SecurityInvoker: row.SecurityInvoker, CanSelect: row.CanSelect,
		CanInsert: row.CanInsert, CanUpdate: row.CanUpdate, CanDelete: row.CanDelete,
		CanTruncate: row.CanTruncate, CanReferences: row.CanReferences, CanTrigger: row.CanTrigger,
		CanMaintain: row.CanMaintain, CanColumnInsert: row.CanColumnInsert,
		CanColumnUpdate: row.CanColumnUpdate, CanColumnRefs: row.CanColumnRefs,
		Columns: columns,
	}, nil
}

func verifyPostgresReadOnlyView(relation postgresSystemConfigRelation, expectedColumns []string) error {
	if relation.RelationKind != "v" || !relation.SecurityBarrier || relation.SecurityInvoker {
		return fmt.Errorf("%w: relation is not the expected security-barrier invoker-disabled view", ErrSchemaNotReady)
	}
	if !relation.CanSelect {
		return fmt.Errorf("%w: runtime SELECT is unavailable", ErrSchemaNotReady)
	}
	if relation.CanInsert || relation.CanUpdate || relation.CanDelete || relation.CanTruncate ||
		relation.CanReferences || relation.CanTrigger || relation.CanMaintain ||
		relation.CanColumnInsert || relation.CanColumnUpdate || relation.CanColumnRefs {
		return fmt.Errorf("%w: runtime mutation privilege is present", ErrSchemaNotReady)
	}
	return verifySystemConfigColumns(relation.Columns, expectedColumns)
}

func verifySQLiteSystemConfigReadiness(ctx context.Context, database *gorm.DB, binding fixedbinding.Binding) error {
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
	var rows []struct {
		ColumnName string `gorm:"column:name"`
	}
	query := "PRAGMA \"" + binding.BusinessSchema + "\".table_info(\"system_configs\")"
	if err := database.WithContext(ctx).Raw(query).Scan(&rows).Error; err != nil {
		return fmt.Errorf("%w: inspect system_configs", ErrSchemaNotReady)
	}
	columns := make([]string, 0, len(rows))
	for _, row := range rows {
		columns = append(columns, row.ColumnName)
	}
	return verifySystemConfigColumns(columns, requiredSystemConfigColumns)
}

func verifySystemConfigColumns(columns []string, expected []string) error {
	available := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		available[column] = struct{}{}
	}
	missing := make([]string, 0)
	for _, column := range expected {
		if _, exists := available[column]; !exists {
			missing = append(missing, column)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: system_configs missing columns %v", ErrSchemaNotReady, missing)
	}
	if len(available) == 0 {
		return fmt.Errorf("%w: system_configs is unavailable", ErrSchemaNotReady)
	}
	if len(available) != len(expected) {
		return fmt.Errorf("%w: system_configs has unsupported columns", ErrSchemaNotReady)
	}
	return nil
}
