package legacydb

import "testing"

func TestDefaultRegistryOwnsOnlyThePlatformPaymentResource(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	if got := len(registry.All()); got != ExpectedSharedResourceCount {
		t.Fatalf("resource count = %d, want %d", got, ExpectedSharedResourceCount)
	}
	if definitions := registry.All(); len(definitions) != 1 || definitions[0].Resource.Name != "payments" {
		t.Fatalf("runtime resources = %#v", definitions)
	}
	for _, name := range []string{"brands", "categories", "classes", "goods_infos", "couriers", "courier_pack_rules", "courier_links", "tenants", "roles", "users", "orders", "shipping_warehouses"} {
		if _, ok := registry.Lookup(name); ok {
			t.Errorf("out-of-scope resource %s is visible", name)
		}
	}
}

func TestPublishedRegistryFreezesTheEightResourceMigrationInput(t *testing.T) {
	t.Parallel()
	want := []string{"brands", "categories", "classes", "courier_links", "courier_pack_rules", "couriers", "goods_infos", "payments"}
	definitions := PublishedRegistry().All()
	if len(definitions) != PublishedSharedResourceCount {
		t.Fatalf("published resource count = %d", len(definitions))
	}
	for index, definition := range definitions {
		if definition.Resource.Name != want[index] {
			t.Fatalf("published resource %d = %q, want %q", index, definition.Resource.Name, want[index])
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
	definition, _ := registry.Lookup("payments")
	definition.Resource.Columns[0].Name = "changed"
	definition.Resource.PrimaryKey[0] = "changed"
	again, _ := registry.Lookup("payments")
	if again.Resource.Columns[0].Name != "id" || again.Resource.PrimaryKey[0] != "id" {
		t.Fatal("registry data was mutable through Lookup")
	}
}
