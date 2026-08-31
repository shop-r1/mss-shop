package custom

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTenantBindingConstraintsAllowEmptyAndRejectDuplicateConfiguredAppID(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/bindings.db"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE biz_tenants (id TEXT PRIMARY KEY, wechat_app_id TEXT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(new(migrationmodels.Migration)); err != nil {
		t.Fatal(err)
	}

	registry, err := business.Compose(migration.New(), tenantBindingConstraintsModule{})
	if err != nil {
		t.Fatalf("compose module: %v", err)
	}
	phases, err := registry.MigrationPhaseRunners()
	if err != nil {
		t.Fatalf("migration runners: %v", err)
	}
	phases.Business.SetDb(db)
	phases.Business.SetModel(new(migrationmodels.Migration))
	if err := phases.Business.MigrateContext(context.Background()); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := phases.Business.MigrateContext(context.Background()); err != nil {
		t.Fatalf("second migration was not idempotent: %v", err)
	}
	if err := verifyTenantBindingConstraintsReadiness(context.Background(), db); err != nil {
		t.Fatalf("readiness: %v", err)
	}
	var applied int64
	if err := db.Model(new(migrationmodels.Migration)).
		Where("version = ?", tenantBindingConstraintsMigrationID.String()).
		Count(&applied).Error; err != nil || applied != 1 {
		t.Fatalf("migration ledger count = %d, error = %v", applied, err)
	}

	for _, row := range []struct {
		id    string
		appID any
	}{
		{id: "empty-one", appID: ""},
		{id: "empty-two", appID: ""},
		{id: "null-one", appID: nil},
		{id: "null-two", appID: nil},
		{id: "configured-one", appID: "wx0123456789abcdef"},
	} {
		if err := db.Exec(`INSERT INTO biz_tenants (id, wechat_app_id) VALUES (?, ?)`, row.id, row.appID).Error; err != nil {
			t.Fatalf("insert %#v: %v", row, err)
		}
	}
	if err := db.Exec(`INSERT INTO biz_tenants (id, wechat_app_id) VALUES (?, ?)`, "configured-two", "wx0123456789abcdef").Error; err == nil {
		t.Fatal("duplicate configured AppID unexpectedly succeeded")
	}
}

func TestCustomModulesIncludeTenantBindingConstraints(t *testing.T) {
	t.Parallel()

	modules := Modules()
	if len(modules) != 2 || modules[0].Name() != "tenant-binding-constraints" || modules[1].Name() != "sharedcatalog" {
		t.Fatalf("Modules() = %#v", modules)
	}
}

func TestTenantBindingConstraintsRejectWrongExistingIndexShape(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/wrong-shape.db"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE biz_tenants (id TEXT PRIMARY KEY, wechat_app_id TEXT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(new(migrationmodels.Migration)); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE INDEX ux_biz_tenants_wechat_app_id_nonempty ON biz_tenants (wechat_app_id)`).Error; err != nil {
		t.Fatal(err)
	}

	runner := migration.New()
	if err := registerTenantBindingConstraintsMigration(runner); err != nil {
		t.Fatal(err)
	}
	runner.SetDb(db)
	runner.SetModel(new(migrationmodels.Migration))
	if err := runner.MigrateContext(context.Background()); err == nil {
		t.Fatal("migration accepted a same-name non-unique index")
	}
	var applied int64
	if err := db.Model(new(migrationmodels.Migration)).
		Where("version = ?", tenantBindingConstraintsMigrationID.String()).
		Count(&applied).Error; err != nil || applied != 0 {
		t.Fatalf("invalid migration ledger count = %d, error = %v", applied, err)
	}
}
