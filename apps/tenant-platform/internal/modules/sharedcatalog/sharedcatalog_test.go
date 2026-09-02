package sharedcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/security"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestModuleDescriptorPermissionsAndMigrationRegistration(t *testing.T) {
	t.Parallel()

	binding := fixedbinding.Binding{CoreSchema: "core", SharedSchema: "shared"}
	core := migration.New()
	registry, err := business.Compose(core, NewModule(fixedbinding.NewResolver(fixedbinding.StaticSource(binding))))
	if err != nil {
		t.Fatalf("Compose(): %v", err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 1 || descriptors[0].Name != ModuleName || len(descriptors[0].Permissions) != 1 {
		t.Fatalf("descriptor = %#v", descriptors)
	}
	for _, permission := range descriptors[0].Permissions {
		if !strings.HasPrefix(permission.Code, "/legacy/resources/") || len(permission.DefaultRoles) != 1 || permission.DefaultRoles[0] != "admin" {
			t.Errorf("permission = %#v", permission)
		}
	}
	if descriptors[0].Migrate != nil {
		t.Fatal("descriptor exposed an inferred migration hook")
	}
	phases, err := registry.MigrationPhaseRunners()
	if err != nil {
		t.Fatal(err)
	}
	if err := phases.Business.Register(AuthorizationMigrationID, func(*gorm.DB, string) error { return nil }); !errors.Is(err, migration.ErrDuplicateMigrationID) {
		t.Fatalf("migration was not registered: %v", err)
	}
	if err := phases.Business.Register(MenuLocalizationMigrationID, func(*gorm.DB, string) error { return nil }); !errors.Is(err, migration.ErrDuplicateMigrationID) {
		t.Fatalf("menu localization migration was not registered: %v", err)
	}
	if err := phases.Business.Register(CapabilityLockdownMigrationID, func(*gorm.DB, string) error { return nil }); !errors.Is(err, migration.ErrDuplicateMigrationID) {
		t.Fatalf("capability lockdown migration was not registered: %v", err)
	}
	if err := phases.Business.Register(OwnershipTransferMigrationID, func(*gorm.DB, string) error { return nil }); !errors.Is(err, migration.ErrDuplicateMigrationID) {
		t.Fatalf("ownership transfer migration was not registered: %v", err)
	}
}

func TestAuthorizationMigrationIsIdempotentQualifiedAndComplete(t *testing.T) {
	t.Parallel()

	db, binding := openSharedCatalogTestDatabase(t)
	published := legacydb.PublishedRegistry()
	registry := legacydb.DefaultRegistry()
	for attempt := 1; attempt <= 2; attempt++ {
		if err := applyAuthorizationMigration(db, binding, published, AuthorizationMigrationID.String(), nil); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt, err)
		}
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := applyMenuLocalizationMigration(db, binding, MenuLocalizationMigrationID.String()); err != nil {
			t.Fatalf("menu localization migration attempt %d: %v", attempt, err)
		}
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := applyCapabilityLockdownMigration(db, binding, published, CapabilityLockdownMigrationID.String(), nil); err != nil {
			t.Fatalf("capability lockdown migration attempt %d: %v", attempt, err)
		}
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := applyOwnershipTransferMigration(db, binding, published, registry, OwnershipTransferMigrationID.String(), nil); err != nil {
			t.Fatalf("ownership transfer migration attempt %d: %v", attempt, err)
		}
	}

	assertCoreCount(t, db, binding, "mss_boot_migration", "version IN ?", []any{[]string{
		AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(), CapabilityLockdownMigrationID.String(), OwnershipTransferMigrationID.String(),
	}}, 4)
	assertCoreCount(t, db, binding, "mss_boot_roles", "name = ?", []any{"admin"}, 1)
	assertCoreCount(t, db, binding, "mss_boot_menus", "1 = 1", nil, 42)
	assertCoreCount(t, db, binding, "mss_boot_menus", "deleted_at IS NULL AND status = ?", []any{"enabled"}, 6)
	assertCoreCount(t, db, binding, "mss_boot_casbin_rule", "ptype = ?", []any{"p"}, 6)
	assertCoreCount(t, db, binding, "mss_boot_config_revisions", "resource = ?", []any{authorizationResource}, 2)
	assertCoreCount(t, db, binding, "mss_boot_config_revisions", "resource = ? AND revision = ?", []any{authorizationResource, 4}, 2)
	assertCoreCount(t, db, binding, "mss_boot_menus", "type = ? AND path = ? AND name = ? AND permission = ?", []any{
		adminpkg.DirectoryAccessType, sharedCatalogRootPath, sharedCatalogMenuNameToken, PermissionCode("payments", "read"),
	}, 1)

	for _, definition := range registry.All() {
		resource := definition.Resource
		assertCoreCount(t, db, binding, "mss_boot_menus", "type = ? AND path = ? AND permission = ?", []any{
			adminpkg.MenuAccessType, menuPath(resource.Name), PermissionCode(resource.Name, string(actionRead)),
		}, 1)
		for _, operation := range []string{"list", "read"} {
			assertCoreCount(t, db, binding, "mss_boot_menus", "type = ? AND path = ?", []any{
				adminpkg.ComponentAccessType, componentPath(resource.Name, operation),
			}, 1)
		}
	}
	for _, target := range capabilityLockdownTargets(registry) {
		assertCoreCount(t, db, binding, "mss_boot_menus", "type = ? AND path = ? AND method = ? AND deleted_at IS NULL AND status = ?", []any{
			target.accessType, target.path, target.method, "enabled",
		}, 0)
		assertCoreCount(t, db, binding, "mss_boot_casbin_rule", "ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?", []any{
			"p", target.accessType.String(), target.path, target.method,
		}, 0)
	}
	if err := verifyAuthorizationReadiness(context.Background(), db, binding, registry); err != nil {
		t.Fatalf("authorization readiness: %v", err)
	}
	if err := db.Table(qualifiedCoreTable(binding, "mss_boot_menus")).Where(
		"type = ? AND path = ?", adminpkg.ComponentAccessType, componentPath("payments", "read"),
	).Update("deleted_at", gorm.Expr("CURRENT_TIMESTAMP")).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyAuthorizationReadiness(context.Background(), db, binding, registry); err == nil {
		t.Fatal("authorization readiness accepted a missing active component menu")
	}

	var mainMenus int64
	if err := db.Raw(`SELECT COUNT(*) FROM main.sqlite_master WHERE type = 'table' AND name = 'mss_boot_menus'`).Scan(&mainMenus).Error; err != nil || mainMenus != 0 {
		t.Fatalf("main-schema menu table unexpectedly available: count=%d err=%v", mainMenus, err)
	}
}

func TestAuthorizationMigrationRollsBackAndNeverMigratesLegacyTables(t *testing.T) {
	t.Parallel()

	db, binding := openSharedCatalogTestDatabase(t)
	injected := errors.New("injected")
	err := applyAuthorizationMigration(db, binding, legacydb.PublishedRegistry(), AuthorizationMigrationID.String(), func() error { return injected })
	if !errors.Is(err, injected) {
		t.Fatalf("migration error = %v", err)
	}
	for _, table := range []string{"mss_boot_migration", "mss_boot_roles", "mss_boot_menus", "mss_boot_casbin_rule", "mss_boot_config_revisions"} {
		assertCoreCount(t, db, binding, table, "1 = 1", nil, 0)
	}
	for _, definition := range legacydb.PublishedRegistry().All() {
		var rows []struct{ Name string }
		query := `SELECT name FROM "shared".sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.Raw(query, definition.Resource.Name).Scan(&rows).Error; err != nil || len(rows) != 0 {
			t.Fatalf("legacy table %s was migrated: rows=%v err=%v", definition.Resource.Name, rows, err)
		}
	}
}

func TestHTTPContractAuthorizationAndForgedSchemaRejection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, binding := openSharedCatalogTestDatabase(t)
	registry := legacydb.DefaultRegistry()
	createSharedRelations(t, db, binding.SharedSchema, registry)
	if err := db.Exec(`INSERT INTO "shared"."payments" (id, name, method, status, created_at, updated_at) VALUES ('178819869911563900', '微信在线', 'wechat_online', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE payments (id TEXT PRIMARY KEY, name TEXT, method TEXT, status NUMERIC)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO payments VALUES ('178819869911563901', '伪造', 'forged', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	applyCurrentSharedCatalogMigrations(t, db, binding, registry)
	adminRole := loadCoreRole(t, db, binding, "admin")
	principals := map[string]security.Verifier{
		"admin":  &testVerifier{roleID: adminRole.ID, tenantID: "attacker-controlled-scope"},
		"denied": &testVerifier{roleID: "role-without-policy", tenantID: "default"},
		"root":   &testVerifier{roleID: "root", root: true, tenantID: "unexpected"},
	}
	var typedNil *testVerifier
	principals["typed-nil"] = typedNil

	router := gin.New()
	runtime := business.Runtime{
		RequestDatabase: func(context.Context) (*gorm.DB, bool) { return db, true },
		Principal: func(ctx *gin.Context) security.Verifier {
			return principals[ctx.GetHeader("X-Test-Role")]
		},
	}
	if err := RegisterRoutes(router.Group("/admin/api"), runtime, binding, registry); err != nil {
		t.Fatal(err)
	}

	response := performRequest(router, http.MethodGet, "/admin/api/legacy/resources/payments?page=1&pageSize=20", nil, map[string]string{
		"X-Test-Role": "admin", "X-Schema": "main",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Data     []map[string]any  `json:"data"`
		Total    int               `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"pageSize"`
		Resource legacydb.Resource `json:"resource"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Data) != 1 || page.Data[0]["method"] != "wechat_online" || page.Page != 1 || page.PageSize != 20 {
		t.Fatalf("list contract = %#v", page)
	}
	if page.Resource.Name != "payments" || page.Resource.Domain != "shared-catalog" ||
		page.Resource.Capabilities != (legacydb.Capabilities{Detail: true}) {
		t.Fatalf("resource descriptor = %#v", page.Resource)
	}
	for _, column := range page.Resource.Columns {
		if column.Writable {
			t.Fatalf("read-only descriptor exposed writable column %s", column.Name)
		}
	}
	response = performRequest(router, http.MethodGet, "/admin/api/legacy/resources/payments?sortBy=method&sortOrder=desc&exact%5Bstatus%5D=1&icontains%5Bmethod%5D=wechat", nil, map[string]string{"X-Test-Role": "admin"})
	if response.Code != http.StatusOK {
		t.Fatalf("filtered list status=%d body=%s", response.Code, response.Body.String())
	}

	for role, want := range map[string]struct {
		status int
		code   string
	}{
		"":          {status: http.StatusUnauthorized, code: "UNAUTHENTICATED"},
		"typed-nil": {status: http.StatusUnauthorized, code: "UNAUTHENTICATED"},
		"denied":    {status: http.StatusForbidden, code: "FORBIDDEN"},
		"root":      {status: http.StatusOK},
	} {
		response := performRequest(router, http.MethodGet, "/admin/api/legacy/resources/payments", nil, map[string]string{"X-Test-Role": role})
		if response.Code != want.status {
			t.Errorf("role %q status=%d want=%d body=%s", role, response.Code, want.status, response.Body.String())
			continue
		}
		if want.code != "" {
			assertErrorCode(t, response, want.status, want.code)
		}
	}

	response = performRequest(router, http.MethodGet, "/admin/api/legacy/resources/payments?schema=main", nil, map[string]string{"X-Test-Role": "admin"})
	assertErrorCode(t, response, http.StatusBadRequest, "INVALID_REQUEST")
	response = performRequest(router, http.MethodGet, "/admin/api/legacy/resources/payments?exact%5Bschema%5D=main", nil, map[string]string{"X-Test-Role": "admin"})
	assertErrorCode(t, response, http.StatusBadRequest, "INVALID_REQUEST")
	response = performRequest(router, http.MethodPost, "/admin/api/legacy/resources/payments", []byte(`{"name":"x","method":"y","status":1,"schema":"main"}`), map[string]string{"X-Test-Role": "admin"})
	assertErrorCode(t, response, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED")
	for _, definition := range registry.All() {
		resource := definition.Resource.Name
		for _, request := range []struct {
			method string
			path   string
		}{
			{method: http.MethodPost, path: "/admin/api/legacy/resources/" + resource},
			{method: http.MethodPut, path: "/admin/api/legacy/resources/" + resource + "/178819869911563900"},
			{method: http.MethodDelete, path: "/admin/api/legacy/resources/" + resource + "/178819869911563900"},
		} {
			response = performRequest(router, request.method, request.path, []byte(`{"name":"blocked"}`), map[string]string{"X-Test-Role": "admin"})
			assertErrorCode(t, response, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED")
		}
	}
	response = performRequest(router, http.MethodGet, "/admin/api/legacy/resources/brands", nil, map[string]string{"X-Test-Role": "admin"})
	assertErrorCode(t, response, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestAuthorizerBindsPermissionToResourceAndRouteNotPrincipalTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, binding := openSharedCatalogTestDatabase(t)
	registry := legacydb.DefaultRegistry()
	applyCurrentSharedCatalogMigrations(t, db, binding, registry)
	role := loadCoreRole(t, db, binding, "admin")
	principal := &testVerifier{roleID: role.ID, tenantID: "not-default-and-not-a-schema"}
	authorizer, err := NewAdminAuthorizer(db, binding, registry, func(*gin.Context) security.Verifier { return principal })
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/admin/api/legacy/resources/:resource", func(ctx *gin.Context) {
		if err := authorizer.Authorize(ctx, PermissionCode("payments", "read")); err != nil {
			ctx.String(http.StatusForbidden, err.Error())
			return
		}
		ctx.Status(http.StatusNoContent)
	})
	for resource, want := range map[string]int{"payments": http.StatusNoContent, "brands": http.StatusForbidden} {
		response := performRequest(router, http.MethodGet, "/admin/api/legacy/resources/"+resource, nil, nil)
		if response.Code != want {
			t.Errorf("resource %s status=%d want=%d body=%s", resource, response.Code, want, response.Body.String())
		}
	}
}

func TestHTTPErrorEnvelopeIsTopLevelAndRedactsInternalDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	persistenceResponse := httptest.NewRecorder()
	persistenceContext, _ := gin.CreateTestContext(persistenceResponse)
	writeError(persistenceContext, fmt.Errorf("%w: password=hunter2", legacydb.ErrPersistence))
	assertErrorCode(t, persistenceResponse, http.StatusServiceUnavailable, "LEGACY_SCHEMA_NOT_READY")
	if strings.Contains(strings.ToLower(persistenceResponse.Body.String()), "hunter2") {
		t.Fatalf("persistence error leaked internal details: %s", persistenceResponse.Body.String())
	}

	authorizationResponse := httptest.NewRecorder()
	authorizationContext, _ := gin.CreateTestContext(authorizationResponse)
	writeAuthorizationError(authorizationContext, ErrAuthorizationUnavailable)
	assertErrorCode(t, authorizationResponse, http.StatusServiceUnavailable, "AUTHORIZATION_UNAVAILABLE")
}

type testVerifier struct {
	roleID      string
	tenantID    string
	root        bool
	refreshOff  bool
	personToken string
}

func (*testVerifier) GetUserID() string                          { return "user" }
func (verifier *testVerifier) GetTenantID() string               { return verifier.tenantID }
func (verifier *testVerifier) GetRoleID() string                 { return verifier.roleID }
func (*testVerifier) GetEmail() string                           { return "" }
func (*testVerifier) GetUsername() string                        { return "test" }
func (verifier *testVerifier) GetRefreshTokenDisable() bool      { return verifier.refreshOff }
func (verifier *testVerifier) SetRefreshTokenDisable(value bool) { verifier.refreshOff = value }
func (*testVerifier) CheckToken(context.Context, string) error   { return nil }
func (verifier *testVerifier) Root() bool                        { return verifier.root }
func (verifier *testVerifier) Verify(context.Context) (bool, security.Verifier, error) {
	return true, verifier, nil
}
func (verifier *testVerifier) GetPersonAccessToken() string      { return verifier.personToken }
func (verifier *testVerifier) SetPersonAccessToken(value string) { verifier.personToken = value }

func openSharedCatalogTestDatabase(t *testing.T) (*gorm.DB, fixedbinding.Binding) {
	t.Helper()
	directory := t.TempDir()
	corePath := filepath.Join(directory, "core.db")
	coreDB, err := gorm.Open(sqlite.Open(corePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{
		&models.Role{}, &models.Menu{}, &models.CasbinRule{}, &models.ConfigRevision{}, &migrationmodels.Migration{},
	} {
		if err := coreDB.AutoMigrate(model); err != nil {
			t.Fatalf("migrate core model %T: %v", model, err)
		}
	}
	coreSQL, err := coreDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := coreSQL.Close(); err != nil {
		t.Fatal(err)
	}

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
	for schema, path := range map[string]string{"core": corePath, "shared": filepath.Join(directory, "shared.db")} {
		if err := db.Exec(`ATTACH DATABASE ? AS "`+schema+`"`, path).Error; err != nil {
			t.Fatal(err)
		}
	}
	binding := fixedbinding.Binding{CoreSchema: "core", SharedSchema: "shared"}
	return db, binding
}

func createSharedRelations(t *testing.T, db *gorm.DB, schema string, registry legacydb.Registry) {
	t.Helper()
	for _, definition := range registry.All() {
		columns := make([]string, 0, len(definition.Resource.Columns))
		for _, column := range definition.Resource.Columns {
			columnType := "TEXT"
			switch column.Type {
			case legacydb.ColumnNumber:
				columnType = "NUMERIC"
			case legacydb.ColumnBoolean:
				columnType = "INTEGER"
			}
			declaration := `"` + column.Name + `" ` + columnType
			if column.Name == "id" {
				declaration += " PRIMARY KEY"
			}
			columns = append(columns, declaration)
		}
		statement := fmt.Sprintf(`CREATE TABLE "%s"."%s" (%s)`, schema, definition.Resource.Name, strings.Join(columns, ", "))
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create %s: %v", definition.Resource.Name, err)
		}
	}
}

func applyCurrentSharedCatalogMigrations(
	t *testing.T,
	db *gorm.DB,
	binding fixedbinding.Binding,
	registry legacydb.Registry,
) {
	t.Helper()
	published := legacydb.PublishedRegistry()
	if err := applyAuthorizationMigration(db, binding, published, AuthorizationMigrationID.String(), nil); err != nil {
		t.Fatal(err)
	}
	if err := applyMenuLocalizationMigration(db, binding, MenuLocalizationMigrationID.String()); err != nil {
		t.Fatal(err)
	}
	if err := applyCapabilityLockdownMigration(db, binding, published, CapabilityLockdownMigrationID.String(), nil); err != nil {
		t.Fatal(err)
	}
	if err := applyOwnershipTransferMigration(db, binding, published, registry, OwnershipTransferMigrationID.String(), nil); err != nil {
		t.Fatal(err)
	}
}

func loadCoreRole(t *testing.T, db *gorm.DB, binding fixedbinding.Binding, name string) models.Role {
	t.Helper()
	var role models.Role
	if err := db.Table(qualifiedCoreTable(binding, role.TableName())).Where("name = ?", name).Take(&role).Error; err != nil {
		t.Fatal(err)
	}
	return role
}

func assertCoreCount(t *testing.T, db *gorm.DB, binding fixedbinding.Binding, table, where string, args []any, want int64) {
	t.Helper()
	var count int64
	query := db.Table(qualifiedCoreTable(binding, table)).Where(where, args...)
	if err := query.Count(&count).Error; err != nil || count != want {
		t.Fatalf("count %s = %d, want %d, err=%v", table, count, want, err)
	}
}

func performRequest(router http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.ErrorCode != code || envelope.ErrorMessage == "" {
		t.Fatalf("error envelope=%#v err=%v body=%s", envelope, err, response.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, nested := raw["error"]; nested {
		t.Fatalf("error envelope retained unsupported nested error object: %s", response.Body.String())
	}
}
