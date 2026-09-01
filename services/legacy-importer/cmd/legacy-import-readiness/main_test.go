package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidatePostgresDSNRejectsOutsideFixedBoundary(t *testing.T) {
	t.Parallel()
	const caPath = "/etc/mss-shop/postgres-tls/ca.crt"
	valid := "postgres://ready:secret@mss-shop-postgres.mss-shop-dev.svc:5432/mss_shop_dev?sslmode=verify-full&sslrootcert=%2Fetc%2Fmss-shop%2Fpostgres-tls%2Fca.crt"
	if err := validatePostgresDSN(valid, &tls.Config{ServerName: postgresHost}, caPath); err != nil {
		t.Fatalf("valid endpoint rejected: %v", err)
	}
	for _, value := range []string{strings.Replace(valid, postgresHost, "r1shop-prod", 1), strings.Replace(valid, "sslmode=verify-full", "sslmode=disable", 1), strings.Replace(valid, "mss_shop_dev", "postgres", 1), "postgres://ready@" + postgresHost + ":5432/mss_shop_dev"} {
		if err := validatePostgresDSN(value, &tls.Config{ServerName: postgresHost}, caPath); err == nil {
			t.Fatalf("unsafe endpoint accepted: %q", value)
		}
	}
}

func TestWriteJSONIsStrictAndSecretFree(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := writeJSON(&output, result{Version: "mss-shop-disposable-readiness-failure/v1", Ready: false, Failure: "credentials"}); err != nil {
		t.Fatal(err)
	}
	var decoded result
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if decoded.Version == "mss-shop-disposable-readiness/v1" || decoded.Failure != "credentials" || strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "postgres://") {
		t.Fatalf("unsafe readiness JSON: %s", output.String())
	}
}

func TestSafeFailureDoesNotEchoSecrets(t *testing.T) {
	t.Parallel()
	if got := safeFailure(assertError("postgres://user:super-secret@host")); got != "postgres" {
		t.Fatalf("failure = %q", got)
	}
}

func TestReadinessRejectsZeroRevisionAndImageDigest(t *testing.T) {
	t.Parallel()
	if nonZeroRevision(strings.Repeat("0", 40)) || !nonZeroRevision("a"+strings.Repeat("0", 39)) {
		t.Fatal("revision zero-value validation drifted")
	}
	if nonZeroImageDigest("sha256:"+strings.Repeat("0", 64)) || !nonZeroImageDigest("sha256:"+"a"+strings.Repeat("0", 63)) {
		t.Fatal("image digest zero-value validation drifted")
	}
	if !podUID.MatchString("01234567-89ab-cdef-0123-456789abcdef") || podUID.MatchString("01234567-89ab-cdef") {
		t.Fatal("pod UID validation drifted")
	}
}

func TestPostgresBoundaryRequiresTheExactEmptyMarkedTarget(t *testing.T) {
	t.Parallel()
	if err := validatePostgresBoundary("170006", postgresDatabase, emptyMarker, true, true, 0, 0, []string{"plpgsql"}); err != nil {
		t.Fatalf("exact empty target rejected: %v", err)
	}
	for _, test := range []struct {
		name                    string
		version, database, mark string
		readOnly, ssl           bool
		userSchemas, objects    int64
		extensions              []string
	}{
		{name: "wrong-version", version: "170005", database: postgresDatabase, mark: emptyMarker, readOnly: true, ssl: true, extensions: []string{"plpgsql"}},
		{name: "imported-marker", version: "170006", database: postgresDatabase, mark: "mss-shop-isolated-dev:legacy-import:v1:" + strings.Repeat("a", 64), readOnly: true, ssl: true, extensions: []string{"plpgsql"}},
		{name: "user-schema", version: "170006", database: postgresDatabase, mark: emptyMarker, readOnly: true, ssl: true, userSchemas: 1, extensions: []string{"plpgsql"}},
		{name: "public-object", version: "170006", database: postgresDatabase, mark: emptyMarker, readOnly: true, ssl: true, objects: 1, extensions: []string{"plpgsql"}},
		{name: "extra-extension", version: "170006", database: postgresDatabase, mark: emptyMarker, readOnly: true, ssl: true, extensions: []string{"plpgsql", "uuid-ossp"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validatePostgresBoundary(test.version, test.database, test.mark, test.readOnly, test.ssl, test.userSchemas, test.objects, test.extensions); err == nil {
				t.Fatal("nonempty or foreign PostgreSQL target accepted")
			}
		})
	}
}

type testError string

func (e testError) Error() string    { return string(e) }
func assertError(value string) error { return testError(value) }
