package mallsettings

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
)

var requiredSystemConfigColumns = []string{
	"id", "created_at", "updated_at", "deleted_at", "tenant_id", "name", "metadata",
}

func verifyRuntimeReadiness(ctx context.Context, database *gorm.DB, binding fixedbinding.Binding) error {
	if ctx == nil || database == nil || database.Dialector == nil {
		return ErrSchemaNotReady
	}
	if err := binding.Validate(); err != nil {
		return ErrSchemaNotReady
	}
	if err := business.RequireAppliedMigrations(ctx, database, AuthorizationMigrationID); err != nil {
		return fmt.Errorf("%w: authorization migration is unavailable", ErrSchemaNotReady)
	}
	if !database.WithContext(ctx).Migrator().HasTable(new(models.CasbinRule)) {
		return fmt.Errorf("%w: MSS policy table is unavailable", ErrSchemaNotReady)
	}
	if err := verifyAuthorizationReadiness(ctx, database); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaNotReady, err)
	}
	if err := verifySystemConfigReadiness(ctx, database, binding); err != nil {
		return err
	}
	return nil
}

func verifyAuthorizationReadiness(ctx context.Context, database *gorm.DB) error {
	var roles []models.Role
	if err := database.WithContext(ctx).Unscoped().Where("name = ?", "admin").Order("id").Limit(2).Find(&roles).Error; err != nil {
		return errors.New("resolve admin role")
	}
	if len(roles) != 1 || roles[0].DeletedAt.Valid || roles[0].Status != enum.Enabled {
		return errors.New("active admin role is unavailable")
	}
	menus := make(map[string]models.Menu, len(authorizationMenuSeeds))
	for _, seed := range authorizationMenuSeeds {
		query := database.WithContext(ctx).Unscoped().Where("type = ? AND path = ?", seed.accessType, seed.path)
		if seed.accessType == adminpkg.APIAccessType {
			query = query.Where("method = ?", seed.method)
		}
		var matches []models.Menu
		if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
			return fmt.Errorf("resolve %s %q", seed.accessType, seed.path)
		}
		if len(matches) != 1 {
			return fmt.Errorf("%s %q count is %d", seed.accessType, seed.path, len(matches))
		}
		menu := matches[0]
		parentID := ""
		if seed.parentPath != "" {
			parent, exists := menus[seed.parentPath]
			if !exists {
				return fmt.Errorf("parent %q is unavailable", seed.parentPath)
			}
			parentID = parent.ID
		}
		if menu.DeletedAt.Valid || menu.Status != enum.Enabled ||
			menu.Name != seed.name || menu.Method != seed.method || menu.ParentID != parentID ||
			menu.Permission != seed.permission || menu.HideInMenu != seed.hidden {
			return fmt.Errorf("%s %q projection is stale", seed.accessType, seed.path)
		}
		menus[authorizationMenuKey(seed)] = menu

		var policyCount int64
		if err := database.WithContext(ctx).Model(new(models.CasbinRule)).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", roles[0].ID, seed.accessType.String(), seed.path, seed.method,
		).Count(&policyCount).Error; err != nil {
			return fmt.Errorf("read %s %q policy", seed.accessType, seed.path)
		}
		if policyCount != 1 {
			return fmt.Errorf("%s %q policy count is %d", seed.accessType, seed.path, policyCount)
		}
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
	query := `SELECT attribute.attname AS column_name FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace JOIN pg_attribute AS attribute ON attribute.attrelid = relation.oid WHERE namespace.nspname = ? AND relation.relname = 'system_configs' AND relation.relkind IN ('r','p') AND attribute.attnum > 0 AND NOT attribute.attisdropped ORDER BY attribute.attnum`
	var rows []struct {
		ColumnName string `gorm:"column:column_name"`
	}
	if err := database.WithContext(ctx).Raw(query, binding.BusinessSchema).Scan(&rows).Error; err != nil {
		return fmt.Errorf("%w: inspect system_configs", ErrSchemaNotReady)
	}
	columns := make([]string, 0, len(rows))
	for _, row := range rows {
		columns = append(columns, row.ColumnName)
	}
	return verifySystemConfigColumns(columns)
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
	return verifySystemConfigColumns(columns)
}

func verifySystemConfigColumns(columns []string) error {
	available := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		available[column] = struct{}{}
	}
	missing := make([]string, 0)
	for _, column := range requiredSystemConfigColumns {
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
	return nil
}
