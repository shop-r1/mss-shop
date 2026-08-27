package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
)

const (
	TenantActive    = "active"
	TenantSuspended = "suspended"
)

var (
	publicIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	colorPattern    = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

type Config struct {
	ListenAddress  string         `json:"listenAddress"`
	AllowedOrigins []string       `json:"allowedOrigins"`
	Tenants        []TenantConfig `json:"tenants"`
}

type TenantConfig struct {
	ID                   tenancy.TenantID  `json:"id"`
	PublicID             string            `json:"publicId"`
	State                string            `json:"state"`
	ConfigurationVersion string            `json:"configurationVersion"`
	Hosts                []string          `json:"hosts"`
	WechatAppIDs         []string          `json:"wechatAppIds"`
	DisplayNames         map[string]string `json:"displayNames"`
	Branding             Branding          `json:"branding"`
	Regional             Regional          `json:"regional"`
	Features             Features          `json:"features"`
}

type Branding struct {
	LogoURL              string `json:"logoUrl"`
	PrimaryColor         string `json:"primaryColor"`
	CustomerServiceQRURL string `json:"customerServiceQrUrl"`
}

type Regional struct {
	EnabledLocales    []string `json:"enabledLocales"`
	DefaultLocale     string   `json:"defaultLocale"`
	FallbackLocale    string   `json:"fallbackLocale"`
	EnabledCurrencies []string `json:"enabledCurrencies"`
	DefaultCurrency   string   `json:"defaultCurrency"`
	TimeZone          string   `json:"timeZone"`
}

type Features struct {
	PopupEnabled bool `json:"popupEnabled"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open storefront config: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode storefront config: %w", err)
	}
	if err := ensureSingleDocument(decoder); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureSingleDocument(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("decode storefront config: multiple JSON documents")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode storefront config trailer: %w", err)
	}
	return nil
}

func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.ListenAddress) == "" {
		return errors.New("validate storefront config: listenAddress is required")
	}
	if len(cfg.Tenants) == 0 {
		return errors.New("validate storefront config: at least one tenant is required")
	}

	for index, origin := range cfg.AllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("validate storefront config: allowedOrigins[%d] must be an origin", index)
		}
	}
	for index, tenant := range cfg.Tenants {
		if err := tenant.Validate(); err != nil {
			return fmt.Errorf("validate storefront config: tenants[%d]: %w", index, err)
		}
	}
	return nil
}

func (tenant TenantConfig) Validate() error {
	if err := tenant.ID.Validate(); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if !publicIDPattern.MatchString(tenant.PublicID) {
		return errors.New("publicId must contain only lower-case letters, numbers, underscore or hyphen")
	}
	if tenant.State != TenantActive && tenant.State != TenantSuspended {
		return errors.New("state must be active or suspended")
	}
	if strings.TrimSpace(tenant.ConfigurationVersion) == "" {
		return errors.New("configurationVersion is required")
	}
	if utf8.RuneCountInString(tenant.ConfigurationVersion) > 128 {
		return errors.New("configurationVersion cannot exceed 128 characters")
	}
	if len(tenant.Hosts) == 0 && len(tenant.WechatAppIDs) == 0 {
		return errors.New("at least one Host or WeChat AppID binding is required")
	}
	for index, appID := range tenant.WechatAppIDs {
		trimmed := strings.TrimSpace(appID)
		if trimmed == "" {
			return fmt.Errorf("wechatAppIds[%d] cannot be empty", index)
		}
		if utf8.RuneCountInString(trimmed) > 64 {
			return fmt.Errorf("wechatAppIds[%d] cannot exceed 64 characters", index)
		}
	}
	if len(tenant.Regional.EnabledLocales) == 0 {
		return errors.New("regional.enabledLocales cannot be empty")
	}
	if duplicate, ok := duplicateValue(tenant.Regional.EnabledLocales); ok {
		return fmt.Errorf("regional.enabledLocales contains duplicate %q", duplicate)
	}
	if !contains(tenant.Regional.EnabledLocales, tenant.Regional.DefaultLocale) {
		return errors.New("regional.defaultLocale must be enabled")
	}
	if !contains(tenant.Regional.EnabledLocales, tenant.Regional.FallbackLocale) {
		return errors.New("regional.fallbackLocale must be enabled")
	}
	for _, locale := range tenant.Regional.EnabledLocales {
		if locale != "zh-CN" && locale != "en-US" {
			return fmt.Errorf("unsupported locale %q", locale)
		}
		if strings.TrimSpace(tenant.DisplayNames[locale]) == "" {
			return fmt.Errorf("displayNames.%s is required", locale)
		}
		if utf8.RuneCountInString(tenant.DisplayNames[locale]) > 200 {
			return fmt.Errorf("displayNames.%s cannot exceed 200 characters", locale)
		}
	}
	if len(tenant.Regional.EnabledCurrencies) == 0 {
		return errors.New("regional.enabledCurrencies cannot be empty")
	}
	if duplicate, ok := duplicateValue(tenant.Regional.EnabledCurrencies); ok {
		return fmt.Errorf("regional.enabledCurrencies contains duplicate %q", duplicate)
	}
	if !contains(tenant.Regional.EnabledCurrencies, tenant.Regional.DefaultCurrency) {
		return errors.New("regional.defaultCurrency must be enabled")
	}
	for _, currency := range tenant.Regional.EnabledCurrencies {
		if !currencyPattern.MatchString(currency) {
			return fmt.Errorf("invalid currency %q", currency)
		}
	}
	if utf8.RuneCountInString(tenant.Regional.TimeZone) > 64 {
		return errors.New("regional.timeZone cannot exceed 64 characters")
	}
	if _, err := time.LoadLocation(tenant.Regional.TimeZone); err != nil {
		return fmt.Errorf("regional.timeZone: %w", err)
	}
	if !colorPattern.MatchString(tenant.Branding.PrimaryColor) {
		return errors.New("branding.primaryColor must be a six-digit hex color")
	}
	for name, rawURL := range map[string]string{
		"branding.logoUrl":              tenant.Branding.LogoURL,
		"branding.customerServiceQrUrl": tenant.Branding.CustomerServiceQRURL,
	} {
		if rawURL == "" {
			continue
		}
		parsed, err := url.ParseRequestURI(rawURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return fmt.Errorf("%s must be an absolute HTTP URL", name)
		}
	}
	return nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func duplicateValue(values []string) (string, bool) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return value, true
		}
		seen[value] = struct{}{}
	}
	return "", false
}
