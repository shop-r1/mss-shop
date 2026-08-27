package tenant

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"golang.org/x/net/idna"

	"github.com/shop-r1/mss-shop/services/storefront-api/internal/config"
)

var ErrNotFound = errors.New("tenant binding not found")

type Directory struct {
	byHost  map[string]*config.TenantConfig
	byAppID map[string]*config.TenantConfig
}

func NewDirectory(tenants []config.TenantConfig) (*Directory, error) {
	directory := &Directory{
		byHost:  make(map[string]*config.TenantConfig),
		byAppID: make(map[string]*config.TenantConfig),
	}

	seenID := make(map[string]struct{}, len(tenants))
	seenPublicID := make(map[string]struct{}, len(tenants))
	for index := range tenants {
		tenant := &tenants[index]
		id := tenant.ID.String()
		if _, exists := seenID[id]; exists {
			return nil, fmt.Errorf("duplicate internal tenant ID %q", id)
		}
		seenID[id] = struct{}{}
		if _, exists := seenPublicID[tenant.PublicID]; exists {
			return nil, fmt.Errorf("duplicate public tenant ID %q", tenant.PublicID)
		}
		seenPublicID[tenant.PublicID] = struct{}{}

		for _, rawHost := range tenant.Hosts {
			host, err := NormalizeHost(rawHost)
			if err != nil {
				return nil, fmt.Errorf("tenant %q Host %q: %w", id, rawHost, err)
			}
			if existing, exists := directory.byHost[host]; exists {
				return nil, fmt.Errorf("duplicate Host binding %q for tenants %q and %q", host, existing.ID, id)
			}
			directory.byHost[host] = tenant
		}
		for _, rawAppID := range tenant.WechatAppIDs {
			appID := strings.TrimSpace(rawAppID)
			if appID == "" {
				return nil, fmt.Errorf("tenant %q has an empty WeChat AppID", id)
			}
			if existing, exists := directory.byAppID[appID]; exists {
				return nil, fmt.Errorf("duplicate WeChat AppID binding %q for tenants %q and %q", appID, existing.ID, id)
			}
			directory.byAppID[appID] = tenant
		}
	}
	return directory, nil
}

func (directory *Directory) ByHost(rawHost string) (config.TenantConfig, error) {
	host, err := NormalizeHost(rawHost)
	if err != nil {
		return config.TenantConfig{}, ErrNotFound
	}
	tenant, exists := directory.byHost[host]
	if !exists {
		return config.TenantConfig{}, ErrNotFound
	}
	return *tenant, nil
}

func (directory *Directory) ByWechatAppID(rawAppID string) (config.TenantConfig, error) {
	tenant, exists := directory.byAppID[strings.TrimSpace(rawAppID)]
	if !exists {
		return config.TenantConfig{}, ErrNotFound
	}
	return *tenant, nil
}

func NormalizeHost(rawHost string) (string, error) {
	host := strings.TrimSpace(rawHost)
	if host == "" {
		return "", errors.New("Host is empty")
	}

	if parsedHost, port, err := net.SplitHostPort(host); err == nil {
		portNumber, conversionErr := strconv.Atoi(port)
		if conversionErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("Host contains an invalid port")
		}
		host = parsedHost
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	} else if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", errors.New("Host contains an invalid port")
	}

	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || strings.ContainsAny(host, "/\\@") {
		return "", errors.New("Host is invalid")
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", fmt.Errorf("convert Host to IDNA ASCII: %w", err)
	}
	if ascii == "" {
		return "", errors.New("Host is invalid")
	}
	return strings.ToLower(ascii), nil
}
