package legacycompat

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAuthorizationMigrationIsForwardOnlyCoreProjection(t *testing.T) {
	db := openMigrationTestDatabase(t)
	if err := migrateAuthorization(db, AuthorizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}
	registry := legacydb.DefaultRegistry()
	wantOperations := 0
	for _, definition := range registry.All() {
		wantOperations += len(authorizationMigrationOperationsFor(definition))
	}
	wantMenus := int64(1 + len(legacydb.FrontendDomains()) + legacydb.ExpectedMallResourceCount + wantOperations + 5)
	for name, modelAndCount := range map[string]struct {
		model any
		want  int64
	}{
		"roles":     {model: new(models.Role), want: 1},
		"menus":     {model: new(models.Menu), want: wantMenus},
		"policies":  {model: new(models.CasbinRule), want: wantMenus},
		"versions":  {model: new(migrationmodels.Migration), want: 1},
		"revisions": {model: new(models.ConfigRevision), want: 2},
	} {
		var count int64
		if err := db.Model(modelAndCount.model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != modelAndCount.want {
			t.Errorf("%s count = %d, want %d", name, count, modelAndCount.want)
		}
	}
	if db.Migrator().HasTable("show_categories") || db.Migrator().HasTable("orders") {
		t.Fatal("authorization migration created a legacy table")
	}
	var businessRoot models.Menu
	if err := db.Where("type = ? AND path = ?", adminpkg.DirectoryAccessType, businessMenuRoot).Take(&businessRoot).Error; err != nil {
		t.Fatalf("load business root: %v", err)
	}
	for _, domain := range legacydb.FrontendDomains() {
		var domainMenu models.Menu
		if err := db.Where("type = ? AND path = ?", adminpkg.DirectoryAccessType, domainMenuPath(domain)).Take(&domainMenu).Error; err != nil {
			t.Errorf("load domain %s: %v", domain, err)
			continue
		}
		if domainMenu.ParentID != businessRoot.ID {
			t.Errorf("domain %s parent = %q, want %q", domain, domainMenu.ParentID, businessRoot.ID)
		}
	}
	for _, definition := range registry.All() {
		var domainMenu, resourceMenu models.Menu
		if err := db.Where("type = ? AND path = ?", adminpkg.DirectoryAccessType, domainMenuPath(definition.Resource.Domain)).Take(&domainMenu).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Where("type = ? AND path = ?", adminpkg.MenuAccessType, menuPath(definition)).Take(&resourceMenu).Error; err != nil {
			t.Errorf("load resource %s: %v", definition.Resource.Name, err)
			continue
		}
		if resourceMenu.ParentID != domainMenu.ID {
			t.Errorf("resource %s parent = %q, want domain %q", definition.Resource.Name, resourceMenu.ParentID, domainMenu.ID)
		}
	}
	var duplicatePaths []struct {
		Path  string `gorm:"column:path"`
		Count int64  `gorm:"column:count"`
	}
	if err := db.Model(new(models.Menu)).
		Select("path, COUNT(*) AS count").
		Where("type IN ?", []adminpkg.AccessType{adminpkg.DirectoryAccessType, adminpkg.MenuAccessType, adminpkg.ComponentAccessType}).
		Group("path").Having("COUNT(*) > 1").Scan(&duplicatePaths).Error; err != nil || len(duplicatePaths) != 0 {
		t.Fatalf("duplicate navigation/component paths=%#v error=%v", duplicatePaths, err)
	}
	var courierLinkMenus int64
	if err := db.Model(new(models.Menu)).Where("path LIKE ?", "%courier_links%").Count(&courierLinkMenus).Error; err != nil || courierLinkMenus != 0 {
		t.Fatalf("courier_links menu count=%d error=%v", courierLinkMenus, err)
	}
	var componentRules int64
	if err := db.Model(new(models.CasbinRule)).Where(
		"v1 = ? AND v2 = ? AND v3 = ?",
		adminpkg.ComponentAccessType.String(), ComponentPath("show_categories", OperationCreate), "GET",
	).Count(&componentRules).Error; err != nil || componentRules != 1 {
		t.Fatalf("historical create component rules=%d error=%v", componentRules, err)
	}
	if err := migrateAuthorization(db, AuthorizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}
	var menusAfter int64
	if err := db.Model(new(models.Menu)).Count(&menusAfter).Error; err != nil || menusAfter != wantMenus {
		t.Fatalf("idempotent menus=%d error=%v", menusAfter, err)
	}
}

func openMigrationTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	// These are MSS core test fixtures only. No legacy business model is present.
	if err := db.Migrator().CreateTable(
		new(models.Role), new(models.Menu), new(models.CasbinRule),
		new(models.ConfigRevision), new(migrationmodels.Migration),
	); err != nil {
		t.Fatal(err)
	}
	return db
}
