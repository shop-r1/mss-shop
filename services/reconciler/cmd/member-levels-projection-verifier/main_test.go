package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
)

const (
	testRevision = "0123456789abcdef0123456789abcdef01234567"
	testDigest   = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testReceipt  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testPodUID   = "01234567-89ab-cdef-0123-456789abcdef"
)

func TestRuntimeDSNIsExactAndErrorsNeverDiscloseIt(t *testing.T) {
	t.Parallel()
	dsn := runtimeDSN("runtime-password-that-must-not-appear")
	if err := validateRuntimeDSN(dsn, postgresCAPath); err != nil {
		t.Fatalf("exact runtime DSN rejected: %v", err)
	}
	unsafe := []string{
		strings.Replace(dsn, mallRuntimeRole, "mss_shop_bootstrap", 1),
		strings.Replace(dsn, postgresHost, "timescaledb-r1shop-prod.database.svc", 1),
		strings.Replace(dsn, "/"+postgresDatabase, "/postgres", 1),
		strings.Replace(dsn, "verify-full", "disable", 1),
		strings.Replace(dsn, url.QueryEscape(mallCoreSchema), url.QueryEscape(mallBusinessSchema), 1),
		dsn + "&application_name=foreign",
	}
	for _, candidate := range unsafe {
		if err := validateRuntimeDSN(candidate, postgresCAPath); err == nil {
			t.Fatal("unsafe runtime DSN accepted")
		} else if strings.Contains(err.Error(), "runtime-password-that-must-not-appear") ||
			strings.Contains(err.Error(), candidate) {
			t.Fatal("runtime DSN or password disclosed by validation")
		}
	}
}

func TestBindingsRejectZerosAndRequireRevisionBoundPodName(t *testing.T) {
	t.Parallel()
	validName := projectionVerifierPodPrefix + testRevision[:observedRevisionPrefixLength] + "-abcde"
	if !validRevision(testRevision) || !validDigest(testDigest) || !validReceipt(testReceipt) ||
		!validProjectionPodName(validName, testRevision) || !podUID.MatchString(testPodUID) {
		t.Fatal("valid immutable execution bindings rejected")
	}
	if validRevision(strings.Repeat("0", 40)) ||
		validDigest("sha256:"+strings.Repeat("0", 64)) ||
		validReceipt(strings.Repeat("0", 64)) ||
		validProjectionPodName(projectionVerifierPodPrefix+strings.Repeat("b", 39), testRevision) ||
		podUID.MatchString("01234567-89ab-cdef") {
		t.Fatal("zero, short, or cross-revision execution binding accepted")
	}
}

func TestExpectedProjectionMetricsAreExact(t *testing.T) {
	t.Parallel()
	expected := metrics{
		PublicMemberLevelsRows: 4, BusinessMemberLevelsRows: 4,
		FlaggedDefaultRows: 1, EnabledDefaultRows: 1, DuplicateNameGroups: 0,
	}
	if err := validateExpectedMetrics(expected); err != nil {
		t.Fatalf("exact projection metrics rejected: %v", err)
	}
	for name, mutate := range map[string]func(*metrics){
		"public-count":   func(value *metrics) { value.PublicMemberLevelsRows = 3 },
		"business-count": func(value *metrics) { value.BusinessMemberLevelsRows = 5 },
		"difference":     func(value *metrics) { value.DifferenceRows = 1 },
		"cross-tenant":   func(value *metrics) { value.CrossTenantRows = 1 },
		"flagged":        func(value *metrics) { value.FlaggedDefaultRows = 0 },
		"enabled":        func(value *metrics) { value.EnabledDefaultRows = 0 },
		"invalid":        func(value *metrics) { value.InvalidDefaultRows = 1 },
		"duplicate-name": func(value *metrics) { value.DuplicateNameGroups = 1 },
		"public-orders":  func(value *metrics) { value.PublicOrdersRows = 1 },
		"biz-orders":     func(value *metrics) { value.BusinessOrdersRows = 1 },
		"public-goods":   func(value *metrics) { value.PublicOrderGoodsRows = 1 },
		"biz-goods":      func(value *metrics) { value.BusinessOrderGoodsRows = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			actual := expected
			mutate(&actual)
			if err := validateExpectedMetrics(actual); err == nil {
				t.Fatal("drifted projection metrics accepted")
			}
		})
	}
}

func TestVerifierUsesOnlyTheFixedAuditAndBusinessViewsAndIndependentlyChecksPublicACLs(t *testing.T) {
	t.Parallel()
	if strings.Count(auditMetricsSQL, "mss_m_aussibuy_biz.r1_member_levels_projection_audit") != 1 ||
		strings.Contains(auditMetricsSQL, "public.") || strings.Contains(auditMetricsSQL, "$1") {
		t.Fatal("audit query is not fixed to the single aggregate view")
	}
	for _, relation := range []string{"member_levels", "orders", "order_goods"} {
		if strings.Count(publicPrivilegeSQL, "'"+relation+"'") != 1 {
			t.Fatalf("public runtime OID lookup for %s is missing or duplicated", relation)
		}
	}
	if strings.Count(publicPrivilegeSQL, "has_table_privilege") != 8 ||
		strings.Count(publicPrivilegeSQL, "has_any_column_privilege") != 4 ||
		strings.Count(publicPrivilegeSQL, "'MAINTAIN'") != 1 ||
		!strings.Contains(publicPrivilegeSQL, "relation.oid") ||
		!strings.Contains(publicPrivilegeSQL, "namespace.nspname = 'public'") ||
		strings.Contains(publicPrivilegeSQL, "'public.") ||
		strings.Contains(publicPrivilegeSQL, "'SELECT,") || strings.Contains(publicPrivilegeSQL, "$1") {
		t.Fatal("public ACL proof can read rows or is not the exact three-table check")
	}
	for _, relation := range []string{"mss_m_aussibuy_biz.member_levels", "mss_m_aussibuy_biz.orders", "mss_m_aussibuy_biz.order_goods"} {
		if !strings.Contains(businessProjectionSQL, relation) {
			t.Fatalf("business projection query omits %s", relation)
		}
	}
}

func TestSuccessJSONIsOneBoundedRecordWithoutCredentialsOrRows(t *testing.T) {
	t.Parallel()
	record := successRecord{
		Version: "mss-shop-member-levels-projection-verification/v1", Verified: true,
		TargetDatabase: postgresDatabase, BusinessSchema: mallBusinessSchema, AuditView: auditView,
		LegacyTenantID: legacyTenantID, ImportReceiptSHA256: testReceipt,
		Metrics: metrics{PublicMemberLevelsRows: 4, BusinessMemberLevelsRows: 4,
			FlaggedDefaultRows: 1, EnabledDefaultRows: 1, DuplicateNameGroups: 0},
		Namespace: namespace, PodName: projectionVerifierPodPrefix + testRevision[:32] + "-abcde",
		PodUID: testPodUID, Revision: testRevision, ImageRepository: imageRepository,
		ImageDigest: testDigest, ImageReference: imageRepository + ":" + testRevision + "@" + testDigest,
	}
	var output bytes.Buffer
	if err := writeJSON(&output, record); err != nil {
		t.Fatal(err)
	}
	encoded := output.String()
	if strings.Count(encoded, "\n") != 1 || !strings.Contains(encoded, `"verified":true`) ||
		!strings.Contains(encoded, `"runtimePublicPrivileges":false`) ||
		strings.Contains(encoded, "postgres://") || strings.Contains(encoded, "password") ||
		strings.Contains(encoded, `"id"`) || strings.Contains(encoded, `"name"`) {
		t.Fatalf("unsafe or non-singular verification JSON: %s", encoded)
	}
}

func TestConfigurationFailureStillEmitsExactlyOneSafeJSON(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	err := run(context.Background(), nil, func(string) (string, bool) { return "", false }, &output)
	if err == nil || strings.Count(output.String(), "\n") != 1 || strings.Contains(output.String(), "postgres://") {
		t.Fatalf("unsafe failure output: %q, %v", output.String(), err)
	}
	var record failureRecord
	if decodeErr := json.Unmarshal(output.Bytes(), &record); decodeErr != nil || record.Verified ||
		record.Version != "mss-shop-member-levels-projection-verification-failure/v1" ||
		record.Failure != "configuration" {
		t.Fatalf("unexpected failure record: %+v, %v", record, decodeErr)
	}
}

func TestReconcilerDeliveryImageContainsTheFixedVerifierEntrypoint(t *testing.T) {
	t.Parallel()
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(dockerfile)
	for _, expected := range []string{
		"./services/reconciler/cmd/member-levels-projection-verifier",
		"/usr/local/bin/mss-shop-member-levels-projection-verifier",
	} {
		if strings.Count(text, expected) != 1 {
			t.Fatalf("reconciler delivery image entrypoint %q is missing or duplicated", expected)
		}
	}
}

func runtimeDSN(password string) string {
	query := url.Values{
		"search_path": []string{mallCoreSchema},
		"sslmode":     []string{"verify-full"},
		"sslrootcert": []string{postgresCAPath},
	}
	return (&url.URL{
		Scheme: "postgres", User: url.UserPassword(mallRuntimeRole, password),
		Host: netJoinHostPort(postgresHost, "5432"), Path: "/" + postgresDatabase,
		RawQuery: query.Encode(),
	}).String()
}

func netJoinHostPort(host, port string) string {
	return host + ":" + port
}
