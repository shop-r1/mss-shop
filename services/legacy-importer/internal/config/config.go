// Package config loads the importer configuration without logging secret
// values or falling back to ambient PostgreSQL environment variables.
package config

import (
	"errors"
	"strings"
)

const RequiredConfirmation = "import-read-only-snapshot-without-order-data"

type Endpoint struct {
	DSN        string
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerName string
}

type Config struct {
	Source Endpoint
	Target Endpoint
}

type LookupEnv func(string) (string, bool)

// Load accepts only the explicit importer variables. Passwords remain inside
// DSN strings and are never interpolated into an error.
func Load(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("environment lookup is not configured")
	}
	confirmation, _ := lookup("MSS_LEGACY_IMPORT_CONFIRM")
	if confirmation != RequiredConfirmation {
		return Config{}, errors.New("legacy import confirmation is missing or invalid")
	}

	source, err := loadEndpoint(lookup, "MSS_LEGACY_SOURCE", false)
	if err != nil {
		return Config{}, err
	}
	target, err := loadEndpoint(lookup, "MSS_LEGACY_TARGET", true)
	if err != nil {
		return Config{}, err
	}
	if source.DSN == target.DSN {
		return Config{}, errors.New("legacy source and target endpoints must be different")
	}
	return Config{Source: source, Target: target}, nil
}

func loadEndpoint(lookup LookupEnv, prefix string, requireTLS bool) (Endpoint, error) {
	getRequired := func(suffix string) (string, error) {
		name := prefix + "_" + suffix
		value, exists := lookup(name)
		if !exists || strings.TrimSpace(value) == "" {
			return "", errors.New(name + " is required")
		}
		if strings.IndexByte(value, 0) >= 0 {
			return "", errors.New(name + " is invalid")
		}
		return value, nil
	}
	dsn, err := getRequired("DSN")
	if err != nil {
		return Endpoint{}, err
	}
	caFile := ""
	serverName := ""
	if requireTLS {
		caFile, err = getRequired("TLS_CA_FILE")
		if err != nil {
			return Endpoint{}, err
		}
		serverName, err = getRequired("TLS_SERVER_NAME")
		if err != nil {
			return Endpoint{}, err
		}
	} else {
		for _, suffix := range []string{"TLS_CA_FILE", "TLS_SERVER_NAME"} {
			if value, exists := lookup(prefix + "_" + suffix); exists && strings.TrimSpace(value) != "" {
				return Endpoint{}, errors.New(prefix + " TLS material is forbidden for the immutable plaintext source")
			}
		}
	}
	certFile, certSet := lookup(prefix + "_TLS_CERT_FILE")
	keyFile, keySet := lookup(prefix + "_TLS_KEY_FILE")
	certSet = certSet && strings.TrimSpace(certFile) != ""
	keySet = keySet && strings.TrimSpace(keyFile) != ""
	if !requireTLS && (certSet || keySet) {
		return Endpoint{}, errors.New(prefix + " TLS material is forbidden for the immutable plaintext source")
	}
	if certSet != keySet {
		return Endpoint{}, errors.New(prefix + " client certificate and key must be configured together")
	}
	return Endpoint{
		DSN:        dsn,
		CAFile:     caFile,
		CertFile:   strings.TrimSpace(certFile),
		KeyFile:    strings.TrimSpace(keyFile),
		ServerName: strings.TrimSpace(serverName),
	}, nil
}
