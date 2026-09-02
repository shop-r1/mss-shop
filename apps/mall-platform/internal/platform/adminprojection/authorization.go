package adminprojection

import (
	"errors"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
)

var (
	ErrAuthenticationRequired   = errors.New("Admin projection authentication required")
	ErrAuthorizationDenied      = errors.New("Admin projection authorization denied")
	ErrAuthorizationUnavailable = errors.New("Admin projection authorization unavailable")
)

type Authorizer struct {
	database  business.RequestDatabase
	principal business.PrincipalResolver
	binding   fixedbinding.Binding
	routes    map[string]RouteGrant
}

func NewAuthorizer(
	database business.RequestDatabase,
	principal business.PrincipalResolver,
	binding fixedbinding.Binding,
	projection Projection,
) (*Authorizer, error) {
	if database == nil || principal == nil {
		return nil, ErrAuthorizationUnavailable
	}
	if err := binding.Validate(); err != nil {
		return nil, ErrAuthorizationUnavailable
	}
	validated, err := projection.cloneAndValidate()
	if err != nil {
		return nil, ErrAuthorizationUnavailable
	}
	routes := make(map[string]RouteGrant, len(validated.Routes))
	for _, route := range validated.Routes {
		routes[route.Permission] = route
	}
	return &Authorizer{
		database: database, principal: principal, binding: binding, routes: routes,
	}, nil
}

// Authorize binds one declared permission to its exact route and requires both
// the persisted Component grant and API grant. Root bypass is considered only
// after the fixed MSS Admin tenant has matched.
func (authorizer *Authorizer) Authorize(ctx *gin.Context, permission string) error {
	if authorizer == nil || authorizer.database == nil || authorizer.principal == nil ||
		ctx == nil || ctx.Request == nil {
		return ErrAuthorizationUnavailable
	}
	route, declared := authorizer.routes[permission]
	if !declared || ctx.Request.Method != route.Method || ctx.FullPath() != route.Path {
		return ErrAuthorizationDenied
	}
	principal := authorizer.principal(ctx)
	if nilInterface(principal) || strings.TrimSpace(principal.GetRoleID()) == "" {
		return ErrAuthenticationRequired
	}
	if principal.GetTenantID() != authorizer.binding.AdminTenantID {
		return ErrAuthorizationDenied
	}
	if principal.Root() {
		return nil
	}
	database, available := authorizer.database(ctx.Request.Context())
	if !available || database == nil {
		return ErrAuthorizationUnavailable
	}
	checks := []struct {
		accessType adminpkg.AccessType
		path       string
		method     string
	}{
		{accessType: adminpkg.ComponentAccessType, path: route.ComponentPath, method: "GET"},
		{accessType: adminpkg.APIAccessType, path: route.Path, method: route.Method},
	}
	for _, check := range checks {
		var count int64
		if err := database.WithContext(ctx.Request.Context()).Model(new(models.CasbinRule)).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", principal.GetRoleID(), check.accessType.String(), check.path, check.method,
		).Count(&count).Error; err != nil {
			return ErrAuthorizationUnavailable
		}
		if count != 1 {
			return ErrAuthorizationDenied
		}
	}
	return nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
