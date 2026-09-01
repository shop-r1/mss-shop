package mallsettings

import (
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
)

type Authorizer interface {
	Authorize(*gin.Context, string) error
}

type authorizationRoute struct {
	method        string
	path          string
	componentPath string
}

var authorizationRoutes = map[string]authorizationRoute{
	PermissionRead:   {method: "GET", path: generalRoutePath, componentPath: readComponent},
	PermissionUpdate: {method: "PUT", path: generalRoutePath, componentPath: updateComponent},
}

// AdminAuthorizer keeps MSS authentication and Casbin authoritative while also
// enforcing the immutable Admin tenant selected at process startup.
type AdminAuthorizer struct {
	database  business.RequestDatabase
	principal business.PrincipalResolver
	binding   fixedbinding.Binding
}

func NewAdminAuthorizer(
	database business.RequestDatabase,
	principal business.PrincipalResolver,
	binding fixedbinding.Binding,
) (*AdminAuthorizer, error) {
	if database == nil || principal == nil {
		return nil, ErrAuthorizationUnavailable
	}
	if err := binding.Validate(); err != nil {
		return nil, ErrAuthorizationUnavailable
	}
	return &AdminAuthorizer{database: database, principal: principal, binding: binding}, nil
}

func (authorizer *AdminAuthorizer) Authorize(ctx *gin.Context, permission string) error {
	if authorizer == nil || authorizer.database == nil || authorizer.principal == nil ||
		ctx == nil || ctx.Request == nil {
		return ErrAuthorizationUnavailable
	}
	route, declared := authorizationRoutes[permission]
	if !declared || ctx.Request.Method != route.method || ctx.FullPath() != route.path {
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
		{accessType: adminpkg.ComponentAccessType, path: route.componentPath, method: "GET"},
		{accessType: adminpkg.APIAccessType, path: route.path, method: route.method},
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
