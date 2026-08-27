package controller

import (
	"context"
	"fmt"
	"sync"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

// MemoryLeaseManager serializes reconcilers in one process. A durable lease
// with fencing is required before multiple production replicas are enabled.
type MemoryLeaseManager struct {
	mu   sync.Mutex
	held map[tenancy.TenantID]struct{}
}

func NewMemoryLeaseManager() *MemoryLeaseManager {
	return &MemoryLeaseManager{held: make(map[tenancy.TenantID]struct{})}
}

func (m *MemoryLeaseManager) Acquire(
	ctx context.Context,
	tenantID tenancy.TenantID,
) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.held[tenantID]; exists {
		return nil, fmt.Errorf("%w: %s", ErrLeaseHeld, tenantID)
	}
	m.held[tenantID] = struct{}{}
	return &memoryLease{manager: m, tenantID: tenantID}, nil
}

type memoryLease struct {
	once     sync.Once
	manager  *MemoryLeaseManager
	tenantID tenancy.TenantID
}

func (l *memoryLease) Release() {
	l.once.Do(func() {
		l.manager.mu.Lock()
		defer l.manager.mu.Unlock()
		delete(l.manager.held, l.tenantID)
	})
}
