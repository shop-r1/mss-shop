package custom

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	tenantBindingConstraintsMigrationID migration.MigrationID = "66966149766768"
	wechatAppIDUniqueIndex                                    = "ux_biz_tenants_wechat_app_id_nonempty"
	wechatAppIDNormalizedColumn                               = "wechat_app_id_nonempty"
)

// tenantBindingConstraintsModule owns the conditional uniqueness that cannot
// be represented by the AdminModule generator: an empty optional AppID may be
// repeated, while every configured AppID remains globally unique.
type tenantBindingConstraintsModule struct{}

type tenantBindingConstraintsModel struct{}

func (tenantBindingConstraintsModel) TableName() string { return "biz_tenants" }

func (tenantBindingConstraintsModule) Name() string { return "tenant-binding-constraints" }

func (tenantBindingConstraintsModule) Register(registry *business.Registry) error {
	if registry == nil {
		return errors.New("tenant binding constraints registry is required")
	}
	return registry.Register(business.Registration{
		Descriptor: business.Descriptor{
			Name:        "tenant-binding-constraints",
			DisplayName: "Tenant binding constraints",
			Model:       new(tenantBindingConstraintsModel),
		},
		Migrations: registerTenantBindingConstraintsMigration,
		Readiness:  verifyTenantBindingConstraintsReadiness,
		Routes:     registerNoTenantBindingConstraintRoutes,
	})
}

func registerTenantBindingConstraintsMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("tenant binding constraints migration runner is required")
	}
	return runner.Register(tenantBindingConstraintsMigrationID, migrateTenantBindingConstraints)
}

func migrateTenantBindingConstraints(db *gorm.DB, version string) error {
	if db == nil {
		return errors.New("tenant binding constraints database is required")
	}
	if version != tenantBindingConstraintsMigrationID.String() {
		return errors.New("tenant binding constraints migration version mismatch")
	}

	var migrationErr error
	switch dialect := db.Dialector.Name(); dialect {
	case "sqlite":
		migrationErr = executeTenantBindingConstraintDDL(db,
			`CREATE UNIQUE INDEX IF NOT EXISTS "ux_biz_tenants_wechat_app_id_nonempty" ON "biz_tenants" ("wechat_app_id") WHERE "wechat_app_id" IS NOT NULL AND "wechat_app_id" <> ''`,
		)
	case "postgres":
		migrationErr = executeTenantBindingConstraintDDL(db,
			`CREATE UNIQUE INDEX IF NOT EXISTS "ux_biz_tenants_wechat_app_id_nonempty" ON "biz_tenants" ("wechat_app_id") WHERE "wechat_app_id" IS NOT NULL AND "wechat_app_id" <> ''`,
		)
	case "mysql":
		if !db.Migrator().HasColumn("biz_tenants", wechatAppIDNormalizedColumn) {
			if err := executeTenantBindingConstraintDDL(db,
				"ALTER TABLE `biz_tenants` ADD COLUMN `wechat_app_id_nonempty` VARCHAR(64) GENERATED ALWAYS AS (NULLIF(`wechat_app_id`, '')) STORED",
			); err != nil {
				return err
			}
		}
		if !db.Migrator().HasIndex("biz_tenants", wechatAppIDUniqueIndex) {
			migrationErr = executeTenantBindingConstraintDDL(db,
				"CREATE UNIQUE INDEX `ux_biz_tenants_wechat_app_id_nonempty` ON `biz_tenants` (`wechat_app_id_nonempty`)",
			)
		}
	default:
		return fmt.Errorf("tenant binding constraints: unsupported database dialect %q", dialect)
	}
	if migrationErr != nil {
		return migrationErr
	}
	if err := verifyTenantBindingConstraintShape(db); err != nil {
		return err
	}

	versionRow := &migrationmodels.Migration{}
	versionRow.SetVersion(version)
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "version"}},
		DoNothing: true,
	}).Create(versionRow).Error; err != nil {
		return errors.New("tenant binding constraints migration failed to record version")
	}
	return nil
}

func executeTenantBindingConstraintDDL(db *gorm.DB, statement string) error {
	if err := db.Exec(statement).Error; err != nil {
		return errors.New("tenant binding constraints migration failed")
	}
	return nil
}

func verifyTenantBindingConstraintsReadiness(ctx context.Context, db *gorm.DB) error {
	if err := business.RequireAppliedMigrations(ctx, db, tenantBindingConstraintsMigrationID); err != nil {
		return fmt.Errorf("tenant binding constraints readiness failed: %w", err)
	}
	if err := verifyTenantBindingConstraintShape(db); err != nil {
		return fmt.Errorf("tenant binding constraints readiness failed: %w", err)
	}
	return nil
}

func verifyTenantBindingConstraintShape(db *gorm.DB) error {
	if db == nil {
		return errors.New("tenant binding constraints shape database is required")
	}
	switch dialect := db.Dialector.Name(); dialect {
	case "sqlite":
		return verifySQLiteTenantBindingConstraint(db)
	case "postgres":
		return verifyPostgresTenantBindingConstraint(db)
	case "mysql":
		return verifyMySQLTenantBindingConstraint(db)
	default:
		return fmt.Errorf("tenant binding constraints: unsupported database dialect %q", dialect)
	}
}

func verifySQLiteTenantBindingConstraint(db *gorm.DB) error {
	var indexes []struct {
		IsUnique  bool `gorm:"column:is_unique"`
		IsPartial bool `gorm:"column:is_partial"`
	}
	if err := db.Raw(
		`SELECT list."unique" <> 0 AS is_unique, list.partial <> 0 AS is_partial FROM pragma_index_list(?) AS list WHERE list.name = ?`,
		"biz_tenants",
		wechatAppIDUniqueIndex,
	).Scan(&indexes).Error; err != nil || len(indexes) != 1 || !indexes[0].IsUnique || !indexes[0].IsPartial {
		return errors.New("tenant binding constraints index shape is invalid")
	}
	columns, err := loadTenantBindingConstraintIndexColumns(
		db,
		`SELECT name AS column_name FROM pragma_index_info(?) ORDER BY seqno`,
		wechatAppIDUniqueIndex,
	)
	if err != nil || !singleTenantBindingColumn(columns, "wechat_app_id") {
		return errors.New("tenant binding constraints index columns are invalid")
	}
	var definition string
	if err := db.Raw(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`,
		wechatAppIDUniqueIndex,
	).Scan(&definition).Error; err != nil || !hasNonemptyAppIDPredicate(definition) {
		return errors.New("tenant binding constraints index predicate is invalid")
	}
	return nil
}

func verifyPostgresTenantBindingConstraint(db *gorm.DB) error {
	var rows []struct {
		ColumnName string `gorm:"column:column_name"`
		IsUnique   bool   `gorm:"column:is_unique"`
		Predicate  string `gorm:"column:predicate"`
	}
	query := `SELECT attribute.attname AS column_name, catalog.indisunique AS is_unique, pg_get_expr(catalog.indpred, catalog.indrelid) AS predicate FROM pg_index AS catalog JOIN pg_class AS table_class ON table_class.oid = catalog.indrelid JOIN pg_namespace AS namespace ON namespace.oid = table_class.relnamespace JOIN pg_class AS index_class ON index_class.oid = catalog.indexrelid JOIN LATERAL unnest(catalog.indkey) WITH ORDINALITY AS key(attnum, position) ON true JOIN pg_attribute AS attribute ON attribute.attrelid = table_class.oid AND attribute.attnum = key.attnum WHERE namespace.nspname = current_schema() AND table_class.relname = ? AND index_class.relname = ? ORDER BY key.position`
	if err := db.Raw(query, "biz_tenants", wechatAppIDUniqueIndex).Scan(&rows).Error; err != nil || len(rows) != 1 || !rows[0].IsUnique || rows[0].ColumnName != "wechat_app_id" || !hasNonemptyAppIDPredicate(rows[0].Predicate) {
		return errors.New("tenant binding constraints index shape is invalid")
	}
	return nil
}

func verifyMySQLTenantBindingConstraint(db *gorm.DB) error {
	columns, err := loadTenantBindingConstraintIndexColumns(
		db,
		`SELECT column_name FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ? AND non_unique = 0 ORDER BY seq_in_index`,
		"biz_tenants",
		wechatAppIDUniqueIndex,
	)
	if err != nil || !singleTenantBindingColumn(columns, wechatAppIDNormalizedColumn) {
		return errors.New("tenant binding constraints index shape is invalid")
	}
	var generated []struct {
		DataType             string `gorm:"column:data_type"`
		MaximumLength        int64  `gorm:"column:maximum_length"`
		GenerationExpression string `gorm:"column:generation_expression"`
		Extra                string `gorm:"column:extra"`
	}
	query := `SELECT data_type, character_maximum_length AS maximum_length, generation_expression, extra FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`
	if err := db.Raw(query, "biz_tenants", wechatAppIDNormalizedColumn).Scan(&generated).Error; err != nil || len(generated) != 1 {
		return errors.New("tenant binding constraints generated column is unavailable")
	}
	expression := normalizeTenantBindingSQL(generated[0].GenerationExpression)
	if !strings.EqualFold(generated[0].DataType, "varchar") || generated[0].MaximumLength != 64 ||
		!strings.Contains(strings.ToLower(generated[0].Extra), "stored generated") ||
		!strings.Contains(expression, "nullif") ||
		!strings.Contains(expression, "wechat_app_id") ||
		!strings.Contains(expression, "''") {
		return errors.New("tenant binding constraints generated column shape is invalid")
	}
	return nil
}

func loadTenantBindingConstraintIndexColumns(db *gorm.DB, query string, args ...any) ([]string, error) {
	var rows []struct {
		ColumnName string `gorm:"column:column_name"`
	}
	if err := db.Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	columns := make([]string, 0, len(rows))
	for _, row := range rows {
		columns = append(columns, row.ColumnName)
	}
	return columns, nil
}

func singleTenantBindingColumn(columns []string, expected string) bool {
	return len(columns) == 1 && columns[0] == expected
}

func hasNonemptyAppIDPredicate(value string) bool {
	normalized := normalizeTenantBindingSQL(value)
	return strings.Contains(normalized, "wechat_app_id") &&
		strings.Contains(normalized, "is not null") &&
		strings.Contains(normalized, "<> ''")
}

func normalizeTenantBindingSQL(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "`", "")
	value = strings.ReplaceAll(value, `"`, "")
	return strings.Join(strings.Fields(value), " ")
}

func registerNoTenantBindingConstraintRoutes(_ *gin.RouterGroup, _ business.Runtime) error {
	return nil
}
