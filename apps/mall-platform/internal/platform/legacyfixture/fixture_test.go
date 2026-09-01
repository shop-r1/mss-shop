package legacyfixture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestApplyCreatesAllReviewedTablesIsIdempotentAndReady(t *testing.T) {
	root := newMallRoot(t)
	target := filepath.Join(root, DatabaseFilename)
	database := openTestSQLite(t, target)
	if err := database.Exec(`CREATE TABLE "core_marker" ("id" TEXT PRIMARY KEY, "value" TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO "core_marker" ("id", "value") VALUES (?, ?)`, "keep", "original").Error; err != nil {
		t.Fatal(err)
	}
	binding := fixtureBinding("legacy-demo")
	if err := legacydb.VerifyReadiness(context.Background(), database, binding, legacydb.DefaultRegistry()); !errors.Is(err, legacydb.ErrSchemaNotReady) {
		t.Fatalf("readiness before fixture = %v, want ErrSchemaNotReady", err)
	}
	closeTestSQLite(t, database)

	options := Options{
		RootDir: root, DatabasePath: target, LegacyTenantID: "legacy-demo", Confirmed: true,
	}
	first, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if first.TableCount != legacydb.ExpectedMallResourceCount || first.CreatedTables != legacydb.ExpectedMallResourceCount {
		t.Fatalf("first result = %#v", first)
	}
	if first.InsertedRows != int64(len(demoRows("legacy-demo"))) {
		t.Fatalf("inserted rows = %d, want %d", first.InsertedRows, len(demoRows("legacy-demo")))
	}

	database = openTestSQLite(t, target)
	if got := countReviewedTables(t, database); got != legacydb.ExpectedMallResourceCount {
		t.Fatalf("reviewed table count = %d, want %d", got, legacydb.ExpectedMallResourceCount)
	}
	if err := legacydb.VerifyReadiness(context.Background(), database, binding, legacydb.DefaultRegistry()); err != nil {
		t.Fatalf("readiness after fixture: %v", err)
	}
	if err := database.Exec(
		`UPDATE "function_circles" SET "title" = ? WHERE "id" = ?`,
		"手工保留值", "910000000000000001",
	).Error; err != nil {
		t.Fatal(err)
	}
	closeTestSQLite(t, database)

	second, err := Apply(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if second.CreatedTables != 0 || second.InsertedRows != 0 {
		t.Fatalf("second result is not idempotent: %#v", second)
	}
	database = openTestSQLite(t, target)
	defer closeTestSQLite(t, database)
	var title string
	if err := database.Raw(
		`SELECT "title" FROM "function_circles" WHERE "id" = ?`, "910000000000000001",
	).Scan(&title).Error; err != nil {
		t.Fatal(err)
	}
	if title != "手工保留值" {
		t.Fatalf("fixture overwrote existing data: title = %q", title)
	}
	var marker string
	if err := database.Raw(`SELECT "value" FROM "core_marker" WHERE "id" = ?`, "keep").Scan(&marker).Error; err != nil {
		t.Fatal(err)
	}
	if marker != "original" {
		t.Fatalf("unrelated local data changed: %q", marker)
	}
}

func TestApplyRejectsDangerousTargetsBeforeDatabaseWork(t *testing.T) {
	root := newMallRoot(t)
	target := filepath.Join(root, DatabaseFilename)
	base := Options{RootDir: root, DatabasePath: target, LegacyTenantID: "legacy-demo", Confirmed: true}

	tests := []struct {
		name    string
		prepare func(t *testing.T) Options
		want    string
	}{
		{
			name: "confirmation omitted",
			prepare: func(*testing.T) Options {
				options := base
				options.Confirmed = false
				return options
			},
			want: "confirm-local-ui-fixture",
		},
		{
			name: "tenant omitted",
			prepare: func(*testing.T) Options {
				options := base
				options.LegacyTenantID = ""
				return options
			},
			want: "legacy-tenant-id",
		},
		{
			name: "invalid tenant",
			prepare: func(t *testing.T) Options {
				database := openTestSQLite(t, target)
				if err := database.Exec(`CREATE TABLE "mss_setup_marker" ("id" TEXT PRIMARY KEY)`).Error; err != nil {
					t.Fatal(err)
				}
				closeTestSQLite(t, database)
				options := base
				options.LegacyTenantID = " legacy-demo"
				return options
			},
			want: "invalid legacy tenant ID",
		},
		{
			name: "PostgreSQL URL",
			prepare: func(*testing.T) Options {
				options := base
				options.DatabasePath = "postgres://example.invalid/r1shop"
				return options
			},
			want: "DSNs",
		},
		{
			name: "SQLite DSN",
			prepare: func(*testing.T) Options {
				options := base
				options.DatabasePath = "file:mss-boot-admin-local.db?mode=rwc"
				return options
			},
			want: "DSNs",
		},
		{
			name: "outside module",
			prepare: func(t *testing.T) Options {
				options := base
				options.DatabasePath = filepath.Join(t.TempDir(), DatabaseFilename)
				return options
			},
			want: "escapes the mall-platform root",
		},
		{
			name: "missing SQLite file",
			prepare: func(*testing.T) Options {
				return base
			},
			want: "run mss setup first",
		},
		{
			name: "directory",
			prepare: func(t *testing.T) Options {
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				return base
			},
			want: "regular file",
		},
		{
			name: "non SQLite existing file",
			prepare: func(t *testing.T) Options {
				if err := os.WriteFile(target, []byte("this is not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
				return base
			},
			want: "not a valid SQLite",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isolatedRoot := newMallRoot(t)
			root, target = isolatedRoot, filepath.Join(isolatedRoot, DatabaseFilename)
			base = Options{RootDir: root, DatabasePath: target, LegacyTenantID: "legacy-demo", Confirmed: true}
			options := test.prepare(t)
			_, err := Apply(context.Background(), options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Apply() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestApplyRejectsSymlinkAndIncompatibleExistingRelation(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := newMallRoot(t)
		external := filepath.Join(t.TempDir(), "external.db")
		database := openTestSQLite(t, external)
		closeTestSQLite(t, database)
		if err := os.Symlink(external, filepath.Join(root, DatabaseFilename)); err != nil {
			t.Fatal(err)
		}
		_, err := Apply(context.Background(), Options{
			RootDir: root, DatabasePath: filepath.Join(root, DatabaseFilename),
			LegacyTenantID: "legacy-demo", Confirmed: true,
		})
		if err == nil || !strings.Contains(err.Error(), "symbolic-link") {
			t.Fatalf("Apply() error = %v, want symlink rejection", err)
		}
	})

	t.Run("incompatible existing table", func(t *testing.T) {
		root := newMallRoot(t)
		target := filepath.Join(root, DatabaseFilename)
		database := openTestSQLite(t, target)
		if err := database.Exec(`CREATE TABLE "show_categories" ("id" TEXT PRIMARY KEY)`).Error; err != nil {
			t.Fatal(err)
		}
		closeTestSQLite(t, database)

		_, err := Apply(context.Background(), Options{
			RootDir: root, DatabasePath: target, LegacyTenantID: "legacy-demo", Confirmed: true,
		})
		if err == nil || !strings.Contains(err.Error(), "existing show_categories is incompatible") {
			t.Fatalf("Apply() error = %v, want incompatible relation rejection", err)
		}
		database = openTestSQLite(t, target)
		defer closeTestSQLite(t, database)
		var count int64
		if err := database.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("rejected fixture created additional tables: %d", count)
		}
	})
}

func TestSQLiteReadinessDoesNotPretendToProvideCoreSchemaIsolation(t *testing.T) {
	root := newMallRoot(t)
	target := filepath.Join(root, DatabaseFilename)
	database := openTestSQLite(t, target)
	if err := database.Exec(`CREATE TABLE "mss_setup_marker" ("id" TEXT PRIMARY KEY)`).Error; err != nil {
		t.Fatal(err)
	}
	closeTestSQLite(t, database)
	_, err := Apply(context.Background(), Options{
		RootDir: root, DatabasePath: target, LegacyTenantID: "legacy-demo", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	database = openTestSQLite(t, target)
	defer closeTestSQLite(t, database)

	var schemas []struct {
		Name string `gorm:"column:name"`
	}
	if err := database.Raw("PRAGMA database_list").Scan(&schemas).Error; err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 || schemas[0].Name != "main" {
		t.Fatalf("fixture attached an unexpected SQLite schema: %#v", schemas)
	}
	if err := legacydb.VerifyReadiness(
		context.Background(), database, fixtureBinding("legacy-demo"), legacydb.DefaultRegistry(),
	); err != nil {
		t.Fatalf("mall business SQLite readiness: %v", err)
	}
}

func newMallRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(moduleDeclaration+"\n\ngo 1.26.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".mss"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mss", "project.yaml"), []byte("kind: Project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func fixtureBinding(tenantID string) fixedbinding.Binding {
	return fixedbinding.Binding{
		TenantID: "local-ui-control", AdminTenantID: fixedbinding.MSS137AdminTenantID,
		LegacyTenantID: tenantID, BusinessSchema: "main",
	}
}

func openTestSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func closeTestSQLite(t *testing.T, database *gorm.DB) {
	t.Helper()
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDatabase.Close(); err != nil {
		t.Fatal(err)
	}
}

func countReviewedTables(t *testing.T, database *gorm.DB) int {
	t.Helper()
	count := 0
	for _, definition := range legacydb.DefaultRegistry().All() {
		var found int
		if err := database.Raw(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			definition.Resource.Name,
		).Scan(&found).Error; err != nil {
			t.Fatal(err)
		}
		count += found
	}
	return count
}
