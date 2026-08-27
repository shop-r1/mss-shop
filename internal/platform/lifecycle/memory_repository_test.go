package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

func TestMemoryRepositoryCompareAndSwapAndClone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := tenancy.TenantID("tenant-a")
	repository, err := NewMemoryRepository(TenantResource{
		TenantID:        id,
		ProvisioningKey: "abc123",
		Generation:      1,
		ResourceVersion: 1,
		Spec:            Spec{Desired: DesiredActive},
	})
	if err != nil {
		t.Fatal(err)
	}

	resource, err := repository.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	resource.Status.Checkpoints = append(resource.Status.Checkpoints, Checkpoint{Step: "outside"})

	unchanged, err := repository.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Status.Checkpoints) != 0 {
		t.Fatal("Get returned repository-owned slice")
	}

	updated, err := repository.CompareAndSwapStatus(ctx, id, 1, Status{Phase: PhaseReconciling})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ResourceVersion != 2 {
		t.Fatalf("resource version = %d, want 2", updated.ResourceVersion)
	}
	_, err = repository.CompareAndSwapStatus(ctx, id, 1, Status{Phase: PhaseReady})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}
}

func TestMemoryRepositoryConcurrentCASAllowsOneWriter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := tenancy.TenantID("tenant-a")
	repository, err := NewMemoryRepository(TenantResource{
		TenantID:        id,
		ProvisioningKey: "abc123",
		Generation:      1,
		ResourceVersion: 1,
		Spec:            Spec{Desired: DesiredActive},
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, updateErr := repository.CompareAndSwapStatus(
				ctx,
				id,
				1,
				Status{Phase: PhaseReconciling},
			)
			results <- updateErr
		}()
	}
	wg.Wait()
	close(results)

	var successes, conflicts int
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}
}

func TestMemoryRepositoryUpdateSpecAdvancesGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := tenancy.TenantID("tenant-a")
	repository, err := NewMemoryRepository(TenantResource{
		TenantID:        id,
		ProvisioningKey: "abc123",
		Generation:      4,
		ResourceVersion: 7,
		Spec:            Spec{Desired: DesiredActive},
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := repository.UpdateSpec(
		ctx,
		id,
		7,
		Spec{Desired: DesiredSuspended},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != 5 || updated.ResourceVersion != 8 {
		t.Fatalf(
			"generation/version = %d/%d, want 5/8",
			updated.Generation,
			updated.ResourceVersion,
		)
	}
}
