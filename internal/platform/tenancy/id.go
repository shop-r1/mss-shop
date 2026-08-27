package tenancy

import (
	"errors"
	"fmt"
	"strings"
)

// TenantID is the immutable, server-issued identity used across platform
// services. It is deliberately distinct from mutable tenant names, domains,
// AppIDs, and database schema names.
type TenantID string

var ErrInvalidTenantID = errors.New("invalid tenant id")

func ParseTenantID(value string) (TenantID, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 128 {
		return "", fmt.Errorf("%w: expected a non-empty canonical value", ErrInvalidTenantID)
	}
	return TenantID(value), nil
}

func (id TenantID) String() string {
	return string(id)
}

func (id TenantID) Validate() error {
	_, err := ParseTenantID(string(id))
	return err
}
