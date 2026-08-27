package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

var (
	ErrInProgress   = errors.New("message is already in progress")
	ErrInvalidClaim = errors.New("invalid inbox claim")
)

type Claim interface {
	Duplicate() bool
}

type memoryClaim struct {
	key       inboxKey
	token     uint64
	duplicate bool
}

func (c memoryClaim) Duplicate() bool {
	return c.duplicate
}

type Inbox interface {
	Begin(context.Context, tenancy.TenantID, string) (Claim, error)
	Complete(context.Context, Claim) error
	Release(context.Context, Claim) error
}

type inboxKey struct {
	tenantID  tenancy.TenantID
	messageID string
}

type inboxEntry struct {
	token     uint64
	completed bool
}

// MemoryInbox models tenant-scoped message claims for unit tests and local
// simulation. A production inbox must persist the claim and business mutation
// atomically where the underlying store permits it.
type MemoryInbox struct {
	mu      sync.Mutex
	next    uint64
	entries map[inboxKey]inboxEntry
}

func NewMemoryInbox() *MemoryInbox {
	return &MemoryInbox{entries: make(map[inboxKey]inboxEntry)}
}

func (i *MemoryInbox) Begin(
	ctx context.Context,
	tenantID tenancy.TenantID,
	messageID string,
) (Claim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := tenantID.Validate(); err != nil || messageID == "" {
		return nil, fmt.Errorf("%w: tenant and message ids are required", ErrInvalidClaim)
	}
	key := inboxKey{tenantID: tenantID, messageID: messageID}
	i.mu.Lock()
	defer i.mu.Unlock()
	if current, exists := i.entries[key]; exists {
		if current.completed {
			return memoryClaim{key: key, token: current.token, duplicate: true}, nil
		}
		return nil, fmt.Errorf("%w: tenant=%s message=%s", ErrInProgress, tenantID, messageID)
	}
	i.next++
	entry := inboxEntry{token: i.next}
	i.entries[key] = entry
	return memoryClaim{key: key, token: entry.token}, nil
}

func (i *MemoryInbox) Complete(ctx context.Context, claim Claim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claimed, ok := claim.(memoryClaim)
	if !ok || claimed.duplicate || claimed.token == 0 {
		return ErrInvalidClaim
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	entry, exists := i.entries[claimed.key]
	if !exists || entry.token != claimed.token || entry.completed {
		return ErrInvalidClaim
	}
	entry.completed = true
	i.entries[claimed.key] = entry
	return nil
}

func (i *MemoryInbox) Release(ctx context.Context, claim Claim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claimed, ok := claim.(memoryClaim)
	if !ok || claimed.duplicate || claimed.token == 0 {
		return ErrInvalidClaim
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	entry, exists := i.entries[claimed.key]
	if !exists || entry.token != claimed.token || entry.completed {
		return ErrInvalidClaim
	}
	delete(i.entries, claimed.key)
	return nil
}
