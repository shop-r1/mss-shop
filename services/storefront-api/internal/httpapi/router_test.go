package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shop-r1/mss-shop/internal/platform/tenancy"
	"github.com/shop-r1/mss-shop/services/storefront-api/internal/config"
)

func TestBootstrapResolvesExactHostAndNegotiatesLocale(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, activeTenant())
	request := httptest.NewRequest(http.MethodGet, "http://example.invalid/app/v1/bootstrap", nil)
	request.Host = "SHOP.EXAMPLE.COM:443"
	request.Header.Set("Accept-Language", "zh-CN;q=0.4,en-US;q=0.9")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Language"); got != "en-US" {
		t.Fatalf("Content-Language = %q, want en-US", got)
	}
	var payload BootstrapResponse
	decodeResponse(t, response, &payload)
	if payload.Data.Tenant.DisplayName != "Example Shop" {
		t.Fatalf("display_name = %q", payload.Data.Tenant.DisplayName)
	}
	if payload.Data.Tenant.PublicID != "example-shop" {
		t.Fatalf("public_id = %q", payload.Data.Tenant.PublicID)
	}
	assertNoInternalData(t, response.Body.String())
}

func TestBootstrapDoesNotGuessTenantFromLegacyInputs(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, activeTenant())
	request := httptest.NewRequest(http.MethodGet, "http://unknown.example/app/v1/bootstrap?tenant_id=tenant-one&member_id=1", nil)
	request.Host = "mall-shop.example.com"
	request.Header.Set("Accept", "shop.example.com")
	request.Header.Set("Referer", "https://shop.example.com/")
	request.Header.Set("X-Forwarded-Host", "shop.example.com")
	request.Header.Set("X-Tenant-ID", "tenant-one")
	request.Header.Set("X-R1-Client-App-Id", "wx-example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertProblemCode(t, response, "TENANT_NOT_FOUND")
}

func TestBootstrapWeChatUsesRegisteredPublicAppID(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, activeTenant())
	request := httptest.NewRequest(http.MethodGet, "http://shared-api.invalid/app/v1/bootstrap", nil)
	request.Header.Set("X-R1-Client-Platform", "mp-weixin")
	request.Header.Set("X-R1-Client-App-Id", "wx-example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload BootstrapResponse
	decodeResponse(t, response, &payload)
	if payload.Data.Channel.Type != "mp-weixin" || payload.Data.Channel.AppID == nil || *payload.Data.Channel.AppID != "wx-example" {
		t.Fatalf("unexpected channel: %#v", payload.Data.Channel)
	}
}

func TestBootstrapRejectsSuspendedTenant(t *testing.T) {
	t.Parallel()

	tenant := activeTenant()
	tenant.State = config.TenantSuspended
	handler := newTestHandler(t, tenant)
	request := httptest.NewRequest(http.MethodGet, "http://shop.example.com/app/v1/bootstrap", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertProblemCode(t, response, "TENANT_UNAVAILABLE")
}

func TestBootstrapRequiresAppIDForWeChat(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, activeTenant())
	request := httptest.NewRequest(http.MethodGet, "http://shared-api.invalid/app/v1/bootstrap", nil)
	request.Header.Set("X-R1-Client-Platform", "mp-weixin")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertProblemCode(t, response, "INVALID_CHANNEL")
}

func TestBootstrapRejectsOversizedWeChatAppID(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, activeTenant())
	request := httptest.NewRequest(http.MethodGet, "http://shared-api.invalid/app/v1/bootstrap", nil)
	request.Header.Set("X-R1-Client-Platform", "mp-weixin")
	request.Header.Set("X-R1-Client-App-Id", strings.Repeat("微", 65))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertProblemCode(t, response, "INVALID_CHANNEL")
}

func activeTenant() config.TenantConfig {
	return config.TenantConfig{
		ID:                   tenancy.TenantID("tenant-one"),
		PublicID:             "example-shop",
		State:                config.TenantActive,
		ConfigurationVersion: "version-1",
		Hosts:                []string{"shop.example.com"},
		WechatAppIDs:         []string{"wx-example"},
		DisplayNames: map[string]string{
			"zh-CN": "示例商城",
			"en-US": "Example Shop",
		},
		Branding: config.Branding{PrimaryColor: "#1677FF"},
		Regional: config.Regional{
			EnabledLocales:    []string{"zh-CN", "en-US"},
			DefaultLocale:     "zh-CN",
			FallbackLocale:    "en-US",
			EnabledCurrencies: []string{"AUD", "CNY"},
			DefaultCurrency:   "AUD",
			TimeZone:          "Australia/Sydney",
		},
		Features: config.Features{PopupEnabled: false},
	}
}

func newTestHandler(t *testing.T, tenants ...config.TenantConfig) http.Handler {
	t.Helper()
	handler, err := New(config.Config{ListenAddress: "127.0.0.1:0", Tenants: tenants})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func assertProblemCode(t *testing.T, response *httptest.ResponseRecorder, expected string) {
	t.Helper()
	var problem Problem
	decodeResponse(t, response, &problem)
	if problem.Code != expected || problem.Status != response.Code {
		t.Fatalf("problem = %#v, want code %s and status %d", problem, expected, response.Code)
	}
}

func assertNoInternalData(t *testing.T, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, forbidden := range []string{"tenant-one", "schema", "dsn", "database", "secret", "core_schema", "biz_schema"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("response leaked forbidden value %q: %s", forbidden, body)
		}
	}
}
