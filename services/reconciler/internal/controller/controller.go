package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shop-r1/mss-shop/internal/platform/lifecycle"
	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

const readyCondition = "Ready"

type Controller struct {
	repository lifecycle.Repository
	leases     LeaseManager
	planner    Planner
	clock      Clock
	backoff    func(uint32) time.Duration
}

func New(
	repository lifecycle.Repository,
	leases LeaseManager,
	planner Planner,
	clock Clock,
) (*Controller, error) {
	if repository == nil || leases == nil || planner == nil || clock == nil {
		return nil, errors.New("repository, lease manager, planner, and clock are required")
	}
	return &Controller{
		repository: repository,
		leases:     leases,
		planner:    planner,
		clock:      clock,
		backoff:    exponentialBackoff,
	}, nil
}

func (c *Controller) Reconcile(
	ctx context.Context,
	tenantID tenancy.TenantID,
) (Result, error) {
	lease, err := c.leases.Acquire(ctx, tenantID)
	if err != nil {
		return Result{Requeue: errors.Is(err, ErrLeaseHeld)}, err
	}
	defer lease.Release()

	resource, err := c.repository.Get(ctx, tenantID)
	if err != nil {
		return Result{}, err
	}
	if err := resource.Validate(); err != nil {
		return Result{}, err
	}

	steps, err := c.planner.Plan(resource.Clone())
	if err != nil {
		return c.recordFailure(ctx, resource, "plan", PermanentError(
			"INVALID_PLAN",
			"The tenant lifecycle plan is invalid.",
			err,
		))
	}
	if err := validatePlan(steps); err != nil {
		return c.recordFailure(ctx, resource, "plan", PermanentError(
			"INVALID_PLAN",
			"The tenant lifecycle plan is invalid.",
			err,
		))
	}

	status := resource.Status.Clone()
	status.Phase = startingPhase(resource)
	status.CurrentStep = ""
	status.RetryAt = nil
	resource, err = c.repository.CompareAndSwapStatus(
		ctx,
		resource.TenantID,
		resource.ResourceVersion,
		status,
	)
	if err != nil {
		return conflictResult(err)
	}

	changed := false
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return Result{Changed: changed, Requeue: true, Phase: resource.Status.Phase}, err
		}

		status = resource.Status.Clone()
		status.CurrentStep = step.Name()
		resource, err = c.repository.CompareAndSwapStatus(
			ctx,
			resource.TenantID,
			resource.ResourceVersion,
			status,
		)
		if err != nil {
			return conflictResultWithChanged(err, changed)
		}

		observation, stepErr := step.Ensure(ctx, resource.Clone())
		changed = changed || observation.Changed
		if stepErr != nil {
			result, failureErr := c.recordFailure(ctx, resource, step.Name(), stepErr)
			result.Changed = changed
			return result, failureErr
		}

		status = resource.Status.Clone()
		status.CurrentStep = ""
		status.Checkpoints = setCheckpoint(status.Checkpoints, lifecycle.Checkpoint{
			Step:        step.Name(),
			Version:     step.Version(),
			Generation:  resource.Generation,
			Changed:     observation.Changed,
			CompletedAt: c.clock.Now(),
		})
		resource, err = c.repository.CompareAndSwapStatus(
			ctx,
			resource.TenantID,
			resource.ResourceVersion,
			status,
		)
		if err != nil {
			return conflictResultWithChanged(err, changed)
		}
	}

	status = resource.Status.Clone()
	status.ObservedGeneration = resource.Generation
	status.CurrentStep = ""
	status.FailureCount = 0
	status.RetryAt = nil
	status.Phase = terminalPhase(resource.Spec.Desired)
	status.Conditions = setCondition(status.Conditions, lifecycle.Condition{
		Type:               readyCondition,
		Status:             readinessCondition(resource.Spec.Desired),
		Reason:             terminalReason(resource.Spec.Desired),
		Message:            terminalMessage(resource.Spec.Desired),
		ObservedGeneration: resource.Generation,
		LastTransitionTime: c.clock.Now(),
	})
	resource, err = c.repository.CompareAndSwapStatus(
		ctx,
		resource.TenantID,
		resource.ResourceVersion,
		status,
	)
	if err != nil {
		return conflictResultWithChanged(err, changed)
	}
	return Result{Changed: changed, Phase: resource.Status.Phase}, nil
}

func (c *Controller) recordFailure(
	ctx context.Context,
	resource lifecycle.TenantResource,
	stepName string,
	err error,
) (Result, error) {
	failure := classifyFailure(err)
	status := resource.Status.Clone()
	status.Phase = lifecycle.PhaseDegraded
	status.CurrentStep = stepName
	status.FailureCount++
	status.RetryAt = nil
	result := Result{Phase: lifecycle.PhaseDegraded}
	if failure.Retryable {
		delay := c.backoff(status.FailureCount)
		retryAt := c.clock.Now().Add(delay)
		status.RetryAt = &retryAt
		result.Requeue = true
		result.RequeueAfter = delay
	}
	status.Conditions = setCondition(status.Conditions, lifecycle.Condition{
		Type:               readyCondition,
		Status:             lifecycle.ConditionFalse,
		Reason:             failure.Code,
		Message:            failure.SafeMessage,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: c.clock.Now(),
	})
	updated, updateErr := c.repository.CompareAndSwapStatus(
		ctx,
		resource.TenantID,
		resource.ResourceVersion,
		status,
	)
	if updateErr != nil {
		return conflictResult(updateErr)
	}
	result.Phase = updated.Status.Phase
	return result, failure
}

func classifyFailure(err error) *StepError {
	var failure *StepError
	if errors.As(err, &failure) {
		if failure.Code == "" {
			failure.Code = "STEP_FAILED"
		}
		if failure.SafeMessage == "" {
			failure.SafeMessage = "A tenant lifecycle step failed."
		}
		return failure
	}
	return &StepError{
		Code:        "STEP_FAILED",
		SafeMessage: "A tenant lifecycle step failed.",
		Retryable:   true,
		Cause:       err,
	}
}

func validatePlan(steps []Step) error {
	if len(steps) == 0 {
		return fmt.Errorf("%w: plan has no steps", ErrInvalidPlan)
	}
	seen := make(map[string]struct{}, len(steps))
	for index, step := range steps {
		if step == nil {
			return fmt.Errorf("%w: step %d is nil", ErrInvalidPlan, index)
		}
		if step.Name() == "" || step.Version() == "" {
			return fmt.Errorf("%w: step %d has no name or version", ErrInvalidPlan, index)
		}
		if _, duplicate := seen[step.Name()]; duplicate {
			return fmt.Errorf("%w: duplicate step %q", ErrInvalidPlan, step.Name())
		}
		seen[step.Name()] = struct{}{}
	}
	return nil
}

func setCheckpoint(
	checkpoints []lifecycle.Checkpoint,
	checkpoint lifecycle.Checkpoint,
) []lifecycle.Checkpoint {
	result := append([]lifecycle.Checkpoint(nil), checkpoints...)
	for index := range result {
		if result[index].Step == checkpoint.Step &&
			result[index].Version == checkpoint.Version &&
			result[index].Generation == checkpoint.Generation {
			result[index] = checkpoint
			return result
		}
	}
	return append(result, checkpoint)
}

func startingPhase(resource lifecycle.TenantResource) lifecycle.Phase {
	if resource.Spec.Desired == lifecycle.DesiredSuspended {
		return lifecycle.PhaseSuspending
	}
	if resource.Status.Phase == lifecycle.PhaseSuspended ||
		resource.Status.Phase == lifecycle.PhaseSuspending {
		return lifecycle.PhaseResuming
	}
	return lifecycle.PhaseReconciling
}

func terminalPhase(desired lifecycle.DesiredState) lifecycle.Phase {
	if desired == lifecycle.DesiredSuspended {
		return lifecycle.PhaseSuspended
	}
	return lifecycle.PhaseReady
}

func readinessCondition(desired lifecycle.DesiredState) lifecycle.ConditionStatus {
	if desired == lifecycle.DesiredSuspended {
		return lifecycle.ConditionFalse
	}
	return lifecycle.ConditionTrue
}

func terminalReason(desired lifecycle.DesiredState) string {
	if desired == lifecycle.DesiredSuspended {
		return "TenantSuspended"
	}
	return "ReconcileSucceeded"
}

func terminalMessage(desired lifecycle.DesiredState) string {
	if desired == lifecycle.DesiredSuspended {
		return "The tenant runtime is suspended."
	}
	return "The tenant runtime is ready."
}

func setCondition(
	conditions []lifecycle.Condition,
	condition lifecycle.Condition,
) []lifecycle.Condition {
	result := append([]lifecycle.Condition(nil), conditions...)
	for index := range result {
		if result[index].Type == condition.Type {
			result[index] = condition
			return result
		}
	}
	return append(result, condition)
}

func exponentialBackoff(failures uint32) time.Duration {
	if failures == 0 {
		return 0
	}
	shift := failures - 1
	if shift > 6 {
		shift = 6
	}
	return time.Second * time.Duration(1<<shift)
}

func conflictResult(err error) (Result, error) {
	return conflictResultWithChanged(err, false)
}

func conflictResultWithChanged(err error, changed bool) (Result, error) {
	return Result{
		Changed: changed,
		Requeue: errors.Is(err, lifecycle.ErrConflict),
	}, err
}
