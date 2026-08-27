package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shop-r1/mss-shop/internal/platform/jobs"
	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

func testEnvelope(tenantID tenancy.TenantID, messageID string) jobs.Envelope {
	return jobs.Envelope{
		ID:         messageID,
		TenantID:   tenantID,
		Kind:       "inventory.release",
		Version:    1,
		OccurredAt: time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC),
		Payload:    json.RawMessage(`{"tenantId":"forged","schema":"forged_schema"}`),
	}
}

func TestDispatcherDeduplicatesAndUsesEnvelopeTenantScope(t *testing.T) {
	t.Parallel()
	tenantID := tenancy.TenantID("tenant-a")
	var calls int
	var observedTenant tenancy.TenantID
	handler := &testHandler{
		handle: func(_ context.Context, scope Scope, _ json.RawMessage) error {
			calls++
			observedTenant = scope.TenantID()
			return nil
		},
	}
	dispatcher, err := New(NewMemoryInbox(), handler)
	if err != nil {
		t.Fatal(err)
	}

	first, err := dispatcher.Dispatch(context.Background(), testEnvelope(tenantID, "message-1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := dispatcher.Dispatch(context.Background(), testEnvelope(tenantID, "message-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Handled || !second.Duplicate || calls != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d", first, second, calls)
	}
	if observedTenant != tenantID {
		t.Fatalf("handler tenant = %q, want envelope tenant %q", observedTenant, tenantID)
	}
}

func TestDispatcherFailureReleasesClaimForRetry(t *testing.T) {
	t.Parallel()
	var calls int
	handler := &testHandler{
		handle: func(context.Context, Scope, json.RawMessage) error {
			calls++
			if calls == 1 {
				return errors.New("temporary failure")
			}
			return nil
		},
	}
	dispatcher, err := New(NewMemoryInbox(), handler)
	if err != nil {
		t.Fatal(err)
	}
	envelope := testEnvelope(tenancy.TenantID("tenant-a"), "message-1")

	first, err := dispatcher.Dispatch(context.Background(), envelope)
	if err == nil || !first.Requeue {
		t.Fatalf("first result/error = %+v / %v, want retry", first, err)
	}
	second, err := dispatcher.Dispatch(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Handled || calls != 2 {
		t.Fatalf("second=%+v calls=%d, want handled after retry", second, calls)
	}
}

func TestDispatcherDeduplicationKeyIncludesTenant(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := make(map[tenancy.TenantID]int)
	handler := &testHandler{
		handle: func(_ context.Context, scope Scope, _ json.RawMessage) error {
			mu.Lock()
			defer mu.Unlock()
			seen[scope.TenantID()]++
			return nil
		},
	}
	dispatcher, err := New(NewMemoryInbox(), handler)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenantID := range []tenancy.TenantID{"tenant-a", "tenant-b"} {
		if _, err := dispatcher.Dispatch(context.Background(), testEnvelope(tenantID, "shared-id")); err != nil {
			t.Fatal(err)
		}
	}
	if seen["tenant-a"] != 1 || seen["tenant-b"] != 1 {
		t.Fatalf("tenant-scoped counts = %+v", seen)
	}
}

func TestConcurrentDuplicateIsNotHandledTwice(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	handler := &testHandler{
		handle: func(context.Context, Scope, json.RawMessage) error {
			calls.Add(1)
			close(started)
			<-release
			return nil
		},
	}
	dispatcher, err := New(NewMemoryInbox(), handler)
	if err != nil {
		t.Fatal(err)
	}
	envelope := testEnvelope(tenancy.TenantID("tenant-a"), "message-1")
	firstDone := make(chan error, 1)
	go func() {
		_, dispatchErr := dispatcher.Dispatch(context.Background(), envelope)
		firstDone <- dispatchErr
	}()
	<-started

	concurrent, err := dispatcher.Dispatch(context.Background(), envelope)
	if !errors.Is(err, ErrInProgress) || !concurrent.Requeue {
		t.Fatalf("concurrent result/error = %+v / %v", concurrent, err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	duplicate, err := dispatcher.Dispatch(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Duplicate || calls.Load() != 1 {
		t.Fatalf("duplicate=%+v calls=%d", duplicate, calls.Load())
	}
}

type testHandler struct {
	handle func(context.Context, Scope, json.RawMessage) error
}

func (*testHandler) Kind() string { return "inventory.release" }
func (*testHandler) Version() int { return 1 }
func (h *testHandler) Handle(
	ctx context.Context,
	scope Scope,
	payload json.RawMessage,
) error {
	return h.handle(ctx, scope, payload)
}
