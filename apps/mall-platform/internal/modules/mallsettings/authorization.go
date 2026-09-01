package mallsettings

import (
	"errors"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/adminprojection"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
)

type Authorizer interface {
	Authorize(*gin.Context, string) error
}

// AdminAuthorizer preserves the mall-settings error contract while delegating
// the fixed route, Admin tenant, Component, and API checks to the shared MSS
// authorization projection.
type AdminAuthorizer struct {
	delegate *adminprojection.Authorizer
}

func NewAdminAuthorizer(
	database business.RequestDatabase,
	principal business.PrincipalResolver,
	binding fixedbinding.Binding,
) (*AdminAuthorizer, error) {
	delegate, err := adminprojection.NewAuthorizer(database, principal, binding, authorizationProjection)
	if err != nil {
		return nil, ErrAuthorizationUnavailable
	}
	return &AdminAuthorizer{delegate: delegate}, nil
}

func (authorizer *AdminAuthorizer) Authorize(ctx *gin.Context, permission string) error {
	if authorizer == nil || authorizer.delegate == nil {
		return ErrAuthorizationUnavailable
	}
	switch err := authorizer.delegate.Authorize(ctx, permission); {
	case err == nil:
		return nil
	case errors.Is(err, adminprojection.ErrAuthenticationRequired):
		return ErrAuthenticationRequired
	case errors.Is(err, adminprojection.ErrAuthorizationDenied):
		return ErrAuthorizationDenied
	default:
		return ErrAuthorizationUnavailable
	}
}

// nilInterface remains local because the mall-settings HTTP composition also
// uses it for application and authorizer dependency validation.
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
