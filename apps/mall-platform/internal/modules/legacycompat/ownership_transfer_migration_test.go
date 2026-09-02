package legacycompat

import (
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
)

func TestOwnershipTransferMigrationAddsOnlyReadAccessForSevenMallSnapshots(t *testing.T) {
	db := openMigrationTestDatabase(t)
	for _, migration := range []struct {
		id  string
		run func(*gorm.DB, string) error
	}{
		{id: AuthorizationMigrationID.String(), run: migrateAuthorization},
		{id: MenuLocalizationMigrationID.String(), run: migrateMenuLocalization},
		{id: CapabilityLockdownMigrationID.String(), run: migrateCapabilityLockdown},
		{id: OwnershipTransferMigrationID.String(), run: migrateOwnershipTransfer},
	} {
		for attempt := 1; attempt <= 2; attempt++ {
			if err := migration.run(db, migration.id); err != nil {
				t.Fatalf("migration %s attempt %d: %v", migration.id, attempt, err)
			}
		}
	}

	definitions, err := transferredDefinitions(legacydb.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		var menu models.Menu
		if err := db.Unscoped().Where(
			"type = ? AND path = ?", adminpkg.MenuAccessType, menuPath(definition),
		).Take(&menu).Error; err != nil {
			t.Fatalf("resource menu %s: %v", definition.Resource.Name, err)
		}
		if menu.DeletedAt.Valid || menu.Status != enum.Enabled || menu.Permission != Permission(definition.Resource.Name, OperationList) {
			t.Errorf("resource menu %s = %#v", definition.Resource.Name, menu)
		}
		for _, operation := range operationsFor(definition) {
			var policies int64
			if err := db.Model(new(models.CasbinRule)).Where(
				"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
				"p", adminpkg.ComponentAccessType.String(), componentPath(definition, operation), "GET",
			).Count(&policies).Error; err != nil || policies != 1 {
				t.Errorf("%s %s policies=%d error=%v", definition.Resource.Name, operation, policies, err)
			}
		}
		for _, operation := range []Operation{OperationCreate, OperationUpdate, OperationDelete} {
			var active int64
			if err := db.Unscoped().Model(new(models.Menu)).Where(
				"type = ? AND path = ? AND deleted_at IS NULL AND status = ?",
				adminpkg.ComponentAccessType, componentPath(definition, operation), enum.Enabled,
			).Count(&active).Error; err != nil || active != 0 {
				t.Errorf("%s unexpectedly exposes %s", definition.Resource.Name, operation)
			}
		}
		if db.Migrator().HasTable(definition.Resource.Name) {
			t.Errorf("authorization migration created business table %s", definition.Resource.Name)
		}
	}
	var versions int64
	if err := db.Model(new(migrationmodels.Migration)).Where("version IN ?", []string{
		AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(), CapabilityLockdownMigrationID.String(), OwnershipTransferMigrationID.String(),
	}).Count(&versions).Error; err != nil || versions != 4 {
		t.Fatalf("migration versions=%d error=%v", versions, err)
	}
	if err := verifyOwnershipTransferReadiness(t.Context(), db, legacydb.DefaultRegistry()); err != nil {
		t.Fatalf("ownership readiness: %v", err)
	}
	if err := verifyCapabilityLockdownReadiness(t.Context(), db); err != nil {
		t.Fatalf("read-only readiness: %v", err)
	}
}
