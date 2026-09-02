package legacycompat

import (
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
)

func TestModuleRegistersCompleteBusinessContract(t *testing.T) {
	t.Parallel()
	module := NewModule(fixedbinding.StaticSource(testBinding()))
	composed, err := business.Compose(migration.New(), module)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := composed.Descriptors()
	if len(descriptors) != 1 || descriptors[0].Name != moduleName || descriptors[0].Menu.Path != businessMenuRoot {
		t.Fatalf("module descriptors = %#v", descriptors)
	}
	wantPermissions := 0
	for _, definition := range legacydb.DefaultRegistry().All() {
		wantPermissions += len(operationsFor(definition))
	}
	if len(descriptors[0].Permissions) != wantPermissions {
		t.Fatalf("permission count = %d, want %d", len(descriptors[0].Permissions), wantPermissions)
	}
	runner, err := composed.MigrationRunner()
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.ValidateRegistrations(); err != nil {
		t.Fatalf("migration registration: %v", err)
	}
}
