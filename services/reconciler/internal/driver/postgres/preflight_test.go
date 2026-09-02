package postgres

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const testExpectedImportReceipt = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func safeStageDatabaseBoundary() stageDatabaseBoundary {
	sources := allLegacyResourceNames()
	slices.Sort(sources)
	return stageDatabaseBoundary{
		ServerVersionNumber:  "170006",
		SSL:                  true,
		DatabaseName:         stage.DatabaseName,
		SessionIdentityExact: true,
		DatabaseOwnerCurrent: true,
		DatabaseMarker:       isolatedImportMarkerPrefix + testExpectedImportReceipt,
		DatabaseInventory:    []string{stage.DatabaseName, "postgres", "template0", "template1"},
		ExtensionInventory:   []string{"plpgsql:1.0:pg_catalog:true"},
		SourceTableInventory: sources,
		PublicSchemaSafe:     true,
	}
}

func TestValidateStageDatabaseBoundaryAcceptsOnlyExactIsolatedInventory(t *testing.T) {
	t.Parallel()
	boundary := safeStageDatabaseBoundary()
	if err := validateStageDatabaseBoundary(boundary, testExpectedImportReceipt); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*stageDatabaseBoundary){
		func(value *stageDatabaseBoundary) { value.SSL = false },
		func(value *stageDatabaseBoundary) { value.DatabaseOwnerCurrent = false },
		func(value *stageDatabaseBoundary) {
			value.ExtensionInventory = append(value.ExtensionInventory, "timescaledb:2.20.2:public:true")
		},
		func(value *stageDatabaseBoundary) { value.SourceTableInventory = value.SourceTableInventory[1:] },
		func(value *stageDatabaseBoundary) { value.PublicSchemaSafe = false },
		func(value *stageDatabaseBoundary) { value.OrdersRows = 1 },
		func(value *stageDatabaseBoundary) { value.OrderGoodsRows = 1 },
		func(value *stageDatabaseBoundary) { value.ForeignPublicConnect = 1 },
		func(value *stageDatabaseBoundary) { value.Summary.LoginEventTriggers = 1 },
		func(value *stageDatabaseBoundary) { value.Summary.Publications = 1 },
	}
	for _, mutate := range mutations {
		candidate := boundary
		candidate.DatabaseInventory = append([]string(nil), boundary.DatabaseInventory...)
		candidate.ExtensionInventory = append([]string(nil), boundary.ExtensionInventory...)
		candidate.SourceTableInventory = append([]string(nil), boundary.SourceTableInventory...)
		mutate(&candidate)
		if err := validateStageDatabaseBoundary(candidate, testExpectedImportReceipt); err == nil {
			t.Fatal("unsafe isolated database boundary was accepted")
		}
	}

	wrongReceipt := strings.Repeat("b", 64)
	if err := validateStageDatabaseBoundary(boundary, wrongReceipt); err == nil {
		t.Fatal("database marker for a different import receipt was accepted")
	}
}

func TestExpectedImportReceiptIsExactNonzeroLowercaseSHA256(t *testing.T) {
	t.Parallel()
	if !validExpectedImportReceipt(testExpectedImportReceipt) {
		t.Fatal("reviewed test import receipt rejected")
	}
	for _, value := range []string{
		"",
		strings.Repeat("0", 64),
		strings.Repeat("a", 63),
		strings.Repeat("A", 64),
		strings.Repeat("g", 64),
	} {
		if validExpectedImportReceipt(value) {
			t.Fatalf("invalid import receipt accepted: length=%d", len(value))
		}
	}
}

func TestPreflightReceiptErrorDoesNotEchoValue(t *testing.T) {
	t.Parallel()
	const value = "receipt-value-that-must-not-be-echoed"
	_, err := PreflightStageDatabase(context.Background(), "not-a-dsn", value)
	if err == nil {
		t.Fatal("invalid import receipt unexpectedly accepted")
	}
	if strings.Contains(err.Error(), value) {
		t.Fatalf("preflight error exposed import receipt: %v", err)
	}
}
