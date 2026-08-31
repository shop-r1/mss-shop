package legacydb

import "testing"

func TestDefaultRegistryOwnsExactlyEightSharedResources(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	if got := len(registry.All()); got != ExpectedSharedResourceCount {
		t.Fatalf("resource count = %d, want %d", got, ExpectedSharedResourceCount)
	}
	for _, name := range []string{"brands", "categories", "classes", "goods_infos", "couriers", "courier_pack_rules", "courier_links", "payments"} {
		if _, ok := registry.Lookup(name); !ok {
			t.Errorf("missing shared resource %s", name)
		}
	}
	for _, name := range []string{"tenants", "roles", "users", "orders", "shipping_warehouses"} {
		if _, ok := registry.Lookup(name); ok {
			t.Errorf("out-of-scope resource %s is visible", name)
		}
	}
}

func TestSharedCapabilitiesKeepEveryResourceReadOnly(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	for _, definition := range registry.All() {
		name := definition.Resource.Name
		if definition.Resource.Capabilities != (Capabilities{Detail: true}) {
			t.Errorf("read-only resource %s capabilities = %#v", name, definition.Resource.Capabilities)
		}
		for _, column := range definition.Resource.Columns {
			if column.Writable {
				t.Errorf("read-only resource %s exposes writable column %s", name, column.Name)
			}
		}
	}
}

func TestRegistryReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	definition, _ := registry.Lookup("courier_links")
	definition.Resource.Columns[0].Name = "changed"
	definition.Resource.PrimaryKey[0] = "changed"
	again, _ := registry.Lookup("courier_links")
	if again.Resource.Columns[0].Name != "id" || again.Resource.PrimaryKey[0] != "id" {
		t.Fatal("registry data was mutable through Lookup")
	}
}
