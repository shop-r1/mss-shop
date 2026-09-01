package adminprojection

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	projectionTestPermission  = "example:read"
	projectionTestComponent   = "/business/example/permissions/read"
	projectionTestRoute       = "/admin/api/example"
	projectionTestMigrationID = migration.MigrationID("66966149766990")
)

func projectionTestContract() Projection {
	return Projection{
		Name:        "example",
		MigrationID: projectionTestMigrationID,
		DefaultRole: RoleSeed{Name: "admin", Remark: "example default role"},
		Menus: []MenuSeed{
			{
				Name: "business", Path: "/business", Method: "GET",
				AccessType: adminpkg.DirectoryAccessType, Permission: "business:access",
				Icon: "database", Sort: 10,
			},
			{
				Name: "example", Path: "/business/example", Method: "GET",
				ParentPath: "/business", AccessType: adminpkg.MenuAccessType,
				Permission: projectionTestPermission, Icon: "setting", Sort: 20,
			},
			{
				Name: projectionTestPermission, Path: projectionTestComponent, Method: "GET",
				ParentPath: "/business/example", AccessType: adminpkg.ComponentAccessType,
				Permission: projectionTestPermission, Hidden: true,
			},
			{
				Name: "api.example.read", Path: projectionTestRoute, Method: "GET",
				ParentPath: projectionTestComponent, AccessType: adminpkg.APIAccessType,
				Permission: projectionTestPermission, Hidden: true,
			},
		},
		Routes: []RouteGrant{
			{
				Permission: projectionTestPermission, Method: "GET",
				Path: projectionTestRoute, ComponentPath: projectionTestComponent,
			},
		},
	}
}

func openProjectionTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "admin-projection.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrator().CreateTable(
		new(models.Role), new(models.Menu), new(models.CasbinRule),
		new(models.ConfigRevision), new(migrationmodels.Migration),
	); err != nil {
		t.Fatal(err)
	}
	return database
}

func applyProjectionTestMigration(t *testing.T, database *gorm.DB, projection Projection) {
	t.Helper()
	runner := migration.New()
	if err := RegisterMigration(runner, projection); err != nil {
		t.Fatal(err)
	}
	runner.SetDb(database)
	runner.SetModel(new(migrationmodels.Migration))
	if err := runner.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}
