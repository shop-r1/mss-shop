package lifecycle

import (
	"errors"
	"fmt"
	"time"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

type DesiredState string

const (
	DesiredActive    DesiredState = "ACTIVE"
	DesiredSuspended DesiredState = "SUSPENDED"
)

type Phase string

const (
	PhasePending     Phase = "Pending"
	PhaseReconciling Phase = "Reconciling"
	PhaseReady       Phase = "Ready"
	PhaseSuspending  Phase = "Suspending"
	PhaseSuspended   Phase = "Suspended"
	PhaseResuming    Phase = "Resuming"
	PhaseDegraded    Phase = "Degraded"
)

type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

type Spec struct {
	Desired DesiredState
}

type Condition struct {
	Type               string
	Status             ConditionStatus
	Reason             string
	Message            string
	ObservedGeneration uint64
	LastTransitionTime time.Time
}

type Checkpoint struct {
	Step        string
	Version     string
	Generation  uint64
	Changed     bool
	CompletedAt time.Time
}

type Status struct {
	ObservedGeneration uint64
	Phase              Phase
	CurrentStep        string
	Conditions         []Condition
	Checkpoints        []Checkpoint
	FailureCount       uint32
	RetryAt            *time.Time
}

type TenantResource struct {
	TenantID        tenancy.TenantID
	ProvisioningKey string
	Generation      uint64
	ResourceVersion uint64
	Spec            Spec
	Status          Status
}

var ErrInvalidResource = errors.New("invalid tenant lifecycle resource")

func (r TenantResource) Validate() error {
	if err := r.TenantID.Validate(); err != nil {
		return fmt.Errorf("%w: tenant id: %v", ErrInvalidResource, err)
	}
	if r.ProvisioningKey == "" {
		return fmt.Errorf("%w: provisioning key is required", ErrInvalidResource)
	}
	if r.Generation == 0 {
		return fmt.Errorf("%w: generation must be positive", ErrInvalidResource)
	}
	if r.ResourceVersion == 0 {
		return fmt.Errorf("%w: resource version must be positive", ErrInvalidResource)
	}
	if !r.Spec.Desired.valid() {
		return fmt.Errorf("%w: unsupported desired state %q", ErrInvalidResource, r.Spec.Desired)
	}
	if r.Status.Phase != "" && !r.Status.Phase.valid() {
		return fmt.Errorf("%w: unsupported observed phase %q", ErrInvalidResource, r.Status.Phase)
	}
	if r.Status.ObservedGeneration > r.Generation {
		return fmt.Errorf("%w: observed generation exceeds desired generation", ErrInvalidResource)
	}
	return nil
}

func (s DesiredState) valid() bool {
	return s == DesiredActive || s == DesiredSuspended
}

func (p Phase) valid() bool {
	switch p {
	case PhasePending, PhaseReconciling, PhaseReady, PhaseSuspending,
		PhaseSuspended, PhaseResuming, PhaseDegraded:
		return true
	default:
		return false
	}
}

func (r TenantResource) Clone() TenantResource {
	r.Status = r.Status.Clone()
	return r
}

func (s Status) Clone() Status {
	s.Conditions = append([]Condition(nil), s.Conditions...)
	s.Checkpoints = append([]Checkpoint(nil), s.Checkpoints...)
	if s.RetryAt != nil {
		retryAt := *s.RetryAt
		s.RetryAt = &retryAt
	}
	return s
}
