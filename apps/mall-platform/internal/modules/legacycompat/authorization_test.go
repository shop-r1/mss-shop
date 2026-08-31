package legacycompat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/security"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testPrincipalKey = "legacy.test.principal"

func TestAdminAuthorizerEnforcesOperationPolicyAndFixedTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openAuthorizationTestDatabase(t)
	for _, rule := range []models.CasbinRule{
		{PType: "p", V0: "role-allowed", V1: adminpkg.ComponentAccessType.String(), V2: ComponentPath("show_categories", OperationList), V3: "GET"},
		{PType: "p", V0: "role-allowed", V1: adminpkg.APIAccessType.String(), V2: collectionRoutePath, V3: "GET"},
		{PType: "p", V0: "role-component-only", V1: adminpkg.ComponentAccessType.String(), V2: ComponentPath("show_categories", OperationList), V3: "GET"},
		{PType: "p", V0: "role-api-only", V1: adminpkg.APIAccessType.String(), V2: collectionRoutePath, V3: "GET"},
	} {
		if err := db.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
	}
	binding := testBinding()
	resolver := func(ctx *gin.Context) security.Verifier {
		value, _ := ctx.Get(testPrincipalKey)
		principal, _ := value.(security.Verifier)
		return principal
	}
	authorizer, err := NewAdminAuthorizer(
		func(context.Context) (*gorm.DB, bool) { return db, true }, resolver, binding, legacydb.DefaultRegistry(),
	)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		switch ctx.GetHeader("X-Test-Principal") {
		case "allowed":
			ctx.Set(testPrincipalKey, &testVerifier{roleID: "role-allowed", tenantID: "default"})
		case "denied":
			ctx.Set(testPrincipalKey, &testVerifier{roleID: "role-denied", tenantID: "default"})
		case "component-only":
			ctx.Set(testPrincipalKey, &testVerifier{roleID: "role-component-only", tenantID: "default"})
		case "api-only":
			ctx.Set(testPrincipalKey, &testVerifier{roleID: "role-api-only", tenantID: "default"})
		case "foreign":
			ctx.Set(testPrincipalKey, &testVerifier{roleID: "role-allowed", tenantID: "another-tenant"})
		case "root":
			ctx.Set(testPrincipalKey, &testVerifier{roleID: "root", tenantID: "default", root: true})
		case "foreign-root":
			ctx.Set(testPrincipalKey, &testVerifier{roleID: "root", tenantID: "another-tenant", root: true})
		}
		ctx.Next()
	})
	router.GET(collectionRoutePath, func(ctx *gin.Context) {
		err := authorizer.Authorize(ctx, ctx.Param("resource"), OperationList)
		switch {
		case err == nil:
			ctx.Status(http.StatusNoContent)
		case errors.Is(err, ErrAuthenticationRequired):
			ctx.Status(http.StatusUnauthorized)
		default:
			ctx.Status(http.StatusForbidden)
		}
	})
	router.POST(collectionRoutePath, func(ctx *gin.Context) {
		if err := authorizer.Authorize(ctx, ctx.Param("resource"), OperationCreate); err == nil {
			ctx.Status(http.StatusNoContent)
			return
		}
		ctx.Status(http.StatusForbidden)
	})

	for _, test := range []struct {
		principal string
		want      int
	}{
		{principal: "allowed", want: http.StatusNoContent},
		{principal: "denied", want: http.StatusForbidden},
		{principal: "component-only", want: http.StatusForbidden},
		{principal: "api-only", want: http.StatusForbidden},
		{principal: "foreign", want: http.StatusForbidden},
		{principal: "root", want: http.StatusNoContent},
		{principal: "foreign-root", want: http.StatusForbidden},
		{principal: "", want: http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(http.MethodGet, "/admin/api/legacy/resources/show_categories", nil)
		request.Header.Set("X-Test-Principal", test.principal)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("principal %q status = %d, want %d", test.principal, response.Code, test.want)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/api/legacy/resources/show_categories", nil)
	request.Header.Set("X-Test-Principal", "root")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("root mutation status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func openAuthorizationTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "authorization.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().CreateTable(new(models.CasbinRule)); err != nil {
		t.Fatal(err)
	}
	return db
}

type testVerifier struct {
	roleID   string
	tenantID string
	root     bool
	disabled bool
	token    string
}

func (verifier *testVerifier) GetUserID() string                        { return "user" }
func (verifier *testVerifier) GetTenantID() string                      { return verifier.tenantID }
func (verifier *testVerifier) GetRoleID() string                        { return verifier.roleID }
func (verifier *testVerifier) GetEmail() string                         { return "" }
func (verifier *testVerifier) GetUsername() string                      { return "user" }
func (verifier *testVerifier) GetRefreshTokenDisable() bool             { return verifier.disabled }
func (verifier *testVerifier) SetRefreshTokenDisable(value bool)        { verifier.disabled = value }
func (verifier *testVerifier) CheckToken(context.Context, string) error { return nil }
func (verifier *testVerifier) Root() bool                               { return verifier.root }
func (verifier *testVerifier) Verify(context.Context) (bool, security.Verifier, error) {
	return true, verifier, nil
}
func (verifier *testVerifier) GetPersonAccessToken() string      { return verifier.token }
func (verifier *testVerifier) SetPersonAccessToken(value string) { verifier.token = value }

func testBinding() fixedbinding.Binding {
	return fixedbinding.Binding{
		TenantID: "control-plane-tenant", AdminTenantID: "default", LegacyTenantID: "legacy-one", BusinessSchema: "main", SharedSchema: "shared",
	}
}
