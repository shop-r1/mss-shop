package memory

import (
	"errors"
	"testing"
)

func TestDriverRejectsNonSimulationMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "development", "production"} {
		if _, err := NewDriver(mode); !errors.Is(err, ErrSimulationOnly) {
			t.Fatalf("mode %q error = %v, want ErrSimulationOnly", mode, err)
		}
	}
	if _, err := NewDriver(ModeSimulation); err != nil {
		t.Fatalf("simulation mode rejected: %v", err)
	}
}
