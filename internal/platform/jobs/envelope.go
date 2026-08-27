package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

var ErrInvalidEnvelope = errors.New("invalid job envelope")

// Envelope carries a server-issued TenantID outside the untrusted business
// payload. A handler must use TenantID from its dispatch scope, never a tenant
// or schema value embedded in Payload.
type Envelope struct {
	ID         string
	TenantID   tenancy.TenantID
	Kind       string
	Version    int
	OccurredAt time.Time
	Payload    json.RawMessage
}

func (e Envelope) Validate() error {
	if e.ID == "" || e.ID != strings.TrimSpace(e.ID) || len(e.ID) > 128 {
		return fmt.Errorf("%w: id must be a non-empty canonical value", ErrInvalidEnvelope)
	}
	if err := e.TenantID.Validate(); err != nil {
		return fmt.Errorf("%w: tenant id: %v", ErrInvalidEnvelope, err)
	}
	if e.Kind == "" || e.Kind != strings.TrimSpace(e.Kind) || len(e.Kind) > 128 {
		return fmt.Errorf("%w: kind must be a non-empty canonical value", ErrInvalidEnvelope)
	}
	if e.Version < 1 {
		return fmt.Errorf("%w: version must be positive", ErrInvalidEnvelope)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred time is required", ErrInvalidEnvelope)
	}
	if len(e.Payload) == 0 || !json.Valid(e.Payload) {
		return fmt.Errorf("%w: payload must be valid JSON", ErrInvalidEnvelope)
	}
	return nil
}

func (e Envelope) Clone() Envelope {
	e.Payload = append(json.RawMessage(nil), e.Payload...)
	return e
}
