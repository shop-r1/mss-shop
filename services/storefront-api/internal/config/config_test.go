package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExample(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join("..", "..", "..", "..", "config", "examples", "storefront-tenants.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Tenants) != 1 || cfg.Tenants[0].PublicID != "demo-shop" {
		t.Fatalf("unexpected example config: %#v", cfg)
	}
}

func TestLoadRejectsUnknownSecretBearingFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{
  "listenAddress":"127.0.0.1:8090",
  "allowedOrigins":[],
  "tenants":[{"id":"tenant-demo","schema":"forbidden"}]
}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field \"schema\"") {
		t.Fatalf("Load() error = %v, want strict unknown-field error", err)
	}
}

func TestValidateRejectsValuesOutsideBootstrapContract(t *testing.T) {
	t.Parallel()

	valid := validContractTenant()
	tests := map[string]func(*TenantConfig){
		"configuration version length": func(tenant *TenantConfig) {
			tenant.ConfigurationVersion = strings.Repeat("v", 129)
		},
		"AppID length": func(tenant *TenantConfig) {
			tenant.WechatAppIDs = []string{strings.Repeat("w", 65)}
		},
		"display name length": func(tenant *TenantConfig) {
			tenant.DisplayNames["zh-CN"] = strings.Repeat("店", 201)
		},
		"duplicate locale": func(tenant *TenantConfig) {
			tenant.Regional.EnabledLocales = []string{"zh-CN", "zh-CN"}
		},
		"duplicate currency": func(tenant *TenantConfig) {
			tenant.Regional.EnabledCurrencies = []string{"AUD", "AUD"}
		},
	}

	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			candidate.DisplayNames = map[string]string{
				"zh-CN": valid.DisplayNames["zh-CN"],
				"en-US": valid.DisplayNames["en-US"],
			}
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
		})
	}
}

func validContractTenant() TenantConfig {
	return TenantConfig{
		ID:                   "tenant-one",
		PublicID:             "tenant-one",
		State:                TenantActive,
		ConfigurationVersion: "version-1",
		Hosts:                []string{"shop.example.com"},
		WechatAppIDs:         []string{"wx0123456789abcdef"},
		DisplayNames: map[string]string{
			"zh-CN": "示例商城",
			"en-US": "Example Shop",
		},
		Branding: Branding{PrimaryColor: "#1677FF"},
		Regional: Regional{
			EnabledLocales:    []string{"zh-CN", "en-US"},
			DefaultLocale:     "zh-CN",
			FallbackLocale:    "en-US",
			EnabledCurrencies: []string{"AUD", "CNY"},
			DefaultCurrency:   "AUD",
			TimeZone:          "Australia/Sydney",
		},
	}
}
