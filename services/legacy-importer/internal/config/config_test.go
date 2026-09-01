package config

import (
	"strings"
	"testing"
)

func TestLoadRequiresExplicitConfirmationAndTLS(t *testing.T) {
	values := validEnvironment()
	delete(values, "MSS_LEGACY_IMPORT_CONFIRM")
	_, err := Load(mapLookup(values))
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("Load() error = %v", err)
	}

	values = validEnvironment()
	delete(values, "MSS_LEGACY_TARGET_TLS_CA_FILE")
	_, err = Load(mapLookup(values))
	if err == nil || err.Error() != "MSS_LEGACY_TARGET_TLS_CA_FILE is required" {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadNeverReturnsSecretInErrors(t *testing.T) {
	const secret = "do-not-leak-this-password"
	values := validEnvironment()
	values["MSS_LEGACY_SOURCE_DSN"] = "postgres://reader:" + secret + "@source/r1shop?sslmode=disable"
	values["MSS_LEGACY_TARGET_DSN"] = values["MSS_LEGACY_SOURCE_DSN"]
	_, err := Load(mapLookup(values))
	if err == nil {
		t.Fatal("Load() succeeded for identical endpoints")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Load() leaked a secret: %v", err)
	}
}

func TestLoadRequiresCertificatePair(t *testing.T) {
	values := validEnvironment()
	values["MSS_LEGACY_TARGET_TLS_CERT_FILE"] = "/tls/client.crt"
	_, err := Load(mapLookup(values))
	if err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRejectsSourceTLSMaterial(t *testing.T) {
	values := validEnvironment()
	values["MSS_LEGACY_SOURCE_TLS_CA_FILE"] = "/source/ca.crt"
	_, err := Load(mapLookup(values))
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("Load() error = %v", err)
	}
}

func validEnvironment() map[string]string {
	return map[string]string{
		"MSS_LEGACY_IMPORT_CONFIRM":         RequiredConfirmation,
		"MSS_LEGACY_SOURCE_DSN":             "postgres://reader:source-secret@source/r1shop?sslmode=disable",
		"MSS_LEGACY_TARGET_DSN":             "postgres://owner:target-secret@target/mss_shop_dev",
		"MSS_LEGACY_TARGET_TLS_CA_FILE":     "/target/ca.crt",
		"MSS_LEGACY_TARGET_TLS_SERVER_NAME": "target.database.svc",
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}
