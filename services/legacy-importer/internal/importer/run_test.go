package importer

import "testing"

func TestReviewedSourceExtensionInventoryIsExact(t *testing.T) {
	reviewed := []string{
		"plpgsql|1.0|pg_catalog",
		"timescaledb|2.20.2|public",
	}
	if !sourceExtensionInventoryReviewed(reviewed) {
		t.Fatal("reviewed source extension inventory was rejected")
	}

	for _, candidate := range [][]string{
		nil,
		{"plpgsql|1.0|pg_catalog"},
		{"plpgsql|1.0|pg_catalog", "timescaledb|2.20.1|public"},
		{"plpgsql|1.0|pg_catalog", "timescaledb|2.20.2|pg_catalog"},
		{"timescaledb|2.20.2|public", "plpgsql|1.0|pg_catalog"},
		{"plpgsql|1.0|pg_catalog", "timescaledb|2.20.2|public", "unsafe|1.0|public"},
	} {
		if sourceExtensionInventoryReviewed(candidate) {
			t.Fatalf("unsafe source extension inventory was accepted: %#v", candidate)
		}
	}
}
