package sharedcatalog

import (
	"context"
	"errors"
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
	"gorm.io/gorm"
)

func TestCapabilityLockdownMigrationRequiresBothPublishedPrerequisites(t *testing.T) {
	t.Parallel()

	db, binding := openSharedCatalogTestDatabase(t)
	registry := legacydb.DefaultRegistry()
	if err := applyCapabilityLockdownMigration(db, binding, registry, CapabilityLockdownMigrationID.String(), nil); err == nil {
		t.Fatal("capability lockdown accepted missing prerequisites")
	}
	if err := applyAuthorizationMigration(db, binding, registry, AuthorizationMigrationID.String(), nil); err != nil {
		t.Fatal(err)
	}
	if err := applyCapabilityLockdownMigration(db, binding, registry, CapabilityLockdownMigrationID.String(), nil); err == nil {
		t.Fatal("capability lockdown accepted a missing localization prerequisite")
	}
	assertCoreCount(t, db, binding, (&migrationmodels.Migration{}).TableName(), "version = ?", []any{CapabilityLockdownMigrationID.String()}, 0)
}

func TestCapabilityLockdownMigrationIsIdempotentAndNeverTouchesSharedTables(t *testing.T) {
	t.Parallel()

	db, binding := openSharedCatalogTestDatabase(t)
	registry := legacydb.DefaultRegistry()
	applyCapabilityLockdownPrerequisites(t, db, binding, registry)

	component, err := upsertAuthorizationMenu(db, binding, authorizationMenuSeed{
		name:       "legacy.permissions.create",
		path:       componentPath("categories", "create"),
		method:     httpGet,
		accessType: adminpkg.ComponentAccessType,
		permission: PermissionCode("categories", string(actionCreate)),
		hidden:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedAuthorizationRule(db, binding, "rogue-role", adminpkg.ComponentAccessType, component.Path, httpGet); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := applyCapabilityLockdownMigration(db, binding, registry, CapabilityLockdownMigrationID.String(), nil); err != nil {
			t.Fatalf("capability lockdown attempt %d: %v", attempt, err)
		}
	}
	assertCoreCount(t, db, binding, (&migrationmodels.Migration{}).TableName(), "version = ?", []any{CapabilityLockdownMigrationID.String()}, 1)
	assertCoreCount(t, db, binding, (&models.Menu{}).TableName(), "type = ? AND path = ? AND deleted_at IS NULL", []any{
		adminpkg.ComponentAccessType, componentPath("categories", "create"),
	}, 0)
	assertCoreCount(t, db, binding, (&models.CasbinRule{}).TableName(), "ptype = ? AND v0 = ? AND v1 = ? AND v2 = ?", []any{
		"p", "rogue-role", adminpkg.ComponentAccessType.String(), componentPath("categories", "create"),
	}, 0)
	if err := verifyAuthorizationReadiness(context.Background(), db, binding, registry); err != nil {
		t.Fatalf("authorization readiness: %v", err)
	}
	if err := seedAuthorizationRule(db, binding, "rogue-role", adminpkg.APIAccessType, collectionRoute, "POST"); err != nil {
		t.Fatal(err)
	}
	if err := verifyAuthorizationReadiness(context.Background(), db, binding, registry); err == nil {
		t.Fatal("authorization readiness accepted a restored write policy")
	}

	for _, definition := range registry.All() {
		var rows []struct{ Name string }
		query := `SELECT name FROM "shared".sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.Raw(query, definition.Resource.Name).Scan(&rows).Error; err != nil || len(rows) != 0 {
			t.Fatalf("legacy table %s was migrated: rows=%v err=%v", definition.Resource.Name, rows, err)
		}
	}
}

func TestCapabilityLockdownMigrationRollsBackCleanup(t *testing.T) {
	t.Parallel()

	db, binding := openSharedCatalogTestDatabase(t)
	registry := legacydb.DefaultRegistry()
	applyCapabilityLockdownPrerequisites(t, db, binding, registry)
	injected := errors.New("injected")
	if err := applyCapabilityLockdownMigration(
		db, binding, registry, CapabilityLockdownMigrationID.String(), func() error { return injected },
	); !errors.Is(err, injected) {
		t.Fatalf("capability lockdown error = %v", err)
	}
	assertCoreCount(t, db, binding, (&migrationmodels.Migration{}).TableName(), "version = ?", []any{CapabilityLockdownMigrationID.String()}, 0)
	assertCoreCount(t, db, binding, (&models.Menu{}).TableName(), "deleted_at IS NULL", nil, 42)
	assertCoreCount(t, db, binding, (&models.CasbinRule{}).TableName(), "ptype = ?", []any{"p"}, 42)
}

func applyCapabilityLockdownPrerequisites(
	t *testing.T,
	db *gorm.DB,
	binding fixedbinding.Binding,
	registry legacydb.Registry,
) {
	t.Helper()
	if err := applyAuthorizationMigration(db, binding, registry, AuthorizationMigrationID.String(), nil); err != nil {
		t.Fatal(err)
	}
	if err := applyMenuLocalizationMigration(db, binding, MenuLocalizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}
}
