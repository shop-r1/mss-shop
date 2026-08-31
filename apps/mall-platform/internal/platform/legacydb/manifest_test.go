package legacydb

import "testing"

func TestDefaultRegistryIsReviewedMallAllowlist(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	definitions := registry.All()
	if got := len(definitions); got != ExpectedMallResourceCount {
		t.Fatalf("resource count = %d, want %d", got, ExpectedMallResourceCount)
	}
	if _, ok := registry.Lookup("courier_links"); ok {
		t.Fatal("courier_links belongs to the shared tenant-platform catalogue")
	}
	if _, ok := registry.Lookup("area"); ok {
		t.Fatal("area is not part of the formal 54-table manifest")
	}
	shipping, ok := registry.Lookup("shipping_warehouses")
	if !ok || len(shipping.Resource.Columns) != 16 {
		t.Fatalf("shipping_warehouses evidence = %#v", shipping.Resource.Columns)
	}
}

func TestCompatibilityProjectionIsReadOnlyUntilLegacyWorkflowsExist(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	for _, definition := range registry.All() {
		capabilities := definition.Resource.Capabilities
		if capabilities.Create || capabilities.Update || capabilities.Delete {
			t.Errorf("resource %s unexpectedly exposes mutation capabilities: %#v", definition.Resource.Name, capabilities)
		}
		for _, column := range definition.Resource.Columns {
			if column.Writable {
				t.Errorf("resource %s unexpectedly exposes writable column %s", definition.Resource.Name, column.Name)
			}
		}
	}
	for _, name := range []string{"function_circles", "message_events", "message_templates", "show_categories", "orders", "payment_orders", "system_configs"} {
		definition, ok := registry.Lookup(name)
		if !ok || definition.Resource.Capabilities != (Capabilities{Detail: true}) {
			t.Errorf("read-only resource %s capabilities = %#v", name, definition.Resource.Capabilities)
		}
	}
	withoutID, _ := registry.Lookup("coupon_links")
	if withoutID.Resource.Capabilities.Detail {
		t.Fatal("id-less composite resource unexpectedly supports detail")
	}
}

func TestSecretMetadataCannotBecomeWritable(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	for _, name := range []string{"members", "payment_installs", "payment_orders", "consignees"} {
		definition, _ := registry.Lookup(name)
		for _, column := range definition.Resource.Columns {
			if column.Secret && (column.Type != ColumnSecret || column.Writable) {
				t.Errorf("%s.%s secret metadata = %#v", name, column.Name, column)
			}
		}
	}
}

func TestRegistryReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	definition, _ := registry.Lookup("orders")
	definition.Resource.PrimaryKey[0] = "changed"
	definition.Resource.Columns[0].Name = "changed"
	again, _ := registry.Lookup("orders")
	if again.Resource.PrimaryKey[0] != "id" || again.Resource.Columns[0].Name != "id" {
		t.Fatal("registry data was mutable through Lookup")
	}
}

func TestEveryResourceMatchesFrontendDomainCatalog(t *testing.T) {
	t.Parallel()
	expectedGroups := map[string][]string{
		"catalog":     {"goods", "goods_assembles", "goods_shipping_warehouses", "goods_specifications", "member_goods", "show_categories"},
		"customers":   {"members", "consumers", "consignees", "senders", "member_levels", "collections"},
		"orders":      {"orders", "payment_orders", "order_unit_packs", "order_goods"},
		"fulfillment": {"shipping_warehouses", "courier_installs", "courier_templates", "payment_installs"},
		"marketing":   {"activities", "activity_links", "coupons", "coupon_links", "coupon_parents", "function_circles"},
		"finance":     {"finances", "finance_logs", "gold_withdraws"},
		"sales":       {"sells", "sell_goods"},
		"messaging":   {"messages", "message_users", "message_events", "message_templates"},
		"inventory":   {"real_warehouses", "inventories", "inventory_tracks", "receipts", "receipt_goods", "inventory_checks"},
		"settings":    {"system_configs"},
		"storefront":  {"shopping_carts"},
	}
	expected := make(map[string]string, ExpectedMallResourceCount)
	for domain, resources := range expectedGroups {
		for _, resource := range resources {
			expected[resource] = domain
		}
	}
	if len(expected) != ExpectedMallResourceCount || len(expectedGroups) != len(FrontendDomains()) {
		t.Fatalf("frontend catalog coverage resources=%d domains=%d", len(expected), len(expectedGroups))
	}
	for _, definition := range DefaultRegistry().All() {
		if want := expected[definition.Resource.Name]; definition.Resource.Domain != want {
			t.Errorf("%s domain = %q, want %q", definition.Resource.Name, definition.Resource.Domain, want)
		}
		delete(expected, definition.Resource.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("frontend resources missing from backend registry: %#v", expected)
	}
}
