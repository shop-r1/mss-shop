package tenant

import (
	"strings"
	"testing"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
	"github.com/shop-r1/mss-shop/services/storefront-api/internal/config"
)

func TestNormalizeHost(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Example.COM:443": "example.com",
		"example.com.":    "example.com",
		"BÜCHER.example":  "xn--bcher-kva.example",
		"127.0.0.1:8090":  "127.0.0.1",
	}
	for input, expected := range tests {
		actual, err := NormalizeHost(input)
		if err != nil {
			t.Fatalf("NormalizeHost(%q) error = %v", input, err)
		}
		if actual != expected {
			t.Fatalf("NormalizeHost(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestNormalizeHostRejectsInvalidPorts(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"example.com:http",
		"example.com:0",
		"example.com:65536",
		"example.com:",
	} {
		if _, err := NormalizeHost(input); err == nil {
			t.Fatalf("NormalizeHost(%q) unexpectedly succeeded", input)
		}
	}
}

func TestDirectoryUsesExactBindingsWithoutPrefixGuessing(t *testing.T) {
	t.Parallel()

	directory, err := NewDirectory([]config.TenantConfig{{
		ID: tenancy.TenantID("tenant-one"), PublicID: "tenant-one", Hosts: []string{"shop.example.com"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := directory.ByHost("mall-shop.example.com"); err == nil {
		t.Fatal("ByHost() accepted an unregistered prefixed Host")
	}
}

func TestDirectoryRejectsDuplicateBindings(t *testing.T) {
	t.Parallel()

	_, err := NewDirectory([]config.TenantConfig{
		{ID: tenancy.TenantID("tenant-one"), PublicID: "tenant-one", Hosts: []string{"EXAMPLE.com"}},
		{ID: tenancy.TenantID("tenant-two"), PublicID: "tenant-two", Hosts: []string{"example.com."}},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate Host binding") {
		t.Fatalf("NewDirectory() error = %v, want duplicate binding", err)
	}
}
