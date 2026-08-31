package sharedcatalog

import (
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
)

func TestMenuLocalizationMigrationRequiresAuthorizationProjection(t *testing.T) {
	t.Parallel()

	db, binding := openSharedCatalogTestDatabase(t)
	if err := applyMenuLocalizationMigration(db, binding, MenuLocalizationMigrationID.String()); err == nil {
		t.Fatal("menu localization migration accepted a missing prerequisite")
	}
	assertCoreCount(t, db, binding, (&migrationmodels.Migration{}).TableName(), "1 = 1", nil, 0)
}

func TestMenuLocalizationMigrationDoesNotCreateSharedBusinessTables(t *testing.T) {
	t.Parallel()

	db, binding := openSharedCatalogTestDatabase(t)
	if err := applyAuthorizationMigration(db, binding, legacydb.DefaultRegistry(), AuthorizationMigrationID.String(), nil); err != nil {
		t.Fatal(err)
	}
	if err := applyMenuLocalizationMigration(db, binding, MenuLocalizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}
	var root models.Menu
	if err := db.Table(qualifiedCoreTable(binding, (&models.Menu{}).TableName())).Where(
		"type = ? AND path = ?", adminpkg.DirectoryAccessType, sharedCatalogRootPath,
	).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	if root.Name != "sharedCatalog" {
		t.Fatalf("localized root name = %q, want %q", root.Name, sharedCatalogMenuNameToken)
	}
	for _, definition := range legacydb.DefaultRegistry().All() {
		var rows []struct{ Name string }
		query := `SELECT name FROM "shared".sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.Raw(query, definition.Resource.Name).Scan(&rows).Error; err != nil || len(rows) != 0 {
			t.Fatalf("legacy table %s was migrated: rows=%v err=%v", definition.Resource.Name, rows, err)
		}
	}
}
