// Package legacydb exposes the deliberately narrow, schema-qualified access
// layer for the old r1shop business tables. The manifest in this package is an
// allowlist, not database discovery: a newly found table is inaccessible until
// it is reviewed and added here.
package legacydb

import (
	"sort"
	"strings"
)

const ExpectedMallResourceCount = 43

var frontendDomains = []string{
	"catalog", "customers", "orders", "fulfillment", "marketing", "finance",
	"sales", "messaging", "inventory", "settings", "storefront",
}

func FrontendDomains() []string {
	return append([]string(nil), frontendDomains...)
}

type ColumnType string

const (
	ColumnString   ColumnType = "string"
	ColumnNumber   ColumnType = "number"
	ColumnBoolean  ColumnType = "boolean"
	ColumnDatetime ColumnType = "datetime"
	ColumnJSON     ColumnType = "json"
	ColumnSecret   ColumnType = "secret"
)

type Column struct {
	Name     string     `json:"name"`
	Label    string     `json:"label"`
	Type     ColumnType `json:"type"`
	Writable bool       `json:"writable"`
	Secret   bool       `json:"secret"`
	Required bool       `json:"required"`
}

type Capabilities struct {
	Detail bool `json:"detail"`
	Create bool `json:"create"`
	Update bool `json:"update"`
	Delete bool `json:"delete"`
}

type Resource struct {
	Name         string       `json:"name"`
	Domain       string       `json:"domain"`
	TitleKey     string       `json:"titleKey"`
	PrimaryKey   []string     `json:"primaryKey"`
	Columns      []Column     `json:"columns"`
	Capabilities Capabilities `json:"capabilities"`
}

// InheritedScope describes a tenant guard through an owning business row.
// Every identifier comes from this compiled manifest and is never supplied by
// an HTTP request.
type InheritedScope struct {
	LocalColumn        string
	ParentTable        string
	ParentColumn       string
	ParentTenantColumn string
	ParentSoftDelete   bool
}

type Definition struct {
	Resource      Resource
	TenantColumn  string
	Inherited     *InheritedScope
	SoftDelete    bool
	NestedSecrets []string
}

type Registry struct {
	definitions map[string]Definition
	names       []string
}

type resourceSeed struct {
	name          string
	domain        string
	primaryKey    string
	columns       string
	secrets       string
	required      string
	softDelete    bool
	tenantColumn  string
	inherited     *InheritedScope
	nestedSecrets string
}

// DefaultRegistry is the reviewed mall-platform projection of the 54-table
// legacy inventory. It is deliberately read-only until each legacy mutation
// workflow has restored its original validation, relation, tenant, and delete
// semantics. courier_links intentionally does not appear here: it has no
// tenant_id and belongs to the shared courier catalogue owned by the tenant
// platform.
func DefaultRegistry() Registry {
	seeds := []resourceSeed{
		{name: "activities", domain: "marketing", primaryKey: "id tenant_id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id name description index_img bg_img status show start_at end_at expiration sort activity_type metadata extend_multi extend_data member_ids_data member_level_ids_data warehouse_ids_data"},
		{name: "activity_links", domain: "marketing", primaryKey: "id tenant_id", columns: "id created_at tenant_id activity_id link_type activity_type link_id name image"},
		{name: "collections", domain: "customers", primaryKey: "tenant_id member_id consumer_id goods_id", columns: "tenant_id member_id consumer_id goods_id created_at"},
		{name: "consignees", domain: "customers", primaryKey: "id", softDelete: true, secrets: "id_card id_card_front id_card_back", columns: "id created_at updated_at deleted_at tenant_id member_id consumer_id name phone country province city address tag init id_card id_card_front id_card_back"},
		{name: "consumers", domain: "customers", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id member_id open_id union_id nickname head_image sex city province country address head_image_url level status remark"},
		{name: "coupon_links", domain: "marketing", primaryKey: "link_type link_id coupon_id", inherited: &InheritedScope{LocalColumn: "coupon_id", ParentTable: "coupons", ParentColumn: "id", ParentTenantColumn: "tenant_id", ParentSoftDelete: true}, columns: "link_type link_id name image coupon_id"},
		{name: "coupon_parents", domain: "marketing", primaryKey: "id tenant_id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id name description start_at end_at send_type enough_type enough_price reduce warehouse_ids_data links_data status expiration sent member_ids_data member_level_ids_data"},
		{name: "coupons", domain: "marketing", primaryKey: "id tenant_id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id name description start_at end_at send_type enough_type enough_price reduce warehouse_ids_data order_id parent_id member_id status used"},
		{name: "courier_installs", domain: "fulfillment", primaryKey: "id", softDelete: true, secrets: "app_key app_secret param0 param1", columns: "id created_at updated_at deleted_at used tenant_id courier_id app_key app_secret param0 param1 prefix region max_amount max_weight custom_price price_unit price_total custom_courier_fee_data count"},
		{name: "courier_templates", domain: "fulfillment", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id courier_install_id name first_weight first_price continued_price code_data"},
		{name: "finance_logs", domain: "finance", primaryKey: "id", columns: "id created_at tenant_id member_id link_id username nickname phone finance_type from_type old change freeze old_aud change_aud freeze_aud remark"},
		{name: "finances", domain: "finance", primaryKey: "member_id", softDelete: true, columns: "member_id tenant_id overage overage_aud gold freeze_overage freeze_overage_aud freeze_gold deleted_at created_at updated_at"},
		{name: "function_circles", domain: "marketing", primaryKey: "id", softDelete: true, required: "title", columns: "id created_at updated_at deleted_at title type status tenant_id bg_color bg_image media video link_type link_id content url sort"},
		{name: "gold_withdraws", domain: "finance", primaryKey: "id member_id", softDelete: true, secrets: "bank_account voucher", columns: "id created_at updated_at deleted_at tenant_id member_id member_name amount bank_type bank_account real_name bank_location check_status paid voucher"},
		{name: "goods", domain: "catalog", primaryKey: "id tenant_id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id category_id parent_category_id brand_id used goods_info_id show_category_id parent_show_category_id alias commission_rmb bar_code image album video content description quality_period stage show status inventory need_inventory click_num buy_num specification_info has_specification metadata sort unit custom_pay payment_ids topped_at goods_type"},
		{name: "goods_assembles", domain: "catalog", primaryKey: "id", inherited: &InheritedScope{LocalColumn: "link_id", ParentTable: "goods", ParentColumn: "id", ParentTenantColumn: "tenant_id", ParentSoftDelete: true}, columns: "id created_at link_id goods_id goods_info_id name image quantity"},
		{name: "goods_shipping_warehouses", domain: "catalog", primaryKey: "id", inherited: &InheritedScope{LocalColumn: "goods_id", ParentTable: "goods", ParentColumn: "id", ParentTenantColumn: "tenant_id", ParentSoftDelete: true}, columns: "id goods_id warehouse_id member_level_price_data price init"},
		{name: "goods_specifications", domain: "catalog", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at name tenant_id goods_id bar_code specification ratio album inventory default_select"},
		{name: "inventories", domain: "inventory", primaryKey: "tenant_id goods_id real_warehouse_id", columns: "tenant_id goods_id real_warehouse_id alias bar_code quantity created_at updated_at"},
		{name: "inventory_checks", domain: "inventory", primaryKey: "id tenant_id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id content created_by created_by_name"},
		{name: "inventory_tracks", domain: "inventory", primaryKey: "id", columns: "id created_at tenant_id goods_id alias real_warehouse_id shipping_warehouse_id link_type link_id quantity_change quantity"},
		{name: "member_goods", domain: "catalog", primaryKey: "tenant_id member_id goods_id", columns: "tenant_id member_id goods_id show use details_data created_at updated_at"},
		{name: "member_levels", domain: "customers", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id name has_market change_courier payment_ids ratio init status"},
		{name: "members", domain: "customers", primaryKey: "id", softDelete: true, secrets: "password_hash salt rest_password_hash open_id union_id", columns: "id created_at updated_at deleted_at tenant_id region referrer_id level_id username nickname phone description open_id union_id password_hash salt rest_password_hash status head_image metadata address sex parent_referrer_id shop_name aud_to_cny free_shipping use_self_aud_to_cny percent_level0 percent_level1 pay_qr_code contact_qr_code"},
		{name: "message_events", domain: "messaging", primaryKey: "id", softDelete: true, required: "name app object event status", columns: "id created_at updated_at deleted_at tenant_id name app object event status"},
		{name: "message_templates", domain: "messaging", primaryKey: "id", softDelete: true, required: "event_id name title content status", columns: "id created_at updated_at deleted_at tenant_id event_id name title content status"},
		{name: "message_users", domain: "messaging", primaryKey: "message_id user_id", columns: "tenant_id message_id user_id read read_time"},
		{name: "messages", domain: "messaging", primaryKey: "id", columns: "id created_at tenant_id title content hits top"},
		{name: "order_goods", domain: "orders", primaryKey: "id", softDelete: true, inherited: &InheritedScope{LocalColumn: "order_id", ParentTable: "orders", ParentColumn: "id", ParentTenantColumn: "tenant_id", ParentSoftDelete: true}, columns: "id created_at updated_at deleted_at order_id goods_id goods_spec_id quantity price goods_data goods_specification_data pack_specification"},
		{name: "order_unit_packs", domain: "orders", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id member_id order_id pack_data weight net_weight goods_price goods_price_copy courier_price courier_price_copy currency courier_id courier_install_id courier_name courier_logo courier_no method remark send_status print need_update_weight"},
		{name: "orders", domain: "orders", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id member_id consumer_id courier_id courier_install_id order_pay_id order_status order_status_copy consignee_id consignee_data sender_id sender_data money money_copy currency overage gold courier_price goods_price price courier_price_copy goods_price_copy price_copy voucher_image_copy reduce_fee reduce_fee_copy warehouse_id get_self payment_ids payment_name payment_order_id bill_account remark description goods_name channel activities_data activities_data_copy referral_code commission_rmb financial_audit financial_remark financial_id financial_name print aud_to_cny aud_to_cny_copy"},
		{name: "payment_installs", domain: "fulfillment", primaryKey: "id", softDelete: true, secrets: "app_key app_secret", columns: "id created_at updated_at deleted_at used tenant_id payment_id app_key app_secret image sort description"},
		{name: "payment_orders", domain: "orders", primaryKey: "id", softDelete: true, secrets: "token callback", columns: "id created_at updated_at deleted_at tenant_id member_id order_id payment_install_id method overage currency gold order_fee real_fee aud_to_cny pay_url redirect token external_order_id payment_status voucher_image remark callback"},
		{name: "real_warehouses", domain: "inventory", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id name region address status"},
		{name: "receipt_goods", domain: "inventory", primaryKey: "tenant_id receipt_id goods_id", columns: "tenant_id receipt_id goods_id bar_code alias image unit_price price quantity remark"},
		{name: "receipts", domain: "inventory", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id real_warehouse_id supplier currency price amount payment payment_account payment_time remark created_by created_by_name bill_create_time"},
		{name: "sell_goods", domain: "sales", primaryKey: "tenant_id sell_id goods_id", columns: "tenant_id sell_id goods_id goods_name quantity currency unit_price price remark order_create_time warehouse_id real_warehouse_id"},
		{name: "sells", domain: "sales", primaryKey: "id", softDelete: true, columns: "id tenant_id warehouse_id real_warehouse_id seller_id member_id member_name currency courier_price goods_price price bill bill_account bill_date payment_name order_create_time remark channel financial_audit financial_remark financial_id financial_name deleted_at created_at updated_at"},
		{name: "senders", domain: "customers", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id member_id consumer_id name phone country province city address tag default"},
		{name: "shipping_warehouses", domain: "fulfillment", primaryKey: "id", softDelete: true, columns: "id created_at updated_at deleted_at tenant_id name currency region address status get_self need_id_card couriers_data custom_pay payment_ids real_warehouse_id"},
		{name: "shopping_carts", domain: "storefront", primaryKey: "id", columns: "id created_at updated_at tenant_id member_id consumer_id goods_id goods_specification_id warehouse_id pack_specification unit quantity selected"},
		{name: "show_categories", domain: "catalog", primaryKey: "id", softDelete: true, required: "name", columns: "id created_at updated_at deleted_at tenant_id name image parent_id status description sort"},
		{name: "system_configs", domain: "settings", primaryKey: "id", softDelete: true, nestedSecrets: "appSecret app_secret", columns: "id created_at updated_at deleted_at tenant_id name metadata"},
	}

	definitions := make(map[string]Definition, len(seeds))
	names := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		definition := definitionFromSeed(seed)
		definitions[seed.name] = definition
		names = append(names, seed.name)
	}
	sort.Strings(names)
	return Registry{definitions: definitions, names: names}
}

func definitionFromSeed(seed resourceSeed) Definition {
	primaryKey := strings.Fields(seed.primaryKey)
	secretSet := stringSet(seed.secrets)
	requiredSet := stringSet(seed.required)
	capabilities := Capabilities{}
	tenantColumn := seed.tenantColumn
	if tenantColumn == "" && seed.inherited == nil {
		tenantColumn = "tenant_id"
	}
	columns := make([]Column, 0, len(strings.Fields(seed.columns)))
	for _, name := range strings.Fields(seed.columns) {
		_, secret := secretSet[name]
		_, required := requiredSet[name]
		columnType := inferColumnType(name)
		if secret {
			columnType = ColumnSecret
		}
		columns = append(columns, Column{
			Name:     name,
			Label:    "legacy.fields." + name,
			Type:     columnType,
			Writable: false,
			Secret:   secret,
			Required: required,
		})
	}
	for _, column := range columns {
		if column.Name == "id" {
			capabilities.Detail = true
			break
		}
	}
	return Definition{
		Resource: Resource{
			Name:         seed.name,
			Domain:       seed.domain,
			TitleKey:     "legacy.resources." + seed.name,
			PrimaryKey:   primaryKey,
			Columns:      columns,
			Capabilities: capabilities,
		},
		TenantColumn:  tenantColumn,
		Inherited:     cloneInheritedScope(seed.inherited),
		SoftDelete:    seed.softDelete,
		NestedSecrets: strings.Fields(seed.nestedSecrets),
	}
}

func (registry Registry) Lookup(name string) (Definition, bool) {
	definition, ok := registry.definitions[name]
	if !ok {
		return Definition{}, false
	}
	return cloneDefinition(definition), true
}

func (registry Registry) All() []Definition {
	result := make([]Definition, 0, len(registry.names))
	for _, name := range registry.names {
		definition, _ := registry.Lookup(name)
		result = append(result, definition)
	}
	return result
}

func cloneDefinition(definition Definition) Definition {
	clone := definition
	clone.Resource.PrimaryKey = append([]string(nil), definition.Resource.PrimaryKey...)
	clone.Resource.Columns = append([]Column(nil), definition.Resource.Columns...)
	clone.Inherited = cloneInheritedScope(definition.Inherited)
	clone.NestedSecrets = append([]string(nil), definition.NestedSecrets...)
	return clone
}

func cloneInheritedScope(scope *InheritedScope) *InheritedScope {
	if scope == nil {
		return nil
	}
	clone := *scope
	return &clone
}

func stringSet(value string) map[string]struct{} {
	result := make(map[string]struct{}, len(strings.Fields(value)))
	for _, item := range strings.Fields(value) {
		result[item] = struct{}{}
	}
	return result
}

func inferColumnType(name string) ColumnType {
	if name == "metadata" || name == "album" || name == "specification_info" ||
		strings.HasSuffix(name, "_data") || strings.HasSuffix(name, "_ids") {
		return ColumnJSON
	}
	if name == "created_at" || name == "updated_at" || name == "deleted_at" ||
		name == "start_at" || name == "end_at" || name == "topped_at" ||
		strings.HasSuffix(name, "_time") || strings.HasSuffix(name, "_date") {
		return ColumnDatetime
	}
	if _, ok := booleanColumns[name]; ok {
		return ColumnBoolean
	}
	if _, ok := numberColumns[name]; ok || strings.HasSuffix(name, "_price") ||
		strings.HasSuffix(name, "_fee") || strings.HasSuffix(name, "_amount") ||
		strings.HasSuffix(name, "_weight") || strings.HasSuffix(name, "_quantity") ||
		strings.HasSuffix(name, "_ratio") || strings.HasSuffix(name, "_copy") {
		return ColumnNumber
	}
	return ColumnString
}

var booleanColumns = map[string]struct{}{
	"custom_pay": {}, "custom_price": {}, "default": {}, "default_select": {},
	"financial_audit": {}, "free_shipping": {}, "get_self": {}, "has_market": {},
	"has_specification": {}, "init": {}, "need_id_card": {}, "need_inventory": {},
	"need_update_weight": {}, "paid": {}, "print": {}, "read": {}, "selected": {},
	"show": {}, "top": {}, "use": {}, "use_self_aud_to_cny": {}, "used": {}, "video": {},
}

var numberColumns = map[string]struct{}{
	"amount": {}, "aud_to_cny": {}, "buy_num": {}, "change": {}, "change_aud": {},
	"click_num": {}, "commission_rmb": {}, "count": {}, "courier_price": {},
	"enough_price": {}, "expiration": {}, "freeze": {}, "freeze_aud": {},
	"freeze_gold": {}, "freeze_overage": {}, "freeze_overage_aud": {}, "gold": {},
	"goods_price": {}, "hits": {}, "inventory": {}, "level": {}, "max_amount": {},
	"max_weight": {}, "money": {}, "net_weight": {}, "old": {}, "old_aud": {},
	"order_fee": {}, "overage": {}, "overage_aud": {}, "percent_level0": {},
	"percent_level1": {}, "price": {}, "price_total": {}, "price_unit": {},
	"quantity": {}, "quantity_change": {}, "ratio": {}, "real_fee": {}, "reduce": {},
	"reduce_fee": {}, "sent": {}, "sex": {}, "sort": {}, "stage": {}, "status": {},
	"unit_price": {}, "weight": {},
}
