package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shop-r1/mss-shop/internal/platform/lifecycle"
	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

type Observation struct {
	Changed bool
}

type Step interface {
	Name() string
	Version() string
	Ensure(context.Context, lifecycle.TenantResource) (Observation, error)
}

type Planner interface {
	Plan(lifecycle.TenantResource) ([]Step, error)
}

type PlannerFunc func(lifecycle.TenantResource) ([]Step, error)

func (f PlannerFunc) Plan(resource lifecycle.TenantResource) ([]Step, error) {
	return f(resource)
}

type Lease interface {
	Release()
}

type LeaseManager interface {
	Acquire(context.Context, tenancy.TenantID) (Lease, error)
}

type Clock interface {
	Now() time.Time
}

type Result struct {
	Changed      bool
	Requeue      bool
	RequeueAfter time.Duration
	Phase        lifecycle.Phase
}

var (
	ErrLeaseHeld   = errors.New("tenant reconcile lease is held")
	ErrInvalidPlan = errors.New("invalid reconcile plan")
)

// StepError separates a safe, status-visible failure description from its
// underlying diagnostic error. SafeMessage must not contain credentials,
// connection strings, or provider response bodies.
type StepError struct {
	Code        string
	SafeMessage string
	Retryable   bool
	Cause       error
}

func (e *StepError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.SafeMessage)
}

func (e *StepError) Unwrap() error {
	return e.Cause
}

func RetryableError(code, safeMessage string, cause error) error {
	return &StepError{
		Code:        code,
		SafeMessage: safeMessage,
		Retryable:   true,
		Cause:       cause,
	}
}

func PermanentError(code, safeMessage string, cause error) error {
	return &StepError{
		Code:        code,
		SafeMessage: safeMessage,
		Retryable:   false,
		Cause:       cause,
	}
}
