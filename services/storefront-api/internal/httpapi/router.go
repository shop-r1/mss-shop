package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/shop-r1/mss-shop/services/storefront-api/internal/config"
	"github.com/shop-r1/mss-shop/services/storefront-api/internal/locale"
	tenantdirectory "github.com/shop-r1/mss-shop/services/storefront-api/internal/tenant"
)

const (
	platformH5       = "h5"
	platformWeChat   = "mp-weixin"
	bootstrapVersion = 1
)

type API struct {
	directory      *tenantdirectory.Directory
	allowedOrigins map[string]struct{}
}

func New(cfg config.Config) (http.Handler, error) {
	directory, err := tenantdirectory.NewDirectory(cfg.Tenants)
	if err != nil {
		return nil, err
	}
	api := &API{
		directory:      directory,
		allowedOrigins: make(map[string]struct{}, len(cfg.AllowedOrigins)),
	}
	for _, origin := range cfg.AllowedOrigins {
		api.allowedOrigins[origin] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("GET /readyz", api.health)
	mux.HandleFunc("GET /app/v1/bootstrap", api.bootstrap)
	mux.HandleFunc("OPTIONS /app/v1/bootstrap", api.preflight)
	return api.withCORS(mux), nil
}

func (api *API) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}

func (api *API) bootstrap(response http.ResponseWriter, request *http.Request) {
	requestID := newRequestID()
	platform := strings.ToLower(strings.TrimSpace(request.Header.Get("X-R1-Client-Platform")))
	if platform == "" {
		platform = platformH5
	}

	var (
		tenant config.TenantConfig
		err    error
	)
	switch platform {
	case platformH5:
		tenant, err = api.directory.ByHost(request.Host)
	case platformWeChat:
		appID := strings.TrimSpace(request.Header.Get("X-R1-Client-App-Id"))
		if appID == "" || utf8.RuneCountInString(appID) > 64 {
			writeProblem(response, http.StatusBadRequest, "INVALID_CHANNEL", "error.invalid_channel", requestID)
			return
		}
		tenant, err = api.directory.ByWechatAppID(appID)
	default:
		writeProblem(response, http.StatusBadRequest, "INVALID_CHANNEL", "error.invalid_channel", requestID)
		return
	}
	if errors.Is(err, tenantdirectory.ErrNotFound) {
		writeProblem(response, http.StatusNotFound, "TENANT_NOT_FOUND", "error.tenant_not_found", requestID)
		return
	}
	if err != nil {
		writeProblem(response, http.StatusInternalServerError, "INTERNAL_ERROR", "error.internal", requestID)
		return
	}
	if tenant.State != config.TenantActive {
		writeProblem(response, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "error.tenant_unavailable", requestID)
		return
	}

	selectedLocale := locale.Negotiate(
		request.Header.Get("Accept-Language"),
		tenant.Regional.EnabledLocales,
		tenant.Regional.DefaultLocale,
	)
	displayName := tenant.DisplayNames[selectedLocale]
	if displayName == "" {
		displayName = tenant.DisplayNames[tenant.Regional.FallbackLocale]
	}

	var appID *string
	if platform == platformWeChat {
		value := strings.TrimSpace(request.Header.Get("X-R1-Client-App-Id"))
		appID = &value
	}

	payload := BootstrapResponse{
		Data: BootstrapData{
			ContractVersion:      bootstrapVersion,
			ConfigurationVersion: tenant.ConfigurationVersion,
			Tenant: PublicTenant{
				PublicID:    tenant.PublicID,
				DisplayName: displayName,
			},
			Branding: Branding{
				LogoURL:              optionalString(tenant.Branding.LogoURL),
				PrimaryColor:         tenant.Branding.PrimaryColor,
				CustomerServiceQRURL: optionalString(tenant.Branding.CustomerServiceQRURL),
			},
			Regional: Regional{
				Locale:            selectedLocale,
				EnabledLocales:    tenant.Regional.EnabledLocales,
				FallbackLocale:    tenant.Regional.FallbackLocale,
				Currency:          tenant.Regional.DefaultCurrency,
				EnabledCurrencies: tenant.Regional.EnabledCurrencies,
				TimeZone:          tenant.Regional.TimeZone,
				Direction:         "ltr",
			},
			Channel:  Channel{Type: platform, AppID: appID},
			Features: Features{PopupEnabled: tenant.Features.PopupEnabled},
		},
		Meta: ResponseMeta{RequestID: requestID},
	}

	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Language", selectedLocale)
	response.Header().Set("ETag", strconv.Quote(tenant.ConfigurationVersion))
	appendVary(response.Header(), "Accept-Language")
	appendVary(response.Header(), "X-R1-Client-Platform")
	appendVary(response.Header(), "X-R1-Client-App-Id")
	writeJSON(response, http.StatusOK, "application/json", payload)
}

func (api *API) preflight(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusNoContent)
}

func (api *API) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if _, allowed := api.allowedOrigins[origin]; allowed && origin != "" {
			response.Header().Set("Access-Control-Allow-Origin", origin)
			response.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			response.Header().Set("Access-Control-Allow-Headers", "Accept-Language, Content-Type, X-R1-Client-App-Id, X-R1-Client-Platform")
			appendVary(response.Header(), "Origin")
		}
		next.ServeHTTP(response, request)
	})
}

type BootstrapResponse struct {
	Data BootstrapData `json:"data"`
	Meta ResponseMeta  `json:"meta"`
}

type BootstrapData struct {
	ContractVersion      int          `json:"contract_version"`
	ConfigurationVersion string       `json:"configuration_version"`
	Tenant               PublicTenant `json:"tenant"`
	Branding             Branding     `json:"branding"`
	Regional             Regional     `json:"regional"`
	Channel              Channel      `json:"channel"`
	Features             Features     `json:"features"`
}

type PublicTenant struct {
	PublicID    string `json:"public_id"`
	DisplayName string `json:"display_name"`
}

type Branding struct {
	LogoURL              *string `json:"logo_url"`
	PrimaryColor         string  `json:"primary_color"`
	CustomerServiceQRURL *string `json:"customer_service_qr_url"`
}

type Regional struct {
	Locale            string   `json:"locale"`
	EnabledLocales    []string `json:"enabled_locales"`
	FallbackLocale    string   `json:"fallback_locale"`
	Currency          string   `json:"currency"`
	EnabledCurrencies []string `json:"enabled_currencies"`
	TimeZone          string   `json:"time_zone"`
	Direction         string   `json:"direction"`
}

type Channel struct {
	Type  string  `json:"type"`
	AppID *string `json:"app_id"`
}

type Features struct {
	PopupEnabled bool `json:"popup_enabled"`
}

type ResponseMeta struct {
	RequestID string `json:"request_id"`
}

type Problem struct {
	Type       string            `json:"type"`
	Status     int               `json:"status"`
	Code       string            `json:"code"`
	MessageKey string            `json:"message_key"`
	Arguments  map[string]string `json:"args"`
	RequestID  string            `json:"request_id"`
}

func writeProblem(response http.ResponseWriter, status int, code, messageKey, requestID string) {
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, status, "application/problem+json", Problem{
		Type:       "about:blank",
		Status:     status,
		Code:       code,
		MessageKey: messageKey,
		Arguments:  map[string]string{},
		RequestID:  requestID,
	})
}

func writeJSON(response http.ResponseWriter, status int, contentType string, value any) {
	response.Header().Set("Content-Type", contentType)
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func newRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(buffer)
}

func appendVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
