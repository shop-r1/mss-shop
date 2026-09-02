package legacydb

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRepositoryDiscardsLegacySQLLogs(t *testing.T) {
	t.Parallel()
	_, repository := newLegacyTestRepository(t)
	if repository.db.Logger != logger.Discard {
		t.Fatal("legacy repository did not install its isolated discard logger")
	}
}

func TestListAppliesFixedTenantSearchFiltersAndPaging(t *testing.T) {
	t.Parallel()
	db, repository := newLegacyTestRepository(t)
	definition := mustDefinition(t, repository.registry, "show_categories")
	createFixtureTable(t, db, definition)
	insertFixture(t, db, `INSERT INTO "main"."show_categories" (id, tenant_id, name, status, sort) VALUES (?, ?, ?, ?, ?)`, "one", "legacy-one", "Alpha %_! sale", 1, 2)
	insertFixture(t, db, `INSERT INTO "main"."show_categories" (id, tenant_id, name, status, sort) VALUES (?, ?, ?, ?, ?)`, "two", "legacy-one", "alpha regular", 1, 1)
	insertFixture(t, db, `INSERT INTO "main"."show_categories" (id, tenant_id, name, status, sort) VALUES (?, ?, ?, ?, ?)`, "foreign", "legacy-two", "Alpha foreign", 1, 0)

	page, err := repository.List(context.Background(), "show_categories", Query{
		Page:      1,
		PageSize:  1,
		Search:    "ALPHA",
		SortBy:    "sort",
		SortOrder: "asc",
		Exact:     map[string]string{"status": "1"},
		Contains:  map[string]string{"name": "alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Data) != 1 || page.Data[0]["id"] != "two" {
		t.Fatalf("fixed-tenant page = %#v", page)
	}
	if page.Page != 1 || page.PageSize != 1 || page.Resource.Name != "show_categories" {
		t.Fatalf("page metadata = %#v", page)
	}

	literal, err := repository.List(context.Background(), "show_categories", Query{
		Contains: map[string]string{"name": "%"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if literal.Total != 1 || literal.Data[0]["id"] != "one" {
		t.Fatalf("LIKE wildcard was not escaped: %#v", literal)
	}
	wildcardSearch, err := repository.List(context.Background(), "show_categories", Query{Search: "%_!"})
	if err != nil {
		t.Fatal(err)
	}
	if wildcardSearch.Total != 1 || wildcardSearch.Data[0]["id"] != "one" {
		t.Fatalf("q wildcard escape result = %#v", wildcardSearch)
	}

	_, err = repository.List(context.Background(), "show_categories", Query{
		Exact: map[string]string{"tenant_id": "legacy-two"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("request tenant override error = %v", err)
	}
}

func TestInheritedScopeUsesQualifiedOwningTable(t *testing.T) {
	t.Parallel()
	db, repository := newLegacyTestRepository(t)
	orders := mustDefinition(t, repository.registry, "orders")
	orderGoods := mustDefinition(t, repository.registry, "order_goods")
	createFixtureTable(t, db, orders)
	createFixtureTable(t, db, orderGoods)
	insertFixture(t, db, `INSERT INTO "main"."orders" (id, tenant_id) VALUES (?, ?)`, "order-one", "legacy-one")
	insertFixture(t, db, `INSERT INTO "main"."orders" (id, tenant_id) VALUES (?, ?)`, "order-two", "legacy-two")
	insertFixture(t, db, `INSERT INTO "main"."order_goods" (id, order_id, goods_id) VALUES (?, ?, ?)`, "line-one", "order-one", "goods-one")
	insertFixture(t, db, `INSERT INTO "main"."order_goods" (id, order_id, goods_id) VALUES (?, ?, ?)`, "line-two", "order-two", "goods-two")

	page, err := repository.List(context.Background(), "order_goods", Query{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Data) != 1 || page.Data[0]["id"] != "line-one" {
		t.Fatalf("inherited tenant page = %#v", page)
	}
}

func TestSchemaScopedSnapshotUsesOnlyTheFixedBusinessSchema(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(directory, "core.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ATTACH DATABASE ? AS "mall_biz"`, filepath.Join(directory, "mall-biz.db")).Error; err != nil {
		t.Fatal(err)
	}
	binding := fixedbinding.Binding{
		TenantID: "control-one", AdminTenantID: "default",
		LegacyTenantID: "legacy-one", BusinessSchema: "mall_biz",
	}
	repository, err := NewRepository(db, binding, DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	brands := mustDefinition(t, repository.registry, "brands")
	if brands.Scope != ScopeSchema || brands.TenantColumn != "" || brands.Inherited != nil {
		t.Fatalf("brands scope = %#v", brands)
	}
	createFixtureTableInSchema(t, db, "main", brands)
	createFixtureTableInSchema(t, db, binding.BusinessSchema, brands)
	insertFixture(t, db, `INSERT INTO "main"."brands" (id, name_en) VALUES (?, ?)`, "forged", "Wrong schema")
	insertFixture(t, db, `INSERT INTO "mall_biz"."brands" (id, name_en) VALUES (?, ?)`, "local", "Tenant snapshot")

	page, err := repository.List(context.Background(), "brands", Query{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Data) != 1 || page.Data[0]["id"] != "local" {
		t.Fatalf("schema-scoped page = %#v", page)
	}
	if _, err := repository.List(context.Background(), "brands", Query{Exact: map[string]string{"tenant_id": "foreign"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("schema-scoped tenant predicate override error = %v", err)
	}
}

func TestRepositoryRejectsImplicitOrIncompleteScope(t *testing.T) {
	t.Parallel()
	db, _ := newLegacyTestRepository(t)
	registry := DefaultRegistry()
	brands := registry.definitions["brands"]
	brands.Scope = ""
	registry.definitions["brands"] = brands
	if _, err := NewRepository(db, fixedbinding.Binding{
		TenantID: "control", AdminTenantID: "default", LegacyTenantID: "legacy", BusinessSchema: "main",
	}, registry); err == nil || !strings.Contains(err.Error(), "unsupported scope") {
		t.Fatalf("implicit scope error = %v", err)
	}
}

func TestCompositePrimaryKeyWithIDSupportsFixedTenantDetail(t *testing.T) {
	t.Parallel()
	db, repository := newLegacyTestRepository(t)
	definition := mustDefinition(t, repository.registry, "activities")
	createFixtureTable(t, db, definition)
	insertFixture(t, db, `INSERT INTO "main"."activities" (id, tenant_id, name) VALUES (?, ?, ?)`, "shared-id", "legacy-one", "local")
	insertFixture(t, db, `INSERT INTO "main"."activities" (id, tenant_id, name) VALUES (?, ?, ?)`, "shared-id", "legacy-two", "foreign")

	record, resource, err := repository.Get(context.Background(), "activities", "shared-id")
	if err != nil {
		t.Fatal(err)
	}
	if record["name"] != "local" || resource.Capabilities != (Capabilities{Detail: true}) {
		t.Fatalf("fixed composite detail record=%#v resource=%#v", record, resource)
	}
}

func TestAllLegacyMutationsFailBeforeValidationOrDatabaseAccess(t *testing.T) {
	t.Parallel()
	db, repository := newLegacyTestRepository(t)
	createFixtureTable(t, db, mustDefinition(t, repository.registry, "show_categories"))
	createFixtureTable(t, db, mustDefinition(t, repository.registry, "function_circles"))

	for _, test := range []struct {
		resource string
		input    map[string]any
	}{
		{resource: "show_categories", input: map[string]any{"name": "category", "status": "enabled"}},
		{resource: "show_categories", input: map[string]any{"name": 123, "status": 1}},
		{resource: "function_circles", input: map[string]any{"title": "slot", "video": "true"}},
	} {
		if _, _, err := repository.Create(context.Background(), test.resource, test.input); !errors.Is(err, ErrOperationNotSupported) {
			t.Errorf("%s create error = %v", test.resource, err)
		}
		if _, _, err := repository.Update(context.Background(), test.resource, "missing", test.input); !errors.Is(err, ErrOperationNotSupported) {
			t.Errorf("%s update error = %v", test.resource, err)
		}
		if _, err := repository.Delete(context.Background(), test.resource, "missing"); !errors.Is(err, ErrOperationNotSupported) {
			t.Errorf("%s delete error = %v", test.resource, err)
		}
	}
}

func TestSecretsAreSelectedAsNullAndNestedMetadataIsRedacted(t *testing.T) {
	t.Parallel()
	db, repository := newLegacyTestRepository(t)
	members := mustDefinition(t, repository.registry, "members")
	configs := mustDefinition(t, repository.registry, "system_configs")
	createFixtureTable(t, db, members)
	createFixtureTable(t, db, configs)
	insertFixture(t, db, `INSERT INTO "main"."members" (id, tenant_id, username, password_hash, salt, rest_password_hash, open_id, union_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "member-one", "legacy-one", "visible", "hash", "salt", "reset", "openid", "unionid")
	insertFixture(t, db, `INSERT INTO "main"."system_configs" (id, tenant_id, name, metadata) VALUES (?, ?, ?, ?)`, "config-one", "legacy-one", "wechat", `{"appId":"visible","appSecret":"hidden","nested":{"access_token":"hidden-too","region":"au"}}`)

	member, _, err := repository.Get(context.Background(), "members", "member-one")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"password_hash", "salt", "rest_password_hash", "open_id", "union_id"} {
		if value, exists := member[field]; !exists || value != nil {
			t.Errorf("member secret %s = %#v", field, value)
		}
	}
	config, _, err := repository.Get(context.Background(), "system_configs", "config-one")
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := config["metadata"].(map[string]any)
	if !ok || metadata["appId"] != "visible" || metadata["appSecret"] != nil {
		t.Fatalf("redacted metadata = %#v", config["metadata"])
	}
	nested, ok := metadata["nested"].(map[string]any)
	if !ok || nested["access_token"] != nil || nested["region"] != "au" {
		t.Fatalf("nested metadata = %#v", metadata["nested"])
	}
}

func TestNestedSecretDocumentsCannotBeQueriedAsAnOracle(t *testing.T) {
	t.Parallel()
	db, repository := newLegacyTestRepository(t)
	configs := mustDefinition(t, repository.registry, "system_configs")
	createFixtureTable(t, db, configs)
	insertFixture(t, db, `INSERT INTO "main"."system_configs" (id, tenant_id, name, metadata) VALUES (?, ?, ?, ?)`, "config-one", "legacy-one", "wechat", `{"appId":"public-id","appSecret":"unguessable-secret","nested":{"access_token":"private-token","region":"au"}}`)

	public, err := repository.List(context.Background(), "system_configs", Query{Search: "wechat"})
	if err != nil || public.Total != 1 {
		t.Fatalf("public name search page=%#v error=%v", public, err)
	}
	secret, err := repository.List(context.Background(), "system_configs", Query{
		Search: "unguessable-secret",
		Exact:  map[string]string{"name": "wechat"},
	})
	if err != nil || secret.Total != 0 {
		t.Fatalf("nested secret was searchable page=%#v error=%v", secret, err)
	}

	for operation, query := range map[string]Query{
		"exact":     {Exact: map[string]string{"metadata": `{"appSecret":"unguessable-secret"}`}},
		"contains":  {Contains: map[string]string{"metadata": "unguessable-secret"}},
		"icontains": {IContains: map[string]string{"metadata": "PRIVATE-TOKEN"}},
		"sort":      {SortBy: "metadata"},
	} {
		if _, queryErr := repository.List(context.Background(), "system_configs", query); !errors.Is(queryErr, ErrInvalidRequest) {
			t.Errorf("%s nested secret query error = %v", operation, queryErr)
		}
	}
}

func TestReadOnlyBoundaryLeavesEverySchemaUnchanged(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ATTACH DATABASE ? AS "mall_biz"`, filepath.Join(t.TempDir(), "mall-biz.db")).Error; err != nil {
		t.Fatal(err)
	}
	binding := fixedbinding.Binding{
		TenantID:       "control-one",
		AdminTenantID:  "default",
		LegacyTenantID: "legacy-one",
		BusinessSchema: "mall_biz",
	}
	repository, err := NewRepository(db, binding, DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	definition := mustDefinition(t, repository.registry, "show_categories")
	createFixtureTableInSchema(t, db, "main", definition)
	createFixtureTableInSchema(t, db, binding.BusinessSchema, definition)
	insertFixture(t, db, `INSERT INTO "mall_biz"."show_categories" (id, tenant_id, name) VALUES (?, ?, ?)`, "existing", binding.LegacyTenantID, "Existing")

	if _, _, err := repository.Create(context.Background(), definition.Resource.Name, map[string]any{"name": "Blocked"}); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("create error = %v", err)
	}
	if _, _, err := repository.Update(context.Background(), definition.Resource.Name, "existing", map[string]any{"name": "Blocked update"}); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("update error = %v", err)
	}
	if _, err := repository.Delete(context.Background(), definition.Resource.Name, "existing"); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("delete error = %v", err)
	}

	var mainRows, businessRows, unchangedRows int64
	if err := db.Raw(`SELECT COUNT(*) FROM "main"."show_categories"`).Scan(&mainRows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT COUNT(*) FROM "mall_biz"."show_categories"`).Scan(&businessRows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT COUNT(*) FROM "mall_biz"."show_categories" WHERE id = ? AND tenant_id = ? AND name = ? AND deleted_at IS NULL`, "existing", binding.LegacyTenantID, "Existing").Scan(&unchangedRows).Error; err != nil {
		t.Fatal(err)
	}
	if mainRows != 0 || businessRows != 1 || unchangedRows != 1 {
		t.Fatalf("read-only evidence main=%d business=%d unchanged=%d", mainRows, businessRows, unchangedRows)
	}
}

func TestComplexWritesAndIDLessCompositeDetailAreDenied(t *testing.T) {
	t.Parallel()
	_, repository := newLegacyTestRepository(t)

	if _, _, err := repository.Create(context.Background(), "orders", map[string]any{}); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("orders create error = %v", err)
	}
	if _, _, err := repository.Get(context.Background(), "coupon_links", "id"); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("composite detail error = %v", err)
	}
	if _, _, err := repository.Update(context.Background(), "payment_orders", "id", map[string]any{"token": "new"}); !errors.Is(err, ErrOperationNotSupported) {
		t.Fatalf("payment update error = %v", err)
	}
}

func TestReadinessChecksAllReviewedRelationsWithoutMigrating(t *testing.T) {
	t.Parallel()
	db, repository := newLegacyTestRepository(t)
	for _, definition := range repository.registry.All() {
		createFixtureTable(t, db, definition)
	}
	if err := VerifyReadiness(context.Background(), db, repository.binding, repository.registry); err != nil {
		t.Fatal(err)
	}

	missingDB, missingRepository := newLegacyTestRepository(t)
	for _, definition := range missingRepository.registry.All() {
		if definition.Resource.Name != "shipping_warehouses" {
			createFixtureTable(t, missingDB, definition)
		}
	}
	if err := VerifyReadiness(context.Background(), missingDB, missingRepository.binding, missingRepository.registry); !errors.Is(err, ErrSchemaNotReady) || !strings.Contains(err.Error(), "shipping_warehouses") {
		t.Fatalf("missing shipping_warehouses readiness error = %v", err)
	}
}

func newLegacyTestRepository(t *testing.T) (*gorm.DB, *Repository) {
	t.Helper()
	temporary := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(temporary, "legacy.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	binding := fixedbinding.Binding{
		TenantID:       "default",
		AdminTenantID:  "default",
		LegacyTenantID: "legacy-one",
		BusinessSchema: "main",
	}
	repository, err := NewRepository(db, binding, DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return db, repository
}

func mustDefinition(t *testing.T, registry Registry, name string) Definition {
	t.Helper()
	definition, ok := registry.Lookup(name)
	if !ok {
		t.Fatalf("missing definition %s", name)
	}
	return definition
}

func createFixtureTable(t *testing.T, db *gorm.DB, definition Definition) {
	createFixtureTableInSchema(t, db, "main", definition)
}

func createFixtureTableInSchema(t *testing.T, db *gorm.DB, schema string, definition Definition) {
	t.Helper()
	columns := make([]string, 0, len(definition.Resource.Columns)+1)
	for _, column := range definition.Resource.Columns {
		columns = append(columns, quoteIdentifier(column.Name)+" TEXT")
	}
	primary := make([]string, 0, len(definition.Resource.PrimaryKey))
	for _, column := range definition.Resource.PrimaryKey {
		primary = append(primary, quoteIdentifier(column))
	}
	columns = append(columns, "PRIMARY KEY ("+strings.Join(primary, ", ")+")")
	statement := "CREATE TABLE " + qualifiedTable(schema, definition.Resource.Name) + " (" + strings.Join(columns, ", ") + ")"
	if err := db.Exec(statement).Error; err != nil {
		t.Fatalf("create fixture %s: %v\n%s", definition.Resource.Name, err, statement)
	}
}

func insertFixture(t *testing.T, db *gorm.DB, statement string, arguments ...any) {
	t.Helper()
	if err := db.Exec(statement, arguments...).Error; err != nil {
		t.Fatalf("fixture insert: %v: %s (%v)", err, statement, arguments)
	}
}

func TestQualifiedIdentifiersAreNotRequestValues(t *testing.T) {
	t.Parallel()
	if got := qualifiedTable("mall_biz", "orders"); got != `"mall_biz"."orders"` {
		t.Fatalf("qualified table = %s", got)
	}
	if _, err := NewRepository(nil, fixedbinding.Binding{}, DefaultRegistry()); err == nil {
		t.Fatal("nil database was accepted")
	}
}

func TestEveryResourceHasUsableDefaultSort(t *testing.T) {
	t.Parallel()
	for _, definition := range DefaultRegistry().All() {
		normalized, err := normalizeQuery(definition, Query{})
		if err != nil {
			t.Errorf("%s default query: %v", definition.Resource.Name, err)
			continue
		}
		if normalized.SortBy == definition.TenantColumn {
			t.Errorf("%s default sort exposes fixed tenant column", definition.Resource.Name)
		}
	}
}
