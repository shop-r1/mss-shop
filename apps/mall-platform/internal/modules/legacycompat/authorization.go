package legacycompat

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
)

var (
	ErrAuthenticationRequired   = errors.New("legacy authentication required")
	ErrAuthorizationDenied      = errors.New("legacy authorization denied")
	ErrAuthorizationUnavailable = errors.New("legacy authorization unavailable")
)

type Authorizer interface {
	Authorize(*gin.Context, string, Operation) error
}

type AdminAuthorizer struct {
	database  business.RequestDatabase
	principal business.PrincipalResolver
	binding   fixedbinding.Binding
	registry  legacydb.Registry
}

func NewAdminAuthorizer(
	database business.RequestDatabase,
	principal business.PrincipalResolver,
	binding fixedbinding.Binding,
	registry legacydb.Registry,
) (*AdminAuthorizer, error) {
	if database == nil {
		return nil, errors.New("legacy authorization database is required")
	}
	if principal == nil {
		return nil, errors.New("legacy principal resolver is required")
	}
	if err := binding.Validate(); err != nil {
		return nil, fmt.Errorf("legacy authorization binding: %w", err)
	}
	if len(registry.All()) != legacydb.ExpectedMallResourceCount {
		return nil, errors.New("legacy authorization registry is incomplete")
	}
	return &AdminAuthorizer{
		database: database, principal: principal, binding: binding, registry: registry,
	}, nil
}

func (authorizer *AdminAuthorizer) Authorize(ctx *gin.Context, resource string, operation Operation) error {
	if authorizer == nil || authorizer.database == nil || authorizer.principal == nil || ctx == nil || ctx.Request == nil {
		return ErrAuthorizationUnavailable
	}
	definition, ok := authorizer.registry.Lookup(resource)
	if !ok || !operationAllowed(definition, operation) {
		return ErrAuthorizationDenied
	}
	method, path, declared := routeFor(operation)
	if !declared || ctx.Request.Method != method || ctx.FullPath() != path {
		return ErrAuthorizationDenied
	}
	principal := authorizer.principal(ctx)
	if nilInterface(principal) || strings.TrimSpace(principal.GetRoleID()) == "" {
		return ErrAuthenticationRequired
	}
	// The MSS core scope must match its own immutable server binding before root
	// or role grants are considered. It is deliberately not the control-plane
	// TenantID or old-row LegacyTenantID.
	if principal.GetTenantID() != authorizer.binding.AdminTenantID {
		return ErrAuthorizationDenied
	}
	if principal.Root() {
		return nil
	}
	db, available := authorizer.database(ctx.Request.Context())
	if !available || db == nil {
		return ErrAuthorizationUnavailable
	}
	checks := []struct {
		accessType string
		path       string
		method     string
	}{
		{accessType: adminpkg.ComponentAccessType.String(), path: componentPath(definition, operation), method: "GET"},
		{accessType: adminpkg.APIAccessType.String(), path: path, method: method},
	}
	for _, check := range checks {
		var count int64
		if err := db.WithContext(ctx.Request.Context()).Model(&models.CasbinRule{}).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p",
			principal.GetRoleID(),
			check.accessType,
			check.path,
			check.method,
		).Count(&count).Error; err != nil {
			return fmt.Errorf("%w: read MSS %s policy", ErrAuthorizationUnavailable, check.accessType)
		}
		if count == 0 {
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
