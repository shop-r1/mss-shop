package fixedbinding

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestBindingValidation(t *testing.T) {
	t.Parallel()

	valid := Binding{
		TenantID:       "2f6433e3-9b01-4a71-8bf6-a88070a2fa97",
		AdminTenantID:  "default",
		LegacyTenantID: "158815467563520321",
		BusinessSchema: "r1_m_2f6433e3_biz",
		SharedSchema:   "r1_shared",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid binding: %v", err)
	}

	cases := []Binding{
		{},
		{TenantID: " tenant", AdminTenantID: "default", LegacyTenantID: "legacy", BusinessSchema: "biz", SharedSchema: "shared"},
		{TenantID: "tenant", LegacyTenantID: "legacy", BusinessSchema: "biz", SharedSchema: "shared"},
		{TenantID: "tenant", AdminTenantID: "another", LegacyTenantID: "legacy", BusinessSchema: "biz", SharedSchema: "shared"},
		{TenantID: "tenant", AdminTenantID: "default", LegacyTenantID: "legacy", BusinessSchema: `biz;drop schema public`, SharedSchema: "shared"},
		{TenantID: "tenant", AdminTenantID: "default", LegacyTenantID: "legacy", BusinessSchema: "Biz", SharedSchema: "shared"},
		{TenantID: "tenant", AdminTenantID: "default", LegacyTenantID: "legacy", BusinessSchema: "same", SharedSchema: "same"},
		{TenantID: "tenant", AdminTenantID: "default", LegacyTenantID: "legacy", BusinessSchema: "compression_test_mall", SharedSchema: "shared"},
		{TenantID: "tenant", AdminTenantID: "default", LegacyTenantID: "legacy", BusinessSchema: "biz", SharedSchema: "compression_test_shared"},
	}
	for index, candidate := range cases {
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidBinding) {
			t.Errorf("case %d error = %v, want ErrInvalidBinding", index, err)
		}
	}
}

func TestResolverFreezesFirstResult(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	resolver := NewResolver(func() (Binding, error) {
		calls.Add(1)
		return Binding{
			TenantID:       "tenant-one",
			AdminTenantID:  "default",
			LegacyTenantID: "legacy-one",
			BusinessSchema: "mall_biz",
			SharedSchema:   "mall_shared",
		}, nil
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

func TestEnvironmentSourceKeepsThreeTenantIdentitiesSeparate(t *testing.T) {
	t.Setenv(TenantIDEnvironment, "control-plane-tenant")
	t.Setenv(AdminTenantIDEnvironment, "default")
	t.Setenv(LegacyTenantIDEnvironment, "158815467563520321")
	t.Setenv(BusinessSchemaEnvironment, "mall_biz")
	t.Setenv(SharedSchemaEnvironment, "shared_catalog")

	binding, err := EnvironmentSource()
	if err != nil {
		t.Fatal(err)
	}
	if binding.TenantID != "control-plane-tenant" || binding.AdminTenantID != "default" || binding.LegacyTenantID != "158815467563520321" {
		t.Fatalf("tenant identities were conflated: %#v", binding)
	}

	t.Setenv(AdminTenantIDEnvironment, "")
	if _, err := EnvironmentSource(); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("missing Admin tenant scope error = %v", err)
	}
}
