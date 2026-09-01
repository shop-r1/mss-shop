package importer

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/config"
)

const (
	sourceHost     = "timescaledb-r1shop-dev.database.svc"
	sourceDatabase = "r1shop_dev"
	targetHost     = "mss-shop-postgres.mss-shop-dev.svc"
	databasePort   = "5432"
)

func buildConnectionConfig(endpoint config.Endpoint, source bool) (*pgx.ConnConfig, error) {
	expectedHost := targetHost
	if source {
		expectedHost = sourceHost
	}
	expectedDatabase := targetDatabase
	if source {
		expectedDatabase = sourceDatabase
	}
	parsedURL, err := url.Parse(endpoint.DSN)
	if err != nil || parsedURL == nil {
		return nil, errors.New("database endpoint is outside the compiled import boundary")
	}
	password := ""
	passwordSet := false
	if parsedURL.User != nil {
		password, passwordSet = parsedURL.User.Password()
	}
	if (parsedURL.Scheme != "postgres" && parsedURL.Scheme != "postgresql") ||
		parsedURL.User == nil || parsedURL.User.Username() == "" || !passwordSet || password == "" ||
		parsedURL.Hostname() != expectedHost || parsedURL.Port() != databasePort ||
		parsedURL.Path != "/"+expectedDatabase || parsedURL.Fragment != "" ||
		net.ParseIP(parsedURL.Hostname()) != nil ||
		(source && parsedURL.RawQuery != "sslmode=disable") ||
		(!source && parsedURL.RawQuery != "") {
		return nil, errors.New("database endpoint is outside the compiled import boundary")
	}

	connectionConfig, err := pgx.ParseConfig(endpoint.DSN)
	if err != nil {
		return nil, errors.New("parse database endpoint failed")
	}
	if connectionConfig.Host != expectedHost || strconv.Itoa(int(connectionConfig.Port)) != databasePort ||
		connectionConfig.Database != expectedDatabase || connectionConfig.User == "" {
		return nil, errors.New("database endpoint is outside the compiled import boundary")
	}
	if source {
		if endpoint.CAFile != "" || endpoint.CertFile != "" || endpoint.KeyFile != "" ||
			endpoint.ServerName != "" {
			return nil, errors.New("legacy source TLS configuration is forbidden")
		}
		connectionConfig.TLSConfig = nil
		connectionConfig.Fallbacks = nil
	} else {
		if endpoint.ServerName != expectedHost {
			return nil, errors.New("database TLS server name is outside the compiled import boundary")
		}
		caPEM, readErr := os.ReadFile(endpoint.CAFile)
		if readErr != nil {
			return nil, errors.New("read database TLS CA failed")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("database TLS CA is invalid")
		}
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
			ServerName: endpoint.ServerName,
		}
		if endpoint.CertFile != "" || endpoint.KeyFile != "" {
			if endpoint.CertFile == "" || endpoint.KeyFile == "" {
				return nil, errors.New("database TLS client certificate pair is incomplete")
			}
			certificate, loadErr := tls.LoadX509KeyPair(endpoint.CertFile, endpoint.KeyFile)
			if loadErr != nil {
				return nil, errors.New("database TLS client certificate pair is invalid")
			}
			tlsConfig.Certificates = []tls.Certificate{certificate}
		}
		connectionConfig.TLSConfig = tlsConfig
		connectionConfig.Fallbacks = nil
	}

	connectionConfig.ConnectTimeout = 15 * time.Second
	connectionConfig.RuntimeParams = map[string]string{
		"application_name": "mss-shop-legacy-importer-target",
		"event_triggers":   "false",
		"search_path":      "pg_catalog",
	}
	if source {
		connectionConfig.RuntimeParams = map[string]string{
			"application_name":                "mss-shop-legacy-importer-source",
			"default_transaction_read_only":   "on",
			"event_triggers":                  "false",
			"search_path":                     "pg_catalog",
			"enable_indexscan":                "off",
			"enable_bitmapscan":               "off",
			"enable_indexonlyscan":            "off",
			"enable_parallel_append":          "off",
			"enable_parallel_hash":            "off",
			"max_parallel_workers_per_gather": "0",
		}
	}
	return connectionConfig, nil
}
