package dispatch

import (
	"context"
	"errors"
	"testing"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

func TestMemoryInboxDeduplicatesPerTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inbox := NewMemoryInbox()
	tenantA := tenancy.TenantID("tenant-a")
	tenantB := tenancy.TenantID("tenant-b")

	claimA, err := inbox.Begin(ctx, tenantA, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.Complete(ctx, claimA); err != nil {
		t.Fatal(err)
	}
	duplicate, err := inbox.Begin(ctx, tenantA, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Duplicate() {
		t.Fatal("completed message was not recognized as duplicate")
	}

	claimB, err := inbox.Begin(ctx, tenantB, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if claimB.Duplicate() {
		t.Fatal("same message id in another tenant was incorrectly deduplicated")
	}
}

func TestMemoryInboxReleaseAllowsRetryAndRejectsStaleClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inbox := NewMemoryInbox()
	tenantID := tenancy.TenantID("tenant-a")
	first, err := inbox.Begin(ctx, tenantID, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Begin(ctx, tenantID, "message-1"); !errors.Is(err, ErrInProgress) {
		t.Fatalf("second claim error = %v, want ErrInProgress", err)
	}
	if err := inbox.Release(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, err := inbox.Begin(ctx, tenantID, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.Complete(ctx, first); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("stale completion error = %v, want ErrInvalidClaim", err)
	}
	if err := inbox.Complete(ctx, second); err != nil {
		t.Fatal(err)
	}
}
