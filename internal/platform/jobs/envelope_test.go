package jobs

import (
	"errors"
	"testing"
	"time"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

func TestEnvelopeValidation(t *testing.T) {
	t.Parallel()
	valid := Envelope{
		ID:         "message-1",
		TenantID:   tenancy.TenantID("tenant-a"),
		Kind:       "inventory.release",
		Version:    1,
		OccurredAt: time.Now(),
		Payload:    []byte(`{"orderId":"order-1"}`),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}

	invalid := valid
	invalid.Payload = []byte(`{"schema":`)
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("invalid payload error = %v", err)
	}
}

func TestEnvelopeCloneOwnsPayload(t *testing.T) {
	t.Parallel()
	original := Envelope{Payload: []byte(`{"value":1}`)}
	clone := original.Clone()
	clone.Payload[0] = '['
	if original.Payload[0] != '{' {
		t.Fatal("Clone returned a shared payload slice")
	}
}
