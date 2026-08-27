package controller_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shop-r1/mss-shop/internal/platform/lifecycle"
	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
	"github.com/shop-r1/mss-shop/services/reconciler/internal/controller"
	memorydriver "github.com/shop-r1/mss-shop/services/reconciler/internal/driver/memory"
)

var testNow = time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return testNow }

func newFixture(
	t *testing.T,
	desired lifecycle.DesiredState,
) (
	context.Context,
	tenancy.TenantID,
	*lifecycle.MemoryRepository,
	*memorydriver.Driver,
	*controller.Controller,
) {
	t.Helper()
	ctx := context.Background()
	tenantID := tenancy.TenantID("tenant-a")
	repository, err := lifecycle.NewMemoryRepository(lifecycle.TenantResource{
		TenantID:        tenantID,
		ProvisioningKey: "tntabc123",
		Generation:      1,
		ResourceVersion: 1,
		Spec:            lifecycle.Spec{Desired: desired},
	})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := memorydriver.NewDriver(memorydriver.ModeSimulation)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controller.New(
		repository,
		controller.NewMemoryLeaseManager(),
		driver,
		fixedClock{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, tenantID, repository, driver, reconciler
}

func TestReconcileActiveIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx, tenantID, repository, driver, reconciler := newFixture(t, lifecycle.DesiredActive)

	first, err := reconciler.Reconcile(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || first.Phase != lifecycle.PhaseReady {
		t.Fatalf("first result = %+v, want changed and ready", first)
	}
	state := driver.State(tenantID)
	if !state.DBRole || !state.CoreSchema || !state.BusinessSchema ||
		!state.MSSCoreMigrated || !state.BusinessMigrated ||
		!state.RuntimeConfigured || !state.RuntimeRunning ||
		!state.RuntimeReady || !state.StorefrontServing {
		t.Fatalf("incomplete simulated state: %+v", state)
	}

	second, err := reconciler.Reconcile(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || second.Phase != lifecycle.PhaseReady {
		t.Fatalf("second result = %+v, want unchanged and ready", second)
	}
	if calls := driver.Calls(tenantID, memorydriver.StepEnsureDBRole); calls != 2 {
		t.Fatalf("ensure-db-role calls = %d, want 2 actual-state checks", calls)
	}

	resource, err := repository.Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Status.ObservedGeneration != resource.Generation {
		t.Fatalf(
			"observed generation = %d, want %d",
			resource.Status.ObservedGeneration,
			resource.Generation,
		)
	}
	if len(resource.Status.Checkpoints) != 9 {
		t.Fatalf("checkpoint count = %d, want 9 upserted checkpoints", len(resource.Status.Checkpoints))
	}
}

func TestReconcileRetryConvergesWithoutDuplicatingResources(t *testing.T) {
	t.Parallel()
	ctx, tenantID, repository, driver, reconciler := newFixture(t, lifecycle.DesiredActive)
	driver.FailNext(
		tenantID,
		memorydriver.StepEnsureBusinessSchema,
		1,
		controller.RetryableError(
			"PROVIDER_UNAVAILABLE",
			"The simulated provider is temporarily unavailable.",
			errors.New("transient fault"),
		),
	)

	first, err := reconciler.Reconcile(ctx, tenantID)
	if err == nil {
		t.Fatal("first reconcile unexpectedly succeeded")
	}
	if !first.Requeue || first.RequeueAfter != time.Second {
		t.Fatalf("first result = %+v, want one-second retry", first)
	}
	failed, err := repository.Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status.Phase != lifecycle.PhaseDegraded || failed.Status.FailureCount != 1 {
		t.Fatalf("failure status = %+v", failed.Status)
	}
	if failed.Status.RetryAt == nil || !failed.Status.RetryAt.Equal(testNow.Add(time.Second)) {
		t.Fatalf("retry time = %v", failed.Status.RetryAt)
	}

	second, err := reconciler.Reconcile(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Changed || second.Phase != lifecycle.PhaseReady {
		t.Fatalf("retry result = %+v, want convergence", second)
	}
	state := driver.State(tenantID)
	if !state.DBRole || !state.CoreSchema || !state.BusinessSchema || !state.StorefrontServing {
		t.Fatalf("retry did not converge: %+v", state)
	}
	if calls := driver.Calls(tenantID, memorydriver.StepEnsureDBRole); calls != 2 {
		t.Fatalf("earlier ensure step calls = %d, want 2 idempotent checks", calls)
	}
}

func TestReconcileSuspendAndResume(t *testing.T) {
	t.Parallel()
	ctx, tenantID, repository, driver, reconciler := newFixture(t, lifecycle.DesiredActive)
	if _, err := reconciler.Reconcile(ctx, tenantID); err != nil {
		t.Fatal(err)
	}

	active, err := repository.Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateSpec(
		ctx,
		tenantID,
		active.ResourceVersion,
		lifecycle.Spec{Desired: lifecycle.DesiredSuspended},
	); err != nil {
		t.Fatal(err)
	}
	suspendedResult, err := reconciler.Reconcile(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if suspendedResult.Phase != lifecycle.PhaseSuspended {
		t.Fatalf("suspend phase = %s", suspendedResult.Phase)
	}
	suspendedState := driver.State(tenantID)
	if suspendedState.StorefrontServing || suspendedState.RuntimeRunning || suspendedState.RuntimeReady {
		t.Fatalf("runtime still available after suspension: %+v", suspendedState)
	}

	suspended, err := repository.Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateSpec(
		ctx,
		tenantID,
		suspended.ResourceVersion,
		lifecycle.Spec{Desired: lifecycle.DesiredActive},
	); err != nil {
		t.Fatal(err)
	}
	resumedResult, err := reconciler.Reconcile(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if resumedResult.Phase != lifecycle.PhaseReady {
		t.Fatalf("resume phase = %s", resumedResult.Phase)
	}
	resumedState := driver.State(tenantID)
	if !resumedState.StorefrontServing || !resumedState.RuntimeRunning || !resumedState.RuntimeReady {
		t.Fatalf("runtime not available after resume: %+v", resumedState)
	}
}

func TestGenerationChangeDuringStepCannotMarkOldPlanReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tenantID := tenancy.TenantID("tenant-a")
	repository, err := lifecycle.NewMemoryRepository(lifecycle.TenantResource{
		TenantID:        tenantID,
		ProvisioningKey: "tntabc123",
		Generation:      1,
		ResourceVersion: 1,
		Spec:            lifecycle.Spec{Desired: lifecycle.DesiredActive},
	})
	if err != nil {
		t.Fatal(err)
	}
	step := &stepFunc{
		name: "change-generation",
		ensure: func(_ context.Context, resource lifecycle.TenantResource) (controller.Observation, error) {
			_, updateErr := repository.UpdateSpec(
				ctx,
				tenantID,
				resource.ResourceVersion,
				lifecycle.Spec{Desired: lifecycle.DesiredSuspended},
			)
			return controller.Observation{Changed: true}, updateErr
		},
	}
	reconciler, err := controller.New(
		repository,
		controller.NewMemoryLeaseManager(),
		controller.PlannerFunc(func(lifecycle.TenantResource) ([]controller.Step, error) {
			return []controller.Step{step}, nil
		}),
		fixedClock{},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := reconciler.Reconcile(ctx, tenantID)
	if !errors.Is(err, lifecycle.ErrConflict) || !result.Requeue {
		t.Fatalf("result/error = %+v / %v, want requeue conflict", result, err)
	}
	resource, getErr := repository.Get(ctx, tenantID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if resource.Spec.Desired != lifecycle.DesiredSuspended {
		t.Fatalf("desired state = %s, want suspended", resource.Spec.Desired)
	}
	if resource.Status.Phase == lifecycle.PhaseReady ||
		resource.Status.ObservedGeneration == resource.Generation {
		t.Fatalf("stale plan marked new generation observed: %+v", resource.Status)
	}
}

func TestFailureStatusContainsOnlySafeMessage(t *testing.T) {
	t.Parallel()
	ctx, tenantID, repository, driver, reconciler := newFixture(t, lifecycle.DesiredActive)
	driver.FailNext(
		tenantID,
		memorydriver.StepEnsureDBRole,
		1,
		errors.New("postgres://user:top-secret@example.invalid/database"),
	)
	if _, err := reconciler.Reconcile(ctx, tenantID); err == nil {
		t.Fatal("reconcile unexpectedly succeeded")
	} else if strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("returned error exposed diagnostic secret: %v", err)
	}
	resource, err := repository.Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Status.Conditions) != 1 {
		t.Fatalf("conditions = %+v", resource.Status.Conditions)
	}
	condition := resource.Status.Conditions[0]
	if strings.Contains(condition.Message, "top-secret") || condition.Message != "A tenant lifecycle step failed." {
		t.Fatalf("unsafe condition message: %q", condition.Message)
	}
}

func TestEmptyPlanCannotMarkTenantReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tenantID := tenancy.TenantID("tenant-a")
	repository, err := lifecycle.NewMemoryRepository(lifecycle.TenantResource{
		TenantID:        tenantID,
		ProvisioningKey: "tntabc123",
		Generation:      1,
		ResourceVersion: 1,
		Spec:            lifecycle.Spec{Desired: lifecycle.DesiredActive},
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controller.New(
		repository,
		controller.NewMemoryLeaseManager(),
		controller.PlannerFunc(func(lifecycle.TenantResource) ([]controller.Step, error) {
			return nil, nil
		}),
		fixedClock{},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconciler.Reconcile(ctx, tenantID)
	if err == nil || result.Requeue || result.Phase != lifecycle.PhaseDegraded {
		t.Fatalf("result/error = %+v / %v, want permanent degraded failure", result, err)
	}
	resource, getErr := repository.Get(ctx, tenantID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if resource.Status.Phase == lifecycle.PhaseReady {
		t.Fatal("empty plan marked tenant ready")
	}
}

func TestMemoryLeasePreventsConcurrentReconcile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tenantID := tenancy.TenantID("tenant-a")
	repository, err := lifecycle.NewMemoryRepository(lifecycle.TenantResource{
		TenantID:        tenantID,
		ProvisioningKey: "tntabc123",
		Generation:      1,
		ResourceVersion: 1,
		Spec:            lifecycle.Spec{Desired: lifecycle.DesiredActive},
	})
	if err != nil {
		t.Fatal(err)
	}
	leases := controller.NewMemoryLeaseManager()
	held, err := leases.Acquire(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := memorydriver.NewDriver(memorydriver.ModeSimulation)
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controller.New(repository, leases, driver, fixedClock{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconciler.Reconcile(ctx, tenantID)
	if !errors.Is(err, controller.ErrLeaseHeld) || !result.Requeue {
		t.Fatalf("result/error = %+v / %v, want lease requeue", result, err)
	}
	held.Release()
	if _, err := reconciler.Reconcile(ctx, tenantID); err != nil {
		t.Fatalf("lease was not released: %v", err)
	}
}

type stepFunc struct {
	name   string
	ensure func(context.Context, lifecycle.TenantResource) (controller.Observation, error)
}

func (s *stepFunc) Name() string    { return s.name }
func (s *stepFunc) Version() string { return "v1" }
func (s *stepFunc) Ensure(
	ctx context.Context,
	resource lifecycle.TenantResource,
) (controller.Observation, error) {
	return s.ensure(ctx, resource)
}
