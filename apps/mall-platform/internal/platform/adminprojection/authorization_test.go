package adminprojection

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/security"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
)

const projectionPrincipalKey = "adminprojection.test.principal"

func TestAuthorizerEnforcesFixedRouteTenantAndBothExactPoliciesBeforeRootBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := openProjectionTestDatabase(t)
	for _, rule := range []models.CasbinRule{
		{PType: "p", V0: "allowed", V1: adminpkg.ComponentAccessType.String(), V2: projectionTestComponent, V3: "GET"},
		{PType: "p", V0: "allowed", V1: adminpkg.APIAccessType.String(), V2: projectionTestRoute, V3: "GET"},
		{PType: "p", V0: "component-only", V1: adminpkg.ComponentAccessType.String(), V2: projectionTestComponent, V3: "GET"},
		{PType: "p", V0: "api-only", V1: adminpkg.APIAccessType.String(), V2: projectionTestRoute, V3: "GET"},
		{PType: "p", V0: "duplicate", V1: adminpkg.ComponentAccessType.String(), V2: projectionTestComponent, V3: "GET"},
		{PType: "p", V0: "duplicate", V1: adminpkg.ComponentAccessType.String(), V2: projectionTestComponent, V3: "GET"},
		{PType: "p", V0: "duplicate", V1: adminpkg.APIAccessType.String(), V2: projectionTestRoute, V3: "GET"},
	} {
		if err := database.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
	}

	databaseCalls := 0
	resolver := func(ctx *gin.Context) security.Verifier {
		value, _ := ctx.Get(projectionPrincipalKey)
		principal, _ := value.(security.Verifier)
		return principal
	}
	authorizer, err := NewAuthorizer(
		func(context.Context) (*gorm.DB, bool) {
			databaseCalls++
			return database, true
		},
		resolver,
		fixedbinding.Binding{
			TenantID: "tenant-aussibuy-dev", AdminTenantID: fixedbinding.MSS137AdminTenantID,
			LegacyTenantID: "518729051064631297", BusinessSchema: "mss_m_aussibuy_biz",
		},
		projectionTestContract(),
	)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		switch ctx.GetHeader("X-Test-Principal") {
		case "allowed", "component-only", "api-only", "duplicate", "denied":
			ctx.Set(projectionPrincipalKey, &projectionTestVerifier{
				roleID: ctx.GetHeader("X-Test-Principal"), tenantID: fixedbinding.MSS137AdminTenantID,
			})
		case "root":
			ctx.Set(projectionPrincipalKey, &projectionTestVerifier{roleID: "root", tenantID: fixedbinding.MSS137AdminTenantID, root: true})
		case "foreign-root":
			ctx.Set(projectionPrincipalKey, &projectionTestVerifier{roleID: "root", tenantID: "foreign", root: true})
		case "foreign":
			ctx.Set(projectionPrincipalKey, &projectionTestVerifier{roleID: "allowed", tenantID: "foreign"})
		case "empty-role":
			ctx.Set(projectionPrincipalKey, &projectionTestVerifier{tenantID: fixedbinding.MSS137AdminTenantID})
		case "typed-nil":
			ctx.Set(projectionPrincipalKey, (*projectionTestVerifier)(nil))
		}
		ctx.Next()
	})
	handler := func(permission string) gin.HandlerFunc {
		return func(ctx *gin.Context) {
			err := authorizer.Authorize(ctx, permission)
			switch {
			case err == nil:
				ctx.Status(http.StatusNoContent)
			case errors.Is(err, ErrAuthenticationRequired):
				ctx.Status(http.StatusUnauthorized)
			case errors.Is(err, ErrAuthorizationDenied):
				ctx.Status(http.StatusForbidden)
			default:
				ctx.Status(http.StatusServiceUnavailable)
			}
		}
	}
	router.GET(projectionTestRoute, handler(projectionTestPermission))
	router.POST(projectionTestRoute, handler(projectionTestPermission))
	router.GET("/admin/api/other", handler(projectionTestPermission))
	router.GET("/admin/api/unknown", handler("unknown:permission"))

	for _, test := range []struct {
		name        string
		principal   string
		method      string
		path        string
		wantStatus  int
		wantDBCalls int
	}{
		{name: "allowed", principal: "allowed", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusNoContent, wantDBCalls: 1},
		{name: "denied", principal: "denied", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusForbidden, wantDBCalls: 1},
		{name: "component only", principal: "component-only", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusForbidden, wantDBCalls: 1},
		{name: "api only", principal: "api-only", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusForbidden, wantDBCalls: 1},
		{name: "duplicate exact rule", principal: "duplicate", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusForbidden, wantDBCalls: 1},
		{name: "root", principal: "root", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusNoContent, wantDBCalls: 0},
		{name: "foreign root", principal: "foreign-root", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusForbidden, wantDBCalls: 0},
		{name: "foreign tenant", principal: "foreign", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusForbidden, wantDBCalls: 0},
		{name: "missing principal", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusUnauthorized, wantDBCalls: 0},
		{name: "typed nil principal", principal: "typed-nil", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusUnauthorized, wantDBCalls: 0},
		{name: "empty role", principal: "empty-role", method: http.MethodGet, path: projectionTestRoute, wantStatus: http.StatusUnauthorized, wantDBCalls: 0},
		{name: "forged method even for root", principal: "root", method: http.MethodPost, path: projectionTestRoute, wantStatus: http.StatusForbidden, wantDBCalls: 0},
		{name: "forged route even for root", principal: "root", method: http.MethodGet, path: "/admin/api/other", wantStatus: http.StatusForbidden, wantDBCalls: 0},
		{name: "unknown permission even for root", principal: "root", method: http.MethodGet, path: "/admin/api/unknown", wantStatus: http.StatusForbidden, wantDBCalls: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := databaseCalls
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("X-Test-Principal", test.principal)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if calls := databaseCalls - before; calls != test.wantDBCalls {
				t.Fatalf("database resolutions = %d, want %d", calls, test.wantDBCalls)
			}
		})
	}
}

type projectionTestVerifier struct {
	roleID   string
	tenantID string
	root     bool
	disabled bool
	token    string
}

func (*projectionTestVerifier) GetUserID() string                          { return "user" }
func (verifier *projectionTestVerifier) GetTenantID() string               { return verifier.tenantID }
func (verifier *projectionTestVerifier) GetRoleID() string                 { return verifier.roleID }
func (*projectionTestVerifier) GetEmail() string                           { return "" }
func (*projectionTestVerifier) GetUsername() string                        { return "user" }
func (verifier *projectionTestVerifier) GetRefreshTokenDisable() bool      { return verifier.disabled }
func (verifier *projectionTestVerifier) SetRefreshTokenDisable(value bool) { verifier.disabled = value }
func (*projectionTestVerifier) CheckToken(context.Context, string) error   { return nil }
func (verifier *projectionTestVerifier) Root() bool                        { return verifier.root }
func (verifier *projectionTestVerifier) Verify(context.Context) (bool, security.Verifier, error) {
	return true, verifier, nil
}
func (verifier *projectionTestVerifier) GetPersonAccessToken() string      { return verifier.token }
func (verifier *projectionTestVerifier) SetPersonAccessToken(value string) { verifier.token = value }
