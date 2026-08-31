// Package fixedbinding owns the immutable, server-issued identity and schema
// binding for one mall runtime. No HTTP input participates in this contract.
package fixedbinding

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

const (
	MSS137AdminTenantID       = "default"
	TenantIDEnvironment       = "R1SHOP_TENANT_ID"
	AdminTenantIDEnvironment  = "R1SHOP_ADMIN_TENANT_ID"
	LegacyTenantIDEnvironment = "R1SHOP_LEGACY_TENANT_ID"
	BusinessSchemaEnvironment = "R1SHOP_BIZ_SCHEMA"
	SharedSchemaEnvironment   = "R1SHOP_SHARED_SCHEMA"
)

var (
	ErrInvalidBinding = errors.New("invalid fixed mall binding")
	schemaIdentifier  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

// Binding is fixed for the complete process lifetime. TenantID is the new
// control-plane identity; AdminTenantID is the MSS core principal scope;
// LegacyTenantID is the value preserved in old business rows. These identities
// are intentionally distinct and cannot be selected by a request.
type Binding struct {
	TenantID       string
	AdminTenantID  string
	LegacyTenantID string
	BusinessSchema string
	SharedSchema   string
}

// Validate rejects values that cannot be safely used by the qualified-table
// repository. Schema identifiers are never accepted as SQL fragments.
func (binding Binding) Validate() error {
	if err := validateIdentity("tenant ID", binding.TenantID); err != nil {
		return err
	}
	if err := validateIdentity("Admin tenant ID", binding.AdminTenantID); err != nil {
		return err
	}
	if binding.AdminTenantID != MSS137AdminTenantID {
		return fmt.Errorf("%w: Admin tenant ID must be %q for MSS 1.3.7", ErrInvalidBinding, MSS137AdminTenantID)
	}
	if err := validateIdentity("legacy tenant ID", binding.LegacyTenantID); err != nil {
		return err
	}
	if err := validateSchema("business", binding.BusinessSchema); err != nil {
		return err
	}
	if err := validateSchema("shared", binding.SharedSchema); err != nil {
		return err
	}
	if binding.BusinessSchema == binding.SharedSchema {
		return fmt.Errorf("%w: business and shared schemas must be distinct", ErrInvalidBinding)
	}
	return nil
}

func validateSchema(label, value string) error {
	if !schemaIdentifier.MatchString(value) {
		return fmt.Errorf("%w: %s schema must be a canonical PostgreSQL identifier", ErrInvalidBinding, label)
	}
	if strings.HasPrefix(value, "compression_test_") {
		return fmt.Errorf("%w: %s schema is a temporary compression-test schema", ErrInvalidBinding, label)
	}
	return nil
}

func validateIdentity(label, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 128 {
		return fmt.Errorf("%w: %s must be a non-empty canonical value", ErrInvalidBinding, label)
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("%w: %s contains unsupported characters", ErrInvalidBinding, label)
		}
	}
	return nil
}

// Source resolves one binding. Production uses EnvironmentSource; tests can
// inject a static source without changing process environment.
type Source func() (Binding, error)

// EnvironmentSource reads only the five documented server-side variables.
func EnvironmentSource() (Binding, error) {
	binding := Binding{
		TenantID:       os.Getenv(TenantIDEnvironment),
		AdminTenantID:  os.Getenv(AdminTenantIDEnvironment),
		LegacyTenantID: os.Getenv(LegacyTenantIDEnvironment),
		BusinessSchema: os.Getenv(BusinessSchemaEnvironment),
		SharedSchema:   os.Getenv(SharedSchemaEnvironment),
	}
	if err := binding.Validate(); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

// StaticSource returns a source whose value is still validated at resolution.
func StaticSource(binding Binding) Source {
	return func() (Binding, error) {
		if err := binding.Validate(); err != nil {
			return Binding{}, err
		}
		return binding, nil
	}
}

// Resolver resolves at most once and caches both success and failure. This
// prevents config reloads from changing the selected tenant or schema.
type Resolver struct {
	once    sync.Once
	source  Source
	binding Binding
	err     error
}

func NewResolver(source Source) *Resolver {
	return &Resolver{source: source}
}

func (resolver *Resolver) Resolve() (Binding, error) {
	if resolver == nil {
		return Binding{}, fmt.Errorf("%w: resolver is required", ErrInvalidBinding)
	}
	resolver.once.Do(func() {
		if resolver.source == nil {
			resolver.err = fmt.Errorf("%w: source is required", ErrInvalidBinding)
			return
		}
		resolver.binding, resolver.err = resolver.source()
		if resolver.err == nil {
			resolver.err = resolver.binding.Validate()
		}
	})
	if resolver.err != nil {
		return Binding{}, resolver.err
	}
	return resolver.binding, nil
}
