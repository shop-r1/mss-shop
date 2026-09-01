package sharedcatalog

import (
	"errors"
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
)

func TestOwnershipTransferMigrationRemovesSevenTenantMenusAndPolicies(t *testing.T) {
	db, binding := openSharedCatalogTestDatabase(t)
	published := legacydb.PublishedRegistry()
	current := legacydb.DefaultRegistry()
	applyCapabilityLockdownPrerequisites(t, db, binding, published)
	if err := applyCapabilityLockdownMigration(db, binding, published, CapabilityLockdownMigrationID.String(), nil); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := applyOwnershipTransferMigration(db, binding, published, current, OwnershipTransferMigrationID.String(), nil); err != nil {
			t.Fatalf("ownership transfer attempt %d: %v", attempt, err)
		}
	}
	if err := verifyOwnershipTransferReadiness(t.Context(), db, binding, published, current); err != nil {
		t.Fatal(err)
	}
	if err := verifyAuthorizationReadiness(t.Context(), db, binding, current); err != nil {
		t.Fatal(err)
	}
	assertCoreCount(t, db, binding, (&models.Menu{}).TableName(),
		"type = ? AND path = ? AND deleted_at IS NULL", []any{adminpkg.MenuAccessType, menuPath("payments")}, 1)
	assertCoreCount(t, db, binding, (&models.Menu{}).TableName(),
		"type = ? AND path = ? AND deleted_at IS NULL", []any{adminpkg.MenuAccessType, menuPath("brands")}, 0)
	assertCoreCount(t, db, binding, (&migrationmodels.Migration{}).TableName(),
		"version = ?", []any{OwnershipTransferMigrationID.String()}, 1)
}

func TestOwnershipTransferMigrationRollsBackAllAuthorizationChanges(t *testing.T) {
	db, binding := openSharedCatalogTestDatabase(t)
	published := legacydb.PublishedRegistry()
	current := legacydb.DefaultRegistry()
	applyCapabilityLockdownPrerequisites(t, db, binding, published)
	if err := applyCapabilityLockdownMigration(db, binding, published, CapabilityLockdownMigrationID.String(), nil); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected")
	if err := applyOwnershipTransferMigration(
		db, binding, published, current, OwnershipTransferMigrationID.String(), func() error { return injected },
	); !errors.Is(err, injected) {
		t.Fatalf("ownership transfer error = %v", err)
	}
	assertCoreCount(t, db, binding, (&migrationmodels.Migration{}).TableName(),
		"version = ?", []any{OwnershipTransferMigrationID.String()}, 0)
	assertCoreCount(t, db, binding, (&models.Menu{}).TableName(),
		"type = ? AND path = ? AND deleted_at IS NULL", []any{adminpkg.MenuAccessType, menuPath("brands")}, 1)
	assertCoreCount(t, db, binding, (&models.Menu{}).TableName(),
		"type = ? AND path = ? AND permission = ?", []any{
			adminpkg.DirectoryAccessType, sharedCatalogRootPath, PermissionCode("brands", "read"),
		}, 1)
}
