package legacydb

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRepositoryUsesOnlyFixedSharedSchemaAndRejectsAllMutations(t *testing.T) {
	t.Parallel()

	db, binding := openLegacyTestDatabase(t)
	registry := DefaultRegistry()
	createLegacyRelations(t, db, binding.SharedSchema, registry)
	if err := db.Exec(`CREATE TABLE brands (id TEXT PRIMARY KEY, name_zh TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO brands (id, name_zh) VALUES ('wrong-schema', 'must-not-leak')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO "shared"."brands" (id, name_zh, name_en, status, description) VALUES ('178819869911563900', '中文品牌', 'Brand', 1, 'visible')`).Error; err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db, binding, registry)
	if err != nil {
		t.Fatal(err)
	}

	page, err := repository.List(context.Background(), "brands", Query{Page: 1, PageSize: 20, Search: "brand"})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if page.Total != 1 || len(page.Data) != 1 || page.Data[0]["id"] == "wrong-schema" {
		t.Fatalf("qualified list = %#v", page)
	}
	record, descriptor, err := repository.Get(context.Background(), "brands", "178819869911563900")
	if err != nil || record["description"] != "visible" || descriptor.Capabilities != (Capabilities{Detail: true}) {
		t.Fatalf("Get() = %#v, descriptor=%#v, err=%v", record, descriptor, err)
	}
	if _, _, err := repository.Create(context.Background(), "brands", map[string]any{"name_en": "blocked"}); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("Create() error = %v", err)
	}
	if _, _, err := repository.Update(context.Background(), "brands", "178819869911563900", map[string]any{"description": "blocked"}); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := repository.Delete(context.Background(), "brands", "178819869911563900"); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("Delete() error = %v", err)
	}
	var unchanged int64
	if err := db.Table(binding.SharedSchema+".brands").Where("id = ? AND description = ? AND deleted_at IS NULL", "178819869911563900", "visible").Count(&unchanged).Error; err != nil || unchanged != 1 {
		t.Fatalf("unchanged rows = %d, err=%v", unchanged, err)
	}
}

func TestRepositoryRejectsWritesForAllResourcesAndRedactsNestedSecrets(t *testing.T) {
	t.Parallel()

	db, binding := openLegacyTestDatabase(t)
	registry := DefaultRegistry()
	createLegacyRelations(t, db, binding.SharedSchema, registry)
	repository, err := NewRepository(db, binding, registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range registry.All() {
		resource := definition.Resource.Name
		if _, _, err := repository.Create(context.Background(), resource, map[string]any{"name": "unsafe"}); !errors.Is(err, ErrOperationNotSupported) {
			t.Errorf("Create(%s) error = %v", resource, err)
		}
		if _, _, err := repository.Update(context.Background(), resource, "178819869911563900", map[string]any{"name": "unsafe"}); !errors.Is(err, ErrOperationNotSupported) {
			t.Errorf("Update(%s) error = %v", resource, err)
		}
		if _, err := repository.Delete(context.Background(), resource, "178819869911563900"); !errors.Is(err, ErrOperationNotSupported) {
			t.Errorf("Delete(%s) error = %v", resource, err)
		}
	}

	if err := db.Exec(`INSERT INTO "shared"."classes" (id, name, attributes) VALUES ('178819869911563900', 'Class', '{"visible":"yes","api_secret":"must-redact"}')`).Error; err != nil {
		t.Fatal(err)
	}
	record, _, err := repository.Get(context.Background(), "classes", "178819869911563900")
	if err != nil {
		t.Fatal(err)
	}
	attributes, ok := record["attributes"].(map[string]any)
	if !ok || attributes["visible"] != "yes" || attributes["api_secret"] != nil {
		t.Fatalf("sanitized attributes = %#v", record["attributes"])
	}
}

func TestRepositorySearchEscapesWildcardsAndSupportsReviewedFilters(t *testing.T) {
	t.Parallel()

	db, binding := openLegacyTestDatabase(t)
	registry := DefaultRegistry()
	createLegacyRelations(t, db, binding.SharedSchema, registry)
	repository, err := NewRepository(db, binding, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO "shared"."brands" (id, name_zh, name_en, status) VALUES ('178819869911563900', '100%_! literal', 'Literal', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO "shared"."brands" (id, name_zh, name_en, status) VALUES ('178819869911563901', '100xx! literal', 'Other', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	page, err := repository.List(context.Background(), "brands", Query{Search: "%_!"})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if page.Total != 1 || len(page.Data) != 1 || page.Data[0]["id"] != "178819869911563900" {
		t.Fatalf("literal wildcard search = %#v", page)
	}
	page, err = repository.List(context.Background(), "brands", Query{
		SortBy: "name_en", SortOrder: "desc",
		Exact: map[string]string{"status": "1"}, IContains: map[string]string{"name_zh": "%_!"},
	})
	if err != nil || page.Total != 1 || len(page.Data) != 1 || page.Data[0]["id"] != "178819869911563900" {
		t.Fatalf("filtered list = %#v, err=%v", page, err)
	}
	if _, err := repository.List(context.Background(), "brands", Query{Exact: map[string]string{"schema": "main"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown filter error = %v", err)
	}
}

func TestVerifyReadinessRequiresEveryReviewedColumn(t *testing.T) {
	t.Parallel()

	db, binding := openLegacyTestDatabase(t)
	registry := DefaultRegistry()
	createLegacyRelations(t, db, binding.SharedSchema, registry)
	if err := VerifyReadiness(context.Background(), db, binding, registry); err != nil {
		t.Fatalf("VerifyReadiness(): %v", err)
	}
	if err := db.Exec(`DROP TABLE "shared"."courier_links"`).Error; err != nil {
		t.Fatal(err)
	}
	if err := VerifyReadiness(context.Background(), db, binding, registry); !errors.Is(err, ErrSchemaNotReady) {
		t.Fatalf("VerifyReadiness(missing table) = %v", err)
	}
}

func TestDatabaseErrorsAreStableAndRedacted(t *testing.T) {
	t.Parallel()

	if err := classifyDatabaseError(errors.New("UNIQUE constraint failed: secret.value")); !errors.Is(err, ErrConflict) {
		t.Fatalf("unique error = %v", err)
	}
	err := classifyDatabaseError(errors.New("password=hunter2 connection failed"))
	if !errors.Is(err, ErrPersistence) || regexp.MustCompile(`hunter2|password`).MatchString(err.Error()) {
		t.Fatalf("persistence error was not redacted: %v", err)
	}
}

func openLegacyTestDatabase(t *testing.T) (*gorm.DB, fixedbinding.Binding) {
	t.Helper()
	directory := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(directory, "main.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for schema, path := range map[string]string{
		"core":   filepath.Join(directory, "core.db"),
		"shared": filepath.Join(directory, "shared.db"),
	} {
		if err := db.Exec("ATTACH DATABASE ? AS "+quoteIdentifier(schema), path).Error; err != nil {
			t.Fatalf("attach %s: %v", schema, err)
		}
	}
	return db, fixedbinding.Binding{CoreSchema: "core", SharedSchema: "shared"}
}

func createLegacyRelations(t *testing.T, db *gorm.DB, schema string, registry Registry) {
	t.Helper()
	for _, definition := range registry.All() {
		columns := make([]string, 0, len(definition.Resource.Columns))
		for _, column := range definition.Resource.Columns {
			columnType := "TEXT"
			switch column.Type {
			case ColumnNumber:
				columnType = "NUMERIC"
			case ColumnBoolean:
				columnType = "INTEGER"
			}
			declaration := quoteIdentifier(column.Name) + " " + columnType
			if column.Name == "id" {
				declaration += " PRIMARY KEY"
			}
			columns = append(columns, declaration)
		}
		statement := fmt.Sprintf("CREATE TABLE %s (%s)", qualifiedTable(schema, definition.Resource.Name), stringsJoin(columns, ", "))
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create %s: %v", definition.Resource.Name, err)
		}
	}
}

func stringsJoin(values []string, separator string) string {
	result := ""
	for index, value := range values {
		if index != 0 {
			result += separator
		}
		result += value
	}
	return result
}
