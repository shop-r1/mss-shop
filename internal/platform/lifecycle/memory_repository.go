package lifecycle

import (
	"context"
	"fmt"
	"sync"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

// MemoryRepository is a concurrency-safe simulation repository. It performs no
// database or network I/O and is not a production persistence implementation.
type MemoryRepository struct {
	mu        sync.RWMutex
	resources map[tenancy.TenantID]TenantResource
}

func NewMemoryRepository(resources ...TenantResource) (*MemoryRepository, error) {
	repository := &MemoryRepository{
		resources: make(map[tenancy.TenantID]TenantResource, len(resources)),
	}
	for _, resource := range resources {
		if resource.Status.Phase == "" {
			resource.Status.Phase = PhasePending
		}
		if err := resource.Validate(); err != nil {
			return nil, err
		}
		if _, exists := repository.resources[resource.TenantID]; exists {
			return nil, fmt.Errorf("%w: duplicate tenant %q", ErrInvalidResource, resource.TenantID)
		}
		repository.resources[resource.TenantID] = resource.Clone()
	}
	return repository, nil
}

func (r *MemoryRepository) Get(
	ctx context.Context,
	id tenancy.TenantID,
) (TenantResource, error) {
	if err := ctx.Err(); err != nil {
		return TenantResource{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	resource, ok := r.resources[id]
	if !ok {
		return TenantResource{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return resource.Clone(), nil
}

func (r *MemoryRepository) CompareAndSwapStatus(
	ctx context.Context,
	id tenancy.TenantID,
	expectedResourceVersion uint64,
	status Status,
) (TenantResource, error) {
	if err := ctx.Err(); err != nil {
		return TenantResource{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	resource, ok := r.resources[id]
	if !ok {
		return TenantResource{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if resource.ResourceVersion != expectedResourceVersion {
		return TenantResource{}, fmt.Errorf(
			"%w: expected %d, got %d",
			ErrConflict,
			expectedResourceVersion,
			resource.ResourceVersion,
		)
	}
	resource.Status = status.Clone()
	resource.ResourceVersion++
	if err := resource.Validate(); err != nil {
		return TenantResource{}, err
	}
	r.resources[id] = resource.Clone()
	return resource.Clone(), nil
}

func (r *MemoryRepository) UpdateSpec(
	ctx context.Context,
	id tenancy.TenantID,
	expectedResourceVersion uint64,
	spec Spec,
) (TenantResource, error) {
	if err := ctx.Err(); err != nil {
		return TenantResource{}, err
	}
	if !spec.Desired.valid() {
		return TenantResource{}, fmt.Errorf("%w: unsupported desired state %q", ErrInvalidResource, spec.Desired)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	resource, ok := r.resources[id]
	if !ok {
		return TenantResource{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if resource.ResourceVersion != expectedResourceVersion {
		return TenantResource{}, fmt.Errorf(
			"%w: expected %d, got %d",
			ErrConflict,
			expectedResourceVersion,
			resource.ResourceVersion,
		)
	}
	if resource.Spec == spec {
		return resource.Clone(), nil
	}
	resource.Spec = spec
	resource.Generation++
	resource.ResourceVersion++
	r.resources[id] = resource.Clone()
	return resource.Clone(), nil
}
