package memberlevels

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRawPaymentPolicyRequiresEnabledSourceAndNonEmptyTokens(t *testing.T) {
	t.Parallel()

	for _, policy := range []sql.NullString{
		{},
		{Valid: true},
		{String: "   ", Valid: true},
		{String: "payment-1,", Valid: true},
		{String: ",payment-1", Valid: true},
		{String: "payment-1,   ,payment-2", Valid: true},
	} {
		if validRawPaymentPolicy(policy) {
			t.Errorf("invalid raw payment policy %#v was accepted", policy)
		}
	}
	valid := sql.NullString{String: " payment-1 ,payment-2", Valid: true}
	if !validRawPaymentPolicy(valid) {
		t.Fatal("non-empty raw payment policy was rejected")
	}
	if (legacyMemberLevelRow{Status: sql.NullInt64{Int64: legacyDisabledStatus, Valid: true}, PaymentIDs: valid}).hasUsablePaymentPolicy() {
		t.Fatal("disabled payment-policy source was accepted")
	}
	if !(legacyMemberLevelRow{Status: sql.NullInt64{Int64: legacyEnabledStatus, Valid: true}, PaymentIDs: valid}).hasUsablePaymentPolicy() {
		t.Fatal("enabled source with a valid raw payment policy was rejected")
	}
}

func TestRevisionNeverHashesHiddenPolicyFields(t *testing.T) {
	t.Parallel()

	now := sql.NullTime{Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	base := legacyMemberLevelRow{
		ID: "100000000000000001", CreatedAt: now, UpdatedAt: now,
		TenantID:   sql.NullString{String: "tenant-a", Valid: true},
		Name:       sql.NullString{String: "Standard", Valid: true},
		Ratio:      sql.NullString{String: "10", Valid: true},
		Init:       sql.NullBool{Bool: true, Valid: true},
		Status:     sql.NullInt64{Int64: legacyEnabledStatus, Valid: true},
		PaymentIDs: sql.NullString{String: "payment-1", Valid: true},
	}
	hiddenChange := base
	hiddenChange.PaymentIDs.String = "payment-2"
	hiddenChange.HasMarket = sql.NullBool{Bool: true, Valid: true}
	hiddenChange.ChangeCourier = sql.NullBool{Bool: true, Valid: true}
	if base.revision() == "" || base.revision() != hiddenChange.revision() {
		t.Fatal("revision exposed a hidden policy-field candidate")
	}
	publicChange := base
	publicChange.Name.String = "Wholesale"
	if base.revision() == publicChange.revision() {
		t.Fatal("revision ignored a public concurrency field")
	}
}

func TestCSVReferenceExpressionsKeepIDsInBoundArguments(t *testing.T) {
	t.Parallel()

	const id = "UNIQUE_TOKEN"
	for _, dialect := range []string{"postgres", "sqlite"} {
		expression, arguments, err := csvReferenceExpressionForDialect(dialect, id)
		if err != nil {
			t.Fatalf("%s expression: %v", dialect, err)
		}
		if !strings.Contains(expression, "?") || strings.Contains(expression, id) {
			t.Errorf("%s expression interpolated the identifier: %q", dialect, expression)
		}
		if len(arguments) != 1 {
			t.Fatalf("%s argument count = %d, want 1", dialect, len(arguments))
		}
		argument, ok := arguments[0].(string)
		want := id
		if dialect == "sqlite" {
			want = "%," + escapeLike(id) + ",%"
		}
		if !ok || argument != want {
			t.Errorf("%s bound argument = %#v, want %q", dialect, arguments[0], want)
		}
	}
	if _, _, err := csvReferenceExpressionForDialect("mysql", id); !errors.Is(err, ErrSchemaNotReady) {
		t.Fatalf("unsupported dialect error = %v", err)
	}
}

func TestMutationGateAndPaginationFailClosed(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "isolated-cutover ", " isolated-cutover", "ISOLATED-CUTOVER", "true", "unknown"} {
		closed := mutationGateForMode(mode)
		if closed.allows(PermissionCreate) || closed.allows(PermissionUpdate) ||
			closed.allows(PermissionSetDefault) || closed.allows(PermissionDelete) {
			t.Errorf("mutation mode %q did not fail closed", mode)
		}
	}
	cutover := mutationGateForMode(mutationModeIsolatedCutover)
	if !cutover.allows(PermissionCreate) || !cutover.allows(PermissionUpdate) ||
		!cutover.allows(PermissionSetDefault) || cutover.allows(PermissionDelete) {
		t.Fatal("isolated cutover exposed an unexpected operation set")
	}
	strongCutover := mutationGateForMode(mutationModeReferenceWritersStopped)
	if !strongCutover.allows(PermissionCreate) || !strongCutover.allows(PermissionUpdate) ||
		!strongCutover.allows(PermissionSetDefault) || !strongCutover.allows(PermissionDelete) {
		t.Fatal("strong isolated cutover did not enable its complete explicit operation set")
	}

	maximumInt := int(^uint(0) >> 1)
	if _, err := paginationOffset(maximumInt, 2); !errors.Is(err, ErrValidation) {
		t.Fatalf("overflowing pagination error = %v", err)
	}
	if offset, err := paginationOffset(2, maximumPageSize); err != nil || offset != maximumPageSize {
		t.Fatalf("valid pagination offset = %d, error = %v", offset, err)
	}
}
