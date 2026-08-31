// Package fixedbinding owns the immutable schema binding used by the tenant
// control plane. Values come only from server startup configuration; HTTP
// requests never participate in schema selection.
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
	CoreSchemaEnvironment   = "R1SHOP_CORE_SCHEMA"
	SharedSchemaEnvironment = "R1SHOP_SHARED_SCHEMA"
)

var (
	ErrInvalidBinding = errors.New("invalid fixed tenant-platform binding")
	schemaIdentifier  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

// Binding remains fixed for the process lifetime. CoreSchema owns MSS state;
// SharedSchema owns only the reviewed global R1Shop catalogue.
type Binding struct {
	CoreSchema   string
	SharedSchema string
}

// Validate accepts canonical PostgreSQL identifiers only and keeps temporary
// migration-test schemas outside the compatibility boundary.
func (binding Binding) Validate() error {
	if err := validateSchema("core", binding.CoreSchema); err != nil {
		return err
	}
	if err := validateSchema("shared", binding.SharedSchema); err != nil {
		return err
	}
	if binding.CoreSchema == binding.SharedSchema {
		return fmt.Errorf("%w: core and shared schemas must be distinct", ErrInvalidBinding)
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

// Source resolves one binding. Production uses EnvironmentSource; tests use a
// static source without mutating process-global environment.
type Source func() (Binding, error)

func EnvironmentSource() (Binding, error) {
	binding := Binding{
		CoreSchema:   os.Getenv(CoreSchemaEnvironment),
		SharedSchema: os.Getenv(SharedSchemaEnvironment),
	}
	if err := binding.Validate(); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func StaticSource(binding Binding) Source {
	return func() (Binding, error) {
		if err := binding.Validate(); err != nil {
			return Binding{}, err
		}
		return binding, nil
	}
}

// Resolver caches both success and failure. Configuration reloads therefore
// cannot switch schemas after startup.
type Resolver struct {
	once    sync.Once
	source  Source
	binding Binding
	err     error
}

func NewResolver(source Source) *Resolver { return &Resolver{source: source} }

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
