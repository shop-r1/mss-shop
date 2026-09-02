package postgres

// legacyView describes one of the 43 tenant-scoped compatibility views. The
// list is compiled and reviewed; the database is never used for discovery.
type legacyView struct {
	Name            string
	Inherited       *inheritedScope
	RedactedColumns []string
}

type inheritedScope struct {
	LocalColumn      string
	ParentTable      string
	ParentColumn     string
	ParentSoftDelete bool
}

type snapshotResource struct {
	Name       string
	PrimaryKey []string
	Columns    []snapshotColumn
	Indexes    [][]string
}

type snapshotColumn struct {
	Name      string
	Type      string
	NotNull   bool
	Collation string
}

var mallLegacyViews = []legacyView{
	{Name: "activities"},
	{Name: "activity_links"},
	{Name: "collections"},
	{Name: "consignees", RedactedColumns: []string{"id_card", "id_card_front", "id_card_back"}},
	{Name: "consumers"},
	{Name: "coupon_links", Inherited: &inheritedScope{LocalColumn: "coupon_id", ParentTable: "coupons", ParentColumn: "id", ParentSoftDelete: true}},
	{Name: "coupon_parents"},
	{Name: "coupons"},
	{Name: "courier_installs", RedactedColumns: []string{"app_key", "app_secret", "param0", "param1"}},
	{Name: "courier_templates"},
	{Name: "finance_logs"},
	{Name: "finances"},
	{Name: "function_circles"},
	{Name: "gold_withdraws", RedactedColumns: []string{"bank_account", "voucher"}},
	{Name: "goods"},
	{Name: "goods_assembles", Inherited: &inheritedScope{LocalColumn: "link_id", ParentTable: "goods", ParentColumn: "id", ParentSoftDelete: true}},
	{Name: "goods_shipping_warehouses", Inherited: &inheritedScope{LocalColumn: "goods_id", ParentTable: "goods", ParentColumn: "id", ParentSoftDelete: true}},
	{Name: "goods_specifications"},
	{Name: "inventories"},
	{Name: "inventory_checks"},
	{Name: "inventory_tracks"},
	{Name: "member_goods"},
	{Name: "member_levels"},
	{Name: "members", RedactedColumns: []string{"password_hash", "salt", "rest_password_hash", "open_id", "union_id"}},
	{Name: "message_events"},
	{Name: "message_templates"},
	{Name: "message_users"},
	{Name: "messages"},
	{Name: "order_goods", Inherited: &inheritedScope{LocalColumn: "order_id", ParentTable: "orders", ParentColumn: "id", ParentSoftDelete: true}},
	{Name: "order_unit_packs"},
	{Name: "orders"},
	{Name: "payment_installs", RedactedColumns: []string{"app_key", "app_secret"}},
	{Name: "payment_orders", RedactedColumns: []string{"token", "callback"}},
	{Name: "real_warehouses"},
	{Name: "receipt_goods"},
	{Name: "receipts"},
	{Name: "sell_goods"},
	{Name: "sells"},
	{Name: "senders"},
	{Name: "shipping_warehouses"},
	{Name: "shopping_carts"},
	{Name: "show_categories"},
	{Name: "system_configs", RedactedColumns: []string{"metadata"}},
}

var mallSnapshots = []snapshotResource{
	{
		Name: "brands", PrimaryKey: []string{"id"}, Indexes: [][]string{{"deleted_at"}},
		Columns: []snapshotColumn{
			{Name: "id", Type: "character varying(20)", NotNull: true, Collation: "default"},
			{Name: "created_at", Type: "timestamp with time zone"}, {Name: "updated_at", Type: "timestamp with time zone"},
			{Name: "deleted_at", Type: "timestamp with time zone"},
			{Name: "name_zh", Type: "character varying(100)", Collation: "default"},
			{Name: "name_en", Type: "character varying(100)", Collation: "default"},
			{Name: "logo", Type: "text", Collation: "default"},
			{Name: "site_url", Type: "character varying(255)", Collation: "default"},
			{Name: "index_img", Type: "character varying(255)", Collation: "default"},
			{Name: "bg_img", Type: "character varying(255)", Collation: "default"},
			{Name: "description", Type: "text", Collation: "default"},
			{Name: "sort", Type: "integer"}, {Name: "status", Type: "integer"},
		},
	},
	{
		Name: "categories", PrimaryKey: []string{"id"}, Indexes: [][]string{{"deleted_at"}},
		Columns: []snapshotColumn{
			{Name: "id", Type: "character varying(20)", NotNull: true, Collation: "default"},
			{Name: "created_at", Type: "timestamp with time zone"}, {Name: "updated_at", Type: "timestamp with time zone"},
			{Name: "deleted_at", Type: "timestamp with time zone"},
			{Name: "parent_id", Type: "text", Collation: "default"},
			{Name: "name", Type: "character varying(100)", Collation: "default"},
			{Name: "alias", Type: "character varying(100)", Collation: "default"},
			{Name: "description", Type: "text", Collation: "default"}, {Name: "sort", Type: "integer"},
			{Name: "img", Type: "character varying(255)", Collation: "default"},
			{Name: "tag", Type: "character varying(255)", Collation: "default"}, {Name: "pack_rule", Type: "json"},
		},
	},
	{
		Name: "classes", PrimaryKey: []string{"id"}, Indexes: [][]string{{"deleted_at"}},
		Columns: []snapshotColumn{
			{Name: "id", Type: "character varying(20)", NotNull: true, Collation: "default"},
			{Name: "created_at", Type: "timestamp with time zone"}, {Name: "updated_at", Type: "timestamp with time zone"},
			{Name: "deleted_at", Type: "timestamp with time zone"}, {Name: "category_id", Type: "text", Collation: "default"},
			{Name: "name", Type: "character varying(100)", Collation: "default"}, {Name: "attributes", Type: "bytea"},
			{Name: "status", Type: "integer"},
		},
	},
	{
		Name: "goods_infos", PrimaryKey: []string{"id"},
		Indexes: [][]string{{"brand_id"}, {"category_id"}, {"deleted_at"}, {"parent_category_id"}},
		Columns: []snapshotColumn{
			{Name: "id", Type: "character varying(20)", NotNull: true, Collation: "default"},
			{Name: "created_at", Type: "timestamp with time zone"}, {Name: "updated_at", Type: "timestamp with time zone"},
			{Name: "deleted_at", Type: "timestamp with time zone"},
			{Name: "category_id", Type: "character varying(20)", Collation: "default"},
			{Name: "parent_category_id", Type: "character varying(20)", Collation: "default"},
			{Name: "brand_id", Type: "character varying(20)", Collation: "default"},
			{Name: "name", Type: "character varying(255)", Collation: "default"},
			{Name: "album", Type: "text", Collation: "default"}, {Name: "description", Type: "text", Collation: "default"},
			{Name: "image", Type: "character varying(255)", Collation: "default"},
			{Name: "video", Type: "character varying(255)", Collation: "default"},
			{Name: "keywords", Type: "character varying(255)", Collation: "default"},
			{Name: "bar_code", Type: "character varying(100)", Collation: "default"},
			{Name: "content", Type: "text", Collation: "default"}, {Name: "weight", Type: "integer"},
			{Name: "has_pack_rule", Type: "boolean"}, {Name: "pack_rule", Type: "json"},
			{Name: "unit", Type: "character varying(20)", Collation: "default"}, {Name: "goods_type", Type: "integer"},
		},
	},
	{
		Name: "couriers", PrimaryKey: []string{"id"}, Indexes: [][]string{{"deleted_at"}, {"region"}},
		Columns: []snapshotColumn{
			{Name: "id", Type: "character varying(20)", NotNull: true, Collation: "default"},
			{Name: "created_at", Type: "timestamp with time zone"}, {Name: "updated_at", Type: "timestamp with time zone"},
			{Name: "deleted_at", Type: "timestamp with time zone"},
			{Name: "name", Type: "character varying(100)", Collation: "default"},
			{Name: "logo", Type: "character varying(255)", Collation: "default"}, {Name: "status", Type: "integer"},
			{Name: "site_url", Type: "character varying(255)", Collation: "default"},
			{Name: "region", Type: "character varying(20)", Collation: "default"},
			{Name: "method", Type: "character varying(100)", Collation: "default"},
		},
	},
	{
		Name: "courier_pack_rules", PrimaryKey: []string{"id"}, Indexes: [][]string{{"courier_id"}, {"deleted_at"}},
		Columns: []snapshotColumn{
			{Name: "id", Type: "character varying(20)", NotNull: true, Collation: "default"},
			{Name: "created_at", Type: "timestamp with time zone"}, {Name: "updated_at", Type: "timestamp with time zone"},
			{Name: "deleted_at", Type: "timestamp with time zone"},
			{Name: "courier_id", Type: "character varying(20)", Collation: "default"},
			{Name: "name", Type: "character varying(100)", Collation: "default"},
			{Name: "simple", Type: "integer"}, {Name: "mixed", Type: "integer"}, {Name: "mixed_sum", Type: "integer"},
			{Name: "price_unit", Type: "numeric(10,2)"}, {Name: "price_total", Type: "numeric(10,2)"},
		},
	},
	{
		Name: "courier_links", PrimaryKey: []string{"id", "link_id", "left_rule_id"},
		Columns: []snapshotColumn{
			{Name: "id", Type: "text", NotNull: true, Collation: "default"},
			{Name: "link_id", Type: "text", NotNull: true, Collation: "default"},
			{Name: "left_rule_id", Type: "text", NotNull: true, Collation: "default"},
			{Name: "object_ids_data", Type: "text", Collation: "default"},
			{Name: "created_at", Type: "timestamp with time zone"},
		},
	},
}

const (
	expectedMallViewCount                     = 43
	expectedMallSnapshotCount                 = 7
	expectedMemberLevelsProjectionColumnCount = 12
	tenantSharedResource                      = "payments"
	mallSettingsPrivateView                   = "r1_mall_settings_system_configs"
	memberLevelsProjectionAuditView           = "r1_member_levels_projection_audit"
	snapshotAuditTable                        = "r1_reconcile_snapshot_audit"
	snapshotPlanVersion                       = "legacy-global-snapshot-v4-explicit-ddl"
)
