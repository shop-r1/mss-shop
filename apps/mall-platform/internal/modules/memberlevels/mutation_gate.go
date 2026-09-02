package memberlevels

import (
	"os"
)

const (
	// MutationModeEnvironment is deliberately module-specific. An absent,
	// empty or unknown value keeps every legacy mutation fail-closed.
	MutationModeEnvironment = "R1SHOP_MEMBER_LEVELS_MUTATION_MODE"

	mutationModeIsolatedCutover         = "isolated-cutover"
	mutationModeReferenceWritersStopped = "isolated-cutover-all-reference-writers-stopped"
)

type mutationGate struct {
	availability MutationAvailability
}

func environmentMutationGate() mutationGate {
	return mutationGateForMode(os.Getenv(MutationModeEnvironment))
}

func mutationGateForMode(mode string) mutationGate {
	switch mode {
	case mutationModeIsolatedCutover:
		// The operator asserts that the old member_levels writer is stopped.
		// Delete remains closed because its reference domains do not yet share
		// this module's aggregate lock.
		return mutationGate{availability: MutationAvailability{
			Create: true, Update: true, SetDefault: true,
		}}
	case mutationModeReferenceWritersStopped:
		// This stronger, explicit cutover assertion is required until every
		// reference writer participates in a shared database lock protocol.
		return mutationGate{availability: MutationAvailability{
			Create: true, Update: true, SetDefault: true, Delete: true,
		}}
	default:
		return mutationGate{}
	}
}

func (gate mutationGate) allows(permission string) bool {
	switch permission {
	case PermissionCreate:
		return gate.availability.Create
	case PermissionUpdate:
		return gate.availability.Update
	case PermissionSetDefault:
		return gate.availability.SetDefault
	case PermissionDelete:
		return gate.availability.Delete
	default:
		return false
	}
}
