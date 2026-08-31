package fixedbinding

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestBindingValidationRejectsSchemaForgeryAndTemporarySchemas(t *testing.T) {
	t.Parallel()

	valid := Binding{CoreSchema: "r1_control_core", SharedSchema: "public"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid binding: %v", err)
	}

	for name, candidate := range map[string]Binding{
		"empty":             {},
		"SQL fragment":      {CoreSchema: "core", SharedSchema: `shared;drop schema public`},
		"quoted identifier": {CoreSchema: "core", SharedSchema: `shared\"evil`},
		"uppercase":         {CoreSchema: "Core", SharedSchema: "shared"},
		"same schema":       {CoreSchema: "core", SharedSchema: "core"},
		"temporary core":    {CoreSchema: "compression_test_one", SharedSchema: "shared"},
		"temporary shared":  {CoreSchema: "core", SharedSchema: "compression_test_one"},
	} {
		candidate := candidate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("Validate() error = %v, want ErrInvalidBinding", err)
			}
		})
	}
}

func TestResolverFreezesFirstResult(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	resolver := NewResolver(func() (Binding, error) {
		calls.Add(1)
		return Binding{CoreSchema: "control_core", SharedSchema: "shared_catalog"}, nil
	})
	first, err := resolver.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolver.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || calls.Load() != 1 {
		t.Fatalf("resolver did not freeze: first=%#v second=%#v calls=%d", first, second, calls.Load())
	}
}

func TestResolverFreezesFailure(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	resolver := NewResolver(func() (Binding, error) {
		calls.Add(1)
		return Binding{}, errors.New("unavailable")
	})
	for range 2 {
		if _, err := resolver.Resolve(); err == nil {
			t.Fatal("Resolve() unexpectedly succeeded")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("source calls = %d, want 1", calls.Load())
	}
}
