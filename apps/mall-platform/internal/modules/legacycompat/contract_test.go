package legacycompat

import (
	"testing"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
)

func TestDetailCapabilityAndAuthorizationOperationsHaveOneSource(t *testing.T) {
	t.Parallel()
	registry := legacydb.DefaultRegistry()
	withID, _ := registry.Lookup("activities")
	withoutID, _ := registry.Lookup("coupon_links")

	if !withID.Resource.Capabilities.Detail || !operationAllowed(withID, OperationRead) || !containsOperation(operationsFor(withID), OperationRead) {
		t.Fatalf("activities detail contract diverged: %#v operations=%#v", withID.Resource.Capabilities, operationsFor(withID))
	}
	if withoutID.Resource.Capabilities.Detail || operationAllowed(withoutID, OperationRead) || containsOperation(operationsFor(withoutID), OperationRead) {
		t.Fatalf("coupon_links detail contract diverged: %#v operations=%#v", withoutID.Resource.Capabilities, operationsFor(withoutID))
	}
	if got := menuPath(withID); got != "/business/marketing/activities" {
		t.Fatalf("resource menu path = %q", got)
	}
	if got := componentPath(withID, OperationRead); got != "/business/marketing/activities/permissions/read" {
		t.Fatalf("component permission path = %q", got)
	}
}

func containsOperation(operations []Operation, wanted Operation) bool {
	for _, operation := range operations {
		if operation == wanted {
			return true
		}
	}
	return false
}
