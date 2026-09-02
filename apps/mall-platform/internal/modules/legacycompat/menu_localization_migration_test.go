package legacycompat

import (
	"errors"
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
)

func TestMenuLocalizationMigrationIsForwardOnlyAndIdempotent(t *testing.T) {
	db := openMigrationTestDatabase(t)
	if err := migrateAuthorization(db, AuthorizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}

	var original models.Menu
	if err := db.Where("type = ? AND path = ?", adminpkg.DirectoryAccessType, businessMenuRoot).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	if original.Name != "业务管理" {
		t.Fatalf("published root name = %q", original.Name)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := migrateMenuLocalization(db, MenuLocalizationMigrationID.String()); err != nil {
			t.Fatalf("localization migration attempt %d: %v", attempt, err)
		}
	}

	var localized models.Menu
	if err := db.Where("type = ? AND path = ?", adminpkg.DirectoryAccessType, businessMenuRoot).Take(&localized).Error; err != nil {
		t.Fatal(err)
	}
	if localized.Name != "legacyBusiness" {
		t.Fatalf("localized root name = %q, want %q", localized.Name, businessMenuNameToken)
	}
	var versions int64
	if err := db.Model(new(migrationmodels.Migration)).Where("version IN ?", []string{
		AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(),
	}).Count(&versions).Error; err != nil || versions != 2 {
		t.Fatalf("migration versions = %d, err=%v", versions, err)
	}
	var staleRevisions int64
	if err := db.Model(new(models.ConfigRevision)).Where("resource = ? AND revision != ?", authorizationRevisionResource, 2).Count(&staleRevisions).Error; err != nil || staleRevisions != 0 {
		t.Fatalf("stale authorization revisions = %d, err=%v", staleRevisions, err)
	}
	if err := verifyMenuLocalizationReadiness(t.Context(), db); err != nil {
		t.Fatalf("localization readiness: %v", err)
	}
	if db.Migrator().HasTable("show_categories") || db.Migrator().HasTable("orders") {
		t.Fatal("menu localization migration created a legacy business table")
	}
}

func TestMenuLocalizationMigrationRequiresAuthorizationProjection(t *testing.T) {
	db := openMigrationTestDatabase(t)
	if err := migrateMenuLocalization(db, MenuLocalizationMigrationID.String()); err == nil {
		t.Fatal("menu localization migration accepted a missing prerequisite")
	}
	var versions int64
	if err := db.Model(new(migrationmodels.Migration)).Count(&versions).Error; err != nil || versions != 0 {
		t.Fatalf("migration versions = %d, err=%v", versions, err)
	}
}

func TestRegisterMigrationIncludesMenuLocalizationCorrection(t *testing.T) {
	runner := migration.New()
	if err := RegisterMigration(runner); err != nil {
		t.Fatal(err)
	}
	if err := runner.Register(AuthorizationMigrationID, migrateAuthorization); !errors.Is(err, migration.ErrDuplicateMigrationID) {
		t.Fatalf("authorization migration registration = %v", err)
	}
	if err := runner.Register(MenuLocalizationMigrationID, migrateMenuLocalization); !errors.Is(err, migration.ErrDuplicateMigrationID) {
		t.Fatalf("menu localization migration registration = %v", err)
	}
	if err := runner.Register(CapabilityLockdownMigrationID, migrateCapabilityLockdown); !errors.Is(err, migration.ErrDuplicateMigrationID) {
		t.Fatalf("capability lockdown migration registration = %v", err)
	}
}
