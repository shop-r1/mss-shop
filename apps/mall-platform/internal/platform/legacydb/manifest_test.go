package legacydb

import (
	"strings"
	"testing"
)

func TestDefaultRegistryIsReviewedMallAllowlist(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	definitions := registry.All()
	if got := len(definitions); got != ExpectedMallResourceCount {
		t.Fatalf("resource count = %d, want %d", got, ExpectedMallResourceCount)
	}
	courierLinks, ok := registry.Lookup("courier_links")
	if !ok || courierLinks.Scope != ScopeSchema || courierLinks.TenantColumn != "" || courierLinks.Inherited != nil {
		t.Fatalf("courier_links must be an explicit schema-scoped mall snapshot: %#v", courierLinks)
	}
	if _, ok := registry.Lookup("area"); ok {
		t.Fatal("area is not part of the formal 54-table manifest")
	}
	shipping, ok := registry.Lookup("shipping_warehouses")
	if !ok || len(shipping.Resource.Columns) != 16 {
		t.Fatalf("shipping_warehouses evidence = %#v", shipping.Resource.Columns)
	}
}

func TestPublishedRegistryPreservesTheImmutable43ResourceMigrationInput(t *testing.T) {
	t.Parallel()

	published := PublishedRegistry()
	if got := len(published.All()); got != PublishedMallResourceCount {
		t.Fatalf("published resource count = %d, want %d", got, PublishedMallResourceCount)
	}
	for _, name := range []string{"brands", "categories", "classes", "goods_infos", "couriers", "courier_pack_rules", "courier_links"} {
		if _, ok := published.Lookup(name); ok {
			t.Errorf("published 43-resource registry unexpectedly contains %s", name)
		}
	}
	wantNames := strings.Fields("activities activity_links collections consignees consumers coupon_links coupon_parents coupons courier_installs courier_templates finance_logs finances function_circles gold_withdraws goods goods_assembles goods_shipping_warehouses goods_specifications inventories inventory_checks inventory_tracks member_goods member_levels members message_events message_templates message_users messages order_goods order_unit_packs orders payment_installs payment_orders real_warehouses receipt_goods receipts sell_goods sells senders shipping_warehouses shopping_carts show_categories system_configs")
	for index, definition := range published.All() {
		if definition.Resource.Name != wantNames[index] {
			t.Fatalf("published resource %d = %q, want %q", index, definition.Resource.Name, wantNames[index])
		}
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
		"catalog":     {"brands", "categories", "classes", "goods_infos", "goods", "goods_assembles", "goods_shipping_warehouses", "goods_specifications", "member_goods", "show_categories"},
		"customers":   {"members", "consumers", "consignees", "senders", "member_levels", "collections"},
		"orders":      {"orders", "payment_orders", "order_unit_packs", "order_goods"},
		"fulfillment": {"shipping_warehouses", "couriers", "courier_pack_rules", "courier_links", "courier_installs", "courier_templates", "payment_installs"},
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
