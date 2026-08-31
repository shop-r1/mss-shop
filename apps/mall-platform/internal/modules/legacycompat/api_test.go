package legacycompat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLegacyHTTPContractAndCrossTenantIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE "main"."show_categories" (id TEXT PRIMARY KEY, created_at TEXT, updated_at TEXT, deleted_at TEXT, tenant_id TEXT, name TEXT, image TEXT, parent_id TEXT, status INTEGER, description TEXT, sort INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{{"local", "legacy-one", "Alpha", 1}, {"foreign", "legacy-two", "Alpha", 2}} {
		if err := db.Exec(`INSERT INTO "main"."show_categories" (id, tenant_id, name, sort) VALUES (?, ?, ?, ?)`, row...).Error; err != nil {
			t.Fatal(err)
		}
	}
	repository, err := legacydb.NewRepository(db, testBinding(), legacydb.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &recordingAuthorizer{}
	router := newLegacyTestRouter(t, staticRepositoryResolver{repository: repository}, authorizer)

	request := httptest.NewRequest(http.MethodGet, "/admin/api/legacy/resources/show_categories?page=1&pageSize=20&q=alpha&sortBy=sort&sortOrder=asc&exact%5Bname%5D=Alpha", nil)
	request.Header.Set("X-Tenant-ID", "legacy-two")
	request.Header.Set("X-Legacy-Schema", "foreign_schema")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Data     []map[string]any  `json:"data"`
		Total    int64             `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"pageSize"`
		Resource legacydb.Resource `json:"resource"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 1 || len(payload.Data) != 1 || payload.Data[0]["id"] != "local" {
		t.Fatalf("fixed tenant payload = %#v", payload)
	}
	if payload.Page != 1 || payload.PageSize != 20 || payload.Resource.Name != "show_categories" || payload.Resource.TitleKey != "legacy.resources.show_categories" {
		t.Fatalf("contract metadata = %#v", payload)
	}
	if len(authorizer.calls) != 1 || authorizer.calls[0] != "show_categories/list" {
		t.Fatalf("authorization calls = %#v", authorizer.calls)
	}
}

func TestLegacyHTTPRejectsEveryMutationUntilDomainWorkflowsExist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "crud.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE "main"."show_categories" (id TEXT PRIMARY KEY, created_at TEXT, updated_at TEXT, deleted_at TEXT, tenant_id TEXT, name TEXT, image TEXT, parent_id TEXT, status INTEGER, description TEXT, sort INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	repository, err := legacydb.NewRepository(db, testBinding(), legacydb.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &recordingAuthorizer{}
	router := newLegacyTestRouter(t, staticRepositoryResolver{repository: repository}, authorizer)

	create := httptest.NewRequest(http.MethodPost, "/admin/api/legacy/resources/show_categories", bytes.NewBufferString(`{"name":"Created","status":1}`))
	create.Header.Set("Content-Type", "application/json")
	createdResponse := httptest.NewRecorder()
	router.ServeHTTP(createdResponse, create)
	assertErrorContract(t, createdResponse, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED")

	update := httptest.NewRequest(http.MethodPut, "/admin/api/legacy/resources/show_categories/missing", bytes.NewBufferString(`{"name":"Updated"}`))
	updatedResponse := httptest.NewRecorder()
	router.ServeHTTP(updatedResponse, update)
	assertErrorContract(t, updatedResponse, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED")
	remove := httptest.NewRequest(http.MethodDelete, "/admin/api/legacy/resources/show_categories/missing", nil)
	removedResponse := httptest.NewRecorder()
	router.ServeHTTP(removedResponse, remove)
	assertErrorContract(t, removedResponse, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED")

	complex := httptest.NewRequest(http.MethodPost, "/admin/api/legacy/resources/orders", bytes.NewBufferString(`{}`))
	complexResponse := httptest.NewRecorder()
	router.ServeHTTP(complexResponse, complex)
	assertErrorContract(t, complexResponse, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED")
	unknown := httptest.NewRequest(http.MethodGet, "/admin/api/legacy/resources/courier_links", nil)
	unknownResponse := httptest.NewRecorder()
	router.ServeHTTP(unknownResponse, unknown)
	assertErrorContract(t, unknownResponse, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	composite := httptest.NewRequest(http.MethodGet, "/admin/api/legacy/resources/coupon_links/id", nil)
	compositeResponse := httptest.NewRecorder()
	router.ServeHTTP(compositeResponse, composite)
	assertErrorContract(t, compositeResponse, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED")

	wrongType := httptest.NewRequest(http.MethodPost, "/admin/api/legacy/resources/show_categories", bytes.NewBufferString(`{"name":"Typed","status":"enabled"}`))
	wrongTypeResponse := httptest.NewRecorder()
	router.ServeHTTP(wrongTypeResponse, wrongType)
	assertErrorContract(t, wrongTypeResponse, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED")
}

func TestLegacyHTTPUniformErrorsAndAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := legacydb.DefaultRegistry()

	for _, test := range []struct {
		name        string
		authErr     error
		resolverErr error
		path        string
		wantStatus  int
		wantCode    string
	}{
		{name: "authentication", authErr: ErrAuthenticationRequired, path: "/admin/api/legacy/resources/show_categories", wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHENTICATED"},
		{name: "authorization", authErr: ErrAuthorizationDenied, path: "/admin/api/legacy/resources/show_categories", wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN"},
		{name: "schema", resolverErr: legacydb.ErrSchemaNotReady, path: "/admin/api/legacy/resources/show_categories", wantStatus: http.StatusServiceUnavailable, wantCode: "LEGACY_SCHEMA_NOT_READY"},
		{name: "bad query", path: "/admin/api/legacy/resources/show_categories?page=bad", wantStatus: http.StatusBadRequest, wantCode: "INVALID_REQUEST"},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			group := router.Group("/admin/api")
			if err := RegisterRoutes(group, errorRepositoryResolver{err: test.resolverErr}, &recordingAuthorizer{err: test.authErr}, registry); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			assertErrorContract(t, response, test.wantStatus, test.wantCode)
		})
	}
}

func newLegacyTestRouter(t *testing.T, resolver RepositoryResolver, authorizer Authorizer) *gin.Engine {
	t.Helper()
	router := gin.New()
	if err := RegisterRoutes(router.Group("/admin/api"), resolver, authorizer, legacydb.DefaultRegistry()); err != nil {
		t.Fatal(err)
	}
	return router
}

type staticRepositoryResolver struct{ repository LegacyRepository }

func (resolver staticRepositoryResolver) Resolve(context.Context) (LegacyRepository, error) {
	return resolver.repository, nil
}

type errorRepositoryResolver struct{ err error }

func (resolver errorRepositoryResolver) Resolve(context.Context) (LegacyRepository, error) {
	if resolver.err != nil {
		return nil, resolver.err
	}
	return nil, legacydb.ErrSchemaNotReady
}

type recordingAuthorizer struct {
	err   error
	calls []string
}

func (authorizer *recordingAuthorizer) Authorize(_ *gin.Context, resource string, operation Operation) error {
	authorizer.calls = append(authorizer.calls, resource+"/"+string(operation))
	return authorizer.err
}

func assertErrorContract(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d, body=%s", response.Code, status, response.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ErrorCode != code || envelope.ErrorMessage == "" || envelope.MessageKey == "" {
		t.Fatalf("error envelope = %#v", envelope)
	}
	var raw map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, hasObjectError := raw["error"]; hasObjectError {
		t.Fatalf("MSS-incompatible top-level error object: %#v", raw)
	}
}
