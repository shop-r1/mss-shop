package memberlevels

import "testing"

func TestSupportedPostgresRelationKindIncludesReconcilerView(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"r", "p", "v"} {
		if !supportedPostgresRelationKind(kind) {
			t.Errorf("PostgreSQL relation kind %q was rejected", kind)
		}
	}
	for _, kind := range []string{"", "f", "m", "S"} {
		if supportedPostgresRelationKind(kind) {
			t.Errorf("unsupported PostgreSQL relation kind %q was accepted", kind)
		}
	}
}

func TestReadinessRequiresExactReconcilerViewSet(t *testing.T) {
	t.Parallel()

	want := map[string]struct{}{
		"activities": {}, "coupon_parents": {}, "goods": {},
		"goods_shipping_warehouses": {}, "member_levels": {}, "members": {},
	}
	if len(requiredLegacyColumns) != len(want) {
		t.Fatalf("readiness relation count = %d, want %d", len(requiredLegacyColumns), len(want))
	}
	for _, requirement := range requiredLegacyColumns {
		if _, exists := want[requirement.table]; !exists {
			t.Errorf("unexpected readiness relation %q", requirement.table)
		}
		delete(want, requirement.table)
	}
	for missing := range want {
		t.Errorf("reconciler view %q is missing from readiness", missing)
	}
}
