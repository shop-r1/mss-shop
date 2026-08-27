package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/shop-r1/mss-shop/internal/platform/lifecycle"
	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
	"github.com/shop-r1/mss-shop/services/reconciler/internal/controller"
)

const (
	StepEnsureDBRole             = "ensure-db-role"
	StepEnsureCoreSchema         = "ensure-core-schema"
	StepEnsureBusinessSchema     = "ensure-business-schema"
	StepMigrateMSSCore           = "migrate-mss-core"
	StepMigrateShopBusiness      = "migrate-shop-business"
	StepEnsureRuntimeConfig      = "ensure-runtime-config"
	StepEnsureRuntimeRunning     = "ensure-runtime-running"
	StepVerifyRuntimeReady       = "verify-runtime-ready"
	StepPublishStorefrontServing = "publish-storefront-serving"
	StepPublishStorefrontStopped = "publish-storefront-unavailable"
	StepEnsureRuntimeSuspended   = "ensure-runtime-suspended"
	StepVerifyRuntimeSuspended   = "verify-runtime-suspended"
)

var ErrPrerequisite = errors.New("memory resource prerequisite not met")

var ErrSimulationOnly = errors.New("memory reconciler driver is simulation-only")

const ModeSimulation = "simulation"

type State struct {
	DBRole            bool
	CoreSchema        bool
	BusinessSchema    bool
	MSSCoreMigrated   bool
	BusinessMigrated  bool
	RuntimeConfigured bool
	RuntimeRunning    bool
	RuntimeReady      bool
	StorefrontServing bool
}

type failure struct {
	remaining int
	err       error
}

// Driver simulates the resources a future PostgreSQL/Kubernetes driver will
// inspect and ensure. It never performs operating-system, network, SQL, queue,
// or cluster operations.
type Driver struct {
	mu       sync.Mutex
	states   map[tenancy.TenantID]State
	calls    map[tenancy.TenantID]map[string]int
	failures map[tenancy.TenantID]map[string]*failure
}

func NewDriver(runtimeMode string) (*Driver, error) {
	if runtimeMode != ModeSimulation {
		return nil, fmt.Errorf("%w: mode %q", ErrSimulationOnly, runtimeMode)
	}
	return &Driver{
		states:   make(map[tenancy.TenantID]State),
		calls:    make(map[tenancy.TenantID]map[string]int),
		failures: make(map[tenancy.TenantID]map[string]*failure),
	}, nil
}

func (d *Driver) Plan(resource lifecycle.TenantResource) ([]controller.Step, error) {
	switch resource.Spec.Desired {
	case lifecycle.DesiredActive:
		return []controller.Step{
			d.step(StepEnsureDBRole, ensureDBRole),
			d.step(StepEnsureCoreSchema, ensureCoreSchema),
			d.step(StepEnsureBusinessSchema, ensureBusinessSchema),
			d.step(StepMigrateMSSCore, migrateMSSCore),
			d.step(StepMigrateShopBusiness, migrateShopBusiness),
			d.step(StepEnsureRuntimeConfig, ensureRuntimeConfig),
			d.step(StepEnsureRuntimeRunning, ensureRuntimeRunning),
			d.step(StepVerifyRuntimeReady, verifyRuntimeReady),
			d.step(StepPublishStorefrontServing, publishStorefrontServing),
		}, nil
	case lifecycle.DesiredSuspended:
		return []controller.Step{
			d.step(StepPublishStorefrontStopped, publishStorefrontUnavailable),
			d.step(StepEnsureRuntimeSuspended, ensureRuntimeSuspended),
			d.step(StepVerifyRuntimeSuspended, verifyRuntimeSuspended),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported desired state %q", resource.Spec.Desired)
	}
}

func (d *Driver) FailNext(
	tenantID tenancy.TenantID,
	stepName string,
	times int,
	err error,
) {
	if times < 1 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failures[tenantID] == nil {
		d.failures[tenantID] = make(map[string]*failure)
	}
	d.failures[tenantID][stepName] = &failure{remaining: times, err: err}
}

func (d *Driver) State(tenantID tenancy.TenantID) State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.states[tenantID]
}

func (d *Driver) Calls(tenantID tenancy.TenantID, stepName string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[tenantID][stepName]
}

type mutate func(State) (State, bool, error)

type step struct {
	name   string
	driver *Driver
	mutate mutate
}

func (d *Driver) step(name string, mutateState mutate) controller.Step {
	return &step{name: name, driver: d, mutate: mutateState}
}

func (s *step) Name() string {
	return s.name
}

func (s *step) Version() string {
	return "v1"
}

func (s *step) Ensure(
	ctx context.Context,
	resource lifecycle.TenantResource,
) (controller.Observation, error) {
	if err := ctx.Err(); err != nil {
		return controller.Observation{}, err
	}
	s.driver.mu.Lock()
	defer s.driver.mu.Unlock()
	if s.driver.calls[resource.TenantID] == nil {
		s.driver.calls[resource.TenantID] = make(map[string]int)
	}
	s.driver.calls[resource.TenantID][s.name]++
	if byStep := s.driver.failures[resource.TenantID]; byStep != nil {
		if injected := byStep[s.name]; injected != nil && injected.remaining > 0 {
			injected.remaining--
			return controller.Observation{}, injected.err
		}
	}

	current := s.driver.states[resource.TenantID]
	next, changed, err := s.mutate(current)
	if err != nil {
		return controller.Observation{}, controller.PermanentError(
			"INVALID_RESOURCE_STATE",
			"The simulated tenant resources are inconsistent.",
			err,
		)
	}
	s.driver.states[resource.TenantID] = next
	return controller.Observation{Changed: changed}, nil
}

func ensureDBRole(state State) (State, bool, error) {
	if state.DBRole {
		return state, false, nil
	}
	state.DBRole = true
	return state, true, nil
}

func ensureCoreSchema(state State) (State, bool, error) {
	if !state.DBRole {
		return state, false, fmt.Errorf("%w: database role", ErrPrerequisite)
	}
	if state.CoreSchema {
		return state, false, nil
	}
	state.CoreSchema = true
	return state, true, nil
}

func ensureBusinessSchema(state State) (State, bool, error) {
	if !state.DBRole {
		return state, false, fmt.Errorf("%w: database role", ErrPrerequisite)
	}
	if state.BusinessSchema {
		return state, false, nil
	}
	state.BusinessSchema = true
	return state, true, nil
}

func migrateMSSCore(state State) (State, bool, error) {
	if !state.CoreSchema {
		return state, false, fmt.Errorf("%w: core schema", ErrPrerequisite)
	}
	if state.MSSCoreMigrated {
		return state, false, nil
	}
	state.MSSCoreMigrated = true
	return state, true, nil
}

func migrateShopBusiness(state State) (State, bool, error) {
	if !state.BusinessSchema {
		return state, false, fmt.Errorf("%w: business schema", ErrPrerequisite)
	}
	if state.BusinessMigrated {
		return state, false, nil
	}
	state.BusinessMigrated = true
	return state, true, nil
}

func ensureRuntimeConfig(state State) (State, bool, error) {
	if !state.MSSCoreMigrated || !state.BusinessMigrated {
		return state, false, fmt.Errorf("%w: migrations", ErrPrerequisite)
	}
	if state.RuntimeConfigured {
		return state, false, nil
	}
	state.RuntimeConfigured = true
	return state, true, nil
}

func ensureRuntimeRunning(state State) (State, bool, error) {
	if !state.RuntimeConfigured {
		return state, false, fmt.Errorf("%w: runtime configuration", ErrPrerequisite)
	}
	if state.RuntimeRunning {
		return state, false, nil
	}
	state.RuntimeRunning = true
	return state, true, nil
}

func verifyRuntimeReady(state State) (State, bool, error) {
	if !state.RuntimeRunning {
		return state, false, fmt.Errorf("%w: running runtime", ErrPrerequisite)
	}
	if state.RuntimeReady {
		return state, false, nil
	}
	state.RuntimeReady = true
	return state, true, nil
}

func publishStorefrontServing(state State) (State, bool, error) {
	if !state.RuntimeReady {
		return state, false, fmt.Errorf("%w: ready runtime", ErrPrerequisite)
	}
	if state.StorefrontServing {
		return state, false, nil
	}
	state.StorefrontServing = true
	return state, true, nil
}

func publishStorefrontUnavailable(state State) (State, bool, error) {
	if !state.StorefrontServing {
		return state, false, nil
	}
	state.StorefrontServing = false
	return state, true, nil
}

func ensureRuntimeSuspended(state State) (State, bool, error) {
	changed := state.RuntimeRunning || state.RuntimeReady
	state.RuntimeRunning = false
	state.RuntimeReady = false
	return state, changed, nil
}

func verifyRuntimeSuspended(state State) (State, bool, error) {
	if state.RuntimeRunning || state.RuntimeReady || state.StorefrontServing {
		return state, false, errors.New("runtime remains available")
	}
	return state, false, nil
}
