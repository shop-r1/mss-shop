package sharedcatalog

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/security"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
	"gorm.io/gorm"
)

var (
	ErrAuthenticationRequired   = errors.New("shared catalogue authentication required")
	ErrAuthorizationDenied      = errors.New("shared catalogue authorization denied")
	ErrAuthorizationUnavailable = errors.New("shared catalogue authorization unavailable")
)

type AdminAuthorizer struct {
	db          *gorm.DB
	binding     fixedbinding.Binding
	principal   func(*gin.Context) security.Verifier
	permissions map[string]permissionDefinition
}

func NewAdminAuthorizer(
	db *gorm.DB,
	binding fixedbinding.Binding,
	registry legacydb.Registry,
	principal func(*gin.Context) security.Verifier,
) (*AdminAuthorizer, error) {
	if db == nil {
		return nil, errors.New("shared catalogue authorization database is required")
	}
	if err := binding.Validate(); err != nil {
		return nil, fmt.Errorf("shared catalogue authorization binding: %w", err)
	}
	if principal == nil {
		return nil, errors.New("shared catalogue principal resolver is required")
	}
	return &AdminAuthorizer{
		db:          db,
		binding:     binding,
		principal:   principal,
		permissions: permissionIndex(registry),
	}, nil
}

// Authorize requires both the resource-specific component grant and the MSS
// API policy for the concrete route template.
func (authorizer *AdminAuthorizer) Authorize(ctx *gin.Context, permission string) error {
	if authorizer == nil || authorizer.db == nil || authorizer.principal == nil || ctx == nil || ctx.Request == nil {
		return ErrAuthorizationUnavailable
	}
	definition, declared := authorizer.permissions[permission]
	if !declared || ctx.Param("resource") != definition.Resource {
		return ErrAuthorizationDenied
	}
	route, declared := routeForRequest(ctx.Request.Method, ctx.FullPath(), definition.Action)
	if !declared {
		return ErrAuthorizationDenied
	}
	principal := authorizer.principal(ctx)
	if nilVerifier(principal) || strings.TrimSpace(principal.GetRoleID()) == "" {
		return ErrAuthenticationRequired
	}
	if principal.Root() {
		return nil
	}

	policyTable := qualifiedCoreTable(authorizer.binding, "mss_boot_casbin_rule")
	checks := []struct {
		accessType string
		path       string
		method     string
	}{
		{accessType: adminpkg.ComponentAccessType.String(), path: componentPath(definition.Resource, route.ComponentOperation), method: httpGet},
		{accessType: adminpkg.APIAccessType.String(), path: route.Path, method: route.Method},
	}
	for _, check := range checks {
		var count int64
		if err := authorizer.db.WithContext(ctx.Request.Context()).Table(policyTable).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p",
			principal.GetRoleID(),
			check.accessType,
			check.path,
			check.method,
		).Count(&count).Error; err != nil {
			return fmt.Errorf("%w: read MSS policy", ErrAuthorizationUnavailable)
		}
		if count == 0 {
			return ErrAuthorizationDenied
		}
	}
	return nil
}

const httpGet = "GET"

func nilVerifier(value security.Verifier) bool {
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

func qualifiedCoreTable(binding fixedbinding.Binding, table string) string {
	// GORM accepts a validated schema.table name and quotes both identifiers.
	// Passing an already quoted expression to Table during writes makes GORM
	// reinterpret it and can silently drop the schema.
	return binding.CoreSchema + "." + table
}
