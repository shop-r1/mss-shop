package lifecycle

import (
	"context"
	"errors"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

var (
	ErrNotFound = errors.New("tenant lifecycle resource not found")
	ErrConflict = errors.New("tenant lifecycle resource version conflict")
)

type Repository interface {
	Get(context.Context, tenancy.TenantID) (TenantResource, error)
	CompareAndSwapStatus(
		context.Context,
		tenancy.TenantID,
		uint64,
		Status,
	) (TenantResource, error)
}

// DesiredStateRepository is kept separate from Repository because control-plane
// writers and reconcilers must not share mutation authority in production.
// The in-memory implementation exposes it only for simulation and tests.
type DesiredStateRepository interface {
	UpdateSpec(
		context.Context,
		tenancy.TenantID,
		uint64,
		Spec,
	) (TenantResource, error)
}
