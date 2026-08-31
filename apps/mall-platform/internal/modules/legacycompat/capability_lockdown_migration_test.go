package legacycompat

import (
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
)

func TestCapabilityLockdownIsForwardOnlyIdempotentAndReadOnly(t *testing.T) {
	db := openMigrationTestDatabase(t)
	if err := migrateAuthorization(db, AuthorizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}
	if err := migrateMenuLocalization(db, MenuLocalizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}

	showCategories, _ := legacydb.DefaultRegistry().Lookup("show_categories")
	if showCategories.Resource.Capabilities.Create || operationAllowed(showCategories, OperationCreate) {
		t.Fatalf("runtime registry is not read-only: %#v", showCategories.Resource.Capabilities)
	}
	var historicalComponent models.Menu
	if err := db.Where(
		"type = ? AND path = ?", adminpkg.ComponentAccessType, componentPath(showCategories, OperationCreate),
	).Take(&historicalComponent).Error; err != nil {
		t.Fatalf("published write component is not reproducible: %v", err)
	}
	if historicalComponent.Status != enum.Enabled {
		t.Fatalf("published write component status = %q", historicalComponent.Status)
	}
	orders, _ := legacydb.DefaultRegistry().Lookup("orders")
	rogueComponent, err := upsertMenu(db, menuSeed{
		name:       Permission("orders", OperationCreate),
		path:       componentPath(orders, OperationCreate),
		method:     "GET",
		accessType: adminpkg.ComponentAccessType,
		permission: Permission("orders", OperationCreate),
		hidden:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedPolicy(db, "rogue-role", adminpkg.ComponentAccessType, rogueComponent.Path, "GET"); err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := migrateCapabilityLockdown(db, CapabilityLockdownMigrationID.String()); err != nil {
			t.Fatalf("capability lockdown attempt %d: %v", attempt, err)
		}
	}

	for _, target := range capabilityLockdownTargets(legacydb.DefaultRegistry()) {
		query := db.Unscoped().Where("type = ? AND path = ?", target.accessType, target.path)
		if target.accessType == adminpkg.APIAccessType {
			query = query.Where("method = ?", target.method)
		}
		var menus []models.Menu
		if err := query.Find(&menus).Error; err != nil {
			t.Fatal(err)
		}
		if len(menus) > 1 || (len(menus) == 1 && menus[0].Status != enum.Disabled) {
			t.Errorf("locked target %s %s %s menus = %#v", target.accessType, target.method, target.path, menus)
		}
		var policies int64
		if err := db.Model(new(models.CasbinRule)).Where(
			"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", target.accessType.String(), target.path, target.method,
		).Count(&policies).Error; err != nil || policies != 0 {
			t.Errorf("locked target %s %s %s policies=%d error=%v", target.accessType, target.method, target.path, policies, err)
		}
	}

	var readPolicies int64
	if err := db.Model(new(models.CasbinRule)).Where(
		"v1 = ? AND v2 = ? AND v3 = ?",
		adminpkg.ComponentAccessType.String(), componentPath(showCategories, OperationRead), "GET",
	).Count(&readPolicies).Error; err != nil || readPolicies != 1 {
		t.Fatalf("read policy count=%d error=%v", readPolicies, err)
	}
	var roguePolicies int64
	if err := db.Model(new(models.CasbinRule)).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p", "rogue-role", adminpkg.ComponentAccessType.String(), rogueComponent.Path, "GET",
	).Count(&roguePolicies).Error; err != nil || roguePolicies != 0 {
		t.Fatalf("rogue write policy count=%d error=%v", roguePolicies, err)
	}
	var versions int64
	if err := db.Model(new(migrationmodels.Migration)).Where("version IN ?", []string{
		AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(), CapabilityLockdownMigrationID.String(),
	}).Count(&versions).Error; err != nil || versions != 3 {
		t.Fatalf("migration versions=%d error=%v", versions, err)
	}
	var adminRole models.Role
	if err := db.Where("name = ?", "admin").Take(&adminRole).Error; err != nil {
		t.Fatal(err)
	}
	for _, expectation := range []struct {
		scope, ownerID string
		wantRevision   int64
	}{
		{scope: "global", ownerID: "", wantRevision: 3},
		{scope: "role", ownerID: adminRole.ID, wantRevision: 3},
		{scope: "role", ownerID: "rogue-role", wantRevision: 1},
	} {
		var revision models.ConfigRevision
		if err := db.Where(
			"scope = ? AND owner_id = ? AND resource = ?", expectation.scope, expectation.ownerID, authorizationRevisionResource,
		).Take(&revision).Error; err != nil || revision.Revision != expectation.wantRevision {
			t.Errorf("authorization revision scope=%q owner=%q revision=%d want=%d error=%v", expectation.scope, expectation.ownerID, revision.Revision, expectation.wantRevision, err)
		}
	}
	if err := verifyCapabilityLockdownReadiness(t.Context(), db); err != nil {
		t.Fatalf("capability lockdown readiness: %v", err)
	}
	if db.Migrator().HasTable("show_categories") || db.Migrator().HasTable("orders") {
		t.Fatal("capability lockdown created a legacy business table")
	}
}

func TestCapabilityLockdownRequiresBothPublishedPrerequisites(t *testing.T) {
	db := openMigrationTestDatabase(t)
	if err := migrateAuthorization(db, AuthorizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}
	if err := migrateCapabilityLockdown(db, CapabilityLockdownMigrationID.String()); err == nil {
		t.Fatal("capability lockdown accepted a missing localization prerequisite")
	}
	var versions int64
	if err := db.Model(new(migrationmodels.Migration)).Count(&versions).Error; err != nil || versions != 1 {
		t.Fatalf("migration versions=%d error=%v", versions, err)
	}
}
