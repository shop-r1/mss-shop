package importer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/config"
)

func TestBuildSourceConnectionConfigIsFixedPlaintextAndReadOnly(t *testing.T) {
	endpoint := config.Endpoint{
		DSN: "postgres://reader:source-secret@" + sourceHost + ":5432/" + sourceDatabase + "?sslmode=disable",
	}
	parsed, err := buildConnectionConfig(endpoint, true)
	if err != nil {
		t.Fatalf("buildConnectionConfig() error = %v", err)
	}
	if parsed.TLSConfig != nil || len(parsed.Fallbacks) != 0 || parsed.Database != sourceDatabase {
		t.Fatal("source immutable plaintext exception or fixed database boundary drifted")
	}
	for key, want := range map[string]string{
		"default_transaction_read_only":   "on",
		"event_triggers":                  "false",
		"enable_indexscan":                "off",
		"enable_bitmapscan":               "off",
		"enable_indexonlyscan":            "off",
		"enable_parallel_append":          "off",
		"enable_parallel_hash":            "off",
		"max_parallel_workers_per_gather": "0",
	} {
		if parsed.RuntimeParams[key] != want {
			t.Fatalf("runtime parameter %s = %q, want %q", key, parsed.RuntimeParams[key], want)
		}
	}
}

func TestBuildTargetConnectionConfigIsVerifyFull(t *testing.T) {
	endpoint := config.Endpoint{
		DSN:        "postgres://owner:target-secret@" + targetHost + ":5432/" + targetDatabase,
		CAFile:     writeTestCA(t),
		ServerName: targetHost,
	}
	parsed, err := buildConnectionConfig(endpoint, false)
	if err != nil {
		t.Fatalf("buildConnectionConfig() error = %v", err)
	}
	if parsed.TLSConfig == nil || parsed.TLSConfig.InsecureSkipVerify ||
		parsed.TLSConfig.ServerName != targetHost || len(parsed.Fallbacks) != 0 {
		t.Fatal("target TLS configuration is not verify-full without fallback")
	}
}

func TestBuildSourceConnectionConfigRejectsAnyBoundaryOverride(t *testing.T) {
	tests := []string{
		"postgres://reader:secret@" + sourceHost + ":5432/" + sourceDatabase,
		"postgres://reader:secret@" + sourceHost + ":5432/" + sourceDatabase + "?sslmode=require",
		"postgres://reader:secret@" + sourceHost + ":5432/r1shop?sslmode=disable",
		"postgres://reader:secret@other.database.svc:5432/" + sourceDatabase + "?sslmode=disable",
		"postgres://reader:secret@" + sourceHost + ":5433/" + sourceDatabase + "?sslmode=disable",
	}
	for _, dsn := range tests {
		if _, err := buildConnectionConfig(config.Endpoint{DSN: dsn}, true); err == nil {
			t.Fatal("buildConnectionConfig() accepted a source boundary override")
		}
	}
}

func TestBuildConnectionConfigRejectsProdAndDSNOptionsWithoutLeakingSecrets(t *testing.T) {
	const secret = "never-print-this"
	endpoint := config.Endpoint{
		DSN: "postgres://reader:" + secret + "@timescaledb-r1shop-prod.r1shop-prod.svc:5432/r1shop_prod?sslmode=disable",
	}
	_, err := buildConnectionConfig(endpoint, true)
	if err == nil {
		t.Fatal("buildConnectionConfig() accepted production endpoint")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("buildConnectionConfig() leaked secret: %v", err)
	}
}

func writeTestCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.crt")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
