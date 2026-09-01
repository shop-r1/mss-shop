// Package manifest owns the immutable, compiled legacy import allow-list.
package manifest

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	// ReviewedColumnsSHA256 pins the exact catalog-only evidence embedded below.
	ReviewedColumnsSHA256 = "c108b11543f41bbd8384540b7314909cd8056e3a141cc7447c443cb98c7e6e5b"
	OrdersTable           = "orders"
	OrderGoodsTable       = "order_goods"
)

//go:embed source_columns.csv
var reviewedColumnsCSV []byte

// Column is the exact safe source and target shape for one legacy column.
// No SQL expression is accepted from the source database.
type Column struct {
	Position               int
	Name                   string
	Dropped                bool
	Type                   string
	TypeNamespace          string
	TypeName               string
	TypeKind               string
	TypeMod                int
	NotNull                bool
	Storage                string
	HasDefault             bool
	DefaultExpression      string
	Identity               string
	Generated              string
	Compression            string
	HasMissing             bool
	CollationNamespace     string
	Collation              string
	CollationProvider      string
	CollationDeterministic bool
	CollationEncoding      int
	ColumnACL              string
}

// Index is a deliberately small target-only index definition. Source indexes
// are never copied or evaluated by the importer.
type Index struct {
	Primary bool
	Columns []string
}

// Table is one compiled import unit. Order headers and their dependent lines
// are structure-only so the isolated environment cannot contain orphan order
// business data.
type Table struct {
	Name     string
	Columns  []Column
	Indexes  []Index
	CopyRows bool
}

// importTableNames is the ordered target inventory. The order is also used by
// the deterministic receipt.
var importTableNames = []string{
	"activities", "activity_links", "brands", "categories", "classes",
	"collections", "consignees", "consumers", "coupon_links", "coupon_parents",
	"coupons", "courier_installs", "courier_links", "courier_pack_rules",
	"courier_templates", "couriers", "finance_logs", "finances",
	"function_circles", "gold_withdraws", "goods", "goods_assembles",
	"goods_infos", "goods_shipping_warehouses", "goods_specifications",
	"inventories", "inventory_checks", "inventory_tracks", "member_goods",
	"member_levels", "members", "message_events", "message_templates",
	"message_users", "messages", "order_goods", "order_unit_packs", "orders",
	"payment_installs", "payment_orders", "payments", "real_warehouses",
	"receipt_goods", "receipts", "sell_goods", "sells", "senders",
	"shipping_warehouses", "shopping_carts", "show_categories", "system_configs",
}

// sourceIdentityTableNames are expected in the old 54-table database but are
// intentionally not read or copied. They are transformed into MSS identity
// state by a separate reviewed workflow.
var sourceIdentityTableNames = []string{"roles", "tenants", "users"}

// ImportNames returns a copy of the ordered 51-table target inventory.
func ImportNames() []string {
	return append([]string(nil), importTableNames...)
}

// SourceIdentityNames returns a copy of the three expected, deliberately
// ignored source identity tables.
func SourceIdentityNames() []string {
	return append([]string(nil), sourceIdentityTableNames...)
}

var allowedTypes = map[string]struct{}{
	"bigint": {}, "boolean": {}, "bytea": {},
	"character varying(10)": {}, "character varying(20)": {},
	"character varying(40)": {}, "character varying(50)": {},
	"character varying(100)": {}, "character varying(255)": {},
	"integer": {}, "json": {}, "numeric": {}, "numeric(10,2)": {},
	"text": {}, "timestamp with time zone": {},
}

var reviewedIndexes = map[string][]Index{
	"brands": {
		{Primary: true, Columns: []string{"id"}},
		{Columns: []string{"deleted_at"}},
	},
	"categories": {
		{Primary: true, Columns: []string{"id"}},
		{Columns: []string{"deleted_at"}},
	},
	"classes": {
		{Primary: true, Columns: []string{"id"}},
		{Columns: []string{"deleted_at"}},
	},
	"goods_infos": {
		{Primary: true, Columns: []string{"id"}},
		{Columns: []string{"brand_id"}},
		{Columns: []string{"category_id"}},
		{Columns: []string{"deleted_at"}},
		{Columns: []string{"parent_category_id"}},
	},
	"couriers": {
		{Primary: true, Columns: []string{"id"}},
		{Columns: []string{"deleted_at"}},
		{Columns: []string{"region"}},
	},
	"courier_pack_rules": {
		{Primary: true, Columns: []string{"id"}},
		{Columns: []string{"courier_id"}},
		{Columns: []string{"deleted_at"}},
	},
	"courier_links": {
		{Primary: true, Columns: []string{"id", "link_id", "left_rule_id"}},
	},
}

var requiredHeader = []string{
	"table_name", "attnum", "attname", "attisdropped", "format_type",
	"type_namespace", "type_name", "type_kind", "atttypmod", "attnotnull",
	"atthasdef", "default_expression", "attidentity", "attgenerated",
	"attstorage", "attcompression", "collation_namespace", "collation_name",
	"collation_provider", "collation_deterministic", "collation_encoding", "column_acl",
}

// Load returns a fresh copy of the reviewed manifest and refuses any drift in
// its structural safety properties.
func Load() ([]Table, error) {
	digest := sha256.Sum256(reviewedColumnsCSV)
	if hex.EncodeToString(digest[:]) != ReviewedColumnsSHA256 {
		return nil, errors.New("legacy column manifest digest is invalid")
	}
	reader := csv.NewReader(bytes.NewReader(reviewedColumnsCSV))
	header, err := reader.Read()
	if err != nil || !equalStrings(header, requiredHeader) {
		return nil, errors.New("legacy column manifest header is invalid")
	}

	tables := make([]Table, 0, len(importTableNames))
	tablePosition := make(map[string]int, len(importTableNames))
	for index, name := range importTableNames {
		tablePosition[name] = index
		tables = append(tables, Table{
			Name:     name,
			Indexes:  cloneIndexes(reviewedIndexes[name]),
			CopyRows: name != OrdersTable && name != OrderGoodsTable,
		})
	}

	lastTablePosition := -1
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || len(record) != len(requiredHeader) {
			return nil, errors.New("legacy column manifest record is invalid")
		}
		value := func(name string) string {
			for index, field := range requiredHeader {
				if field == name {
					return record[index]
				}
			}
			return ""
		}
		position, exists := tablePosition[value("table_name")]
		if !exists || position < lastTablePosition {
			return nil, errors.New("legacy column manifest table inventory is invalid")
		}
		lastTablePosition = position
		columnPosition, parseErr := strconv.Atoi(value("attnum"))
		if parseErr != nil || columnPosition != len(tables[position].Columns)+1 {
			return nil, errors.New("legacy column manifest column order is invalid")
		}
		if _, allowed := allowedTypes[value("format_type")]; !allowed {
			return nil, errors.New("legacy column manifest type is not approved")
		}
		if value("attisdropped") != "f" || value("type_namespace") != "pg_catalog" ||
			value("type_kind") != "b" || value("atthasdef") != "f" ||
			value("default_expression") != "" || value("attidentity") != "" ||
			value("attgenerated") != "" || value("attcompression") != "" ||
			value("column_acl") != "" {
			return nil, errors.New("legacy column manifest contains an unsafe attribute")
		}
		notNull, boolErr := parsePostgresBool(value("attnotnull"))
		if boolErr != nil {
			return nil, errors.New("legacy column manifest nullability is invalid")
		}
		collation := value("collation_name")
		if collation == "" {
			if value("collation_namespace") != "" || value("collation_provider") != "" ||
				value("collation_deterministic") != "" || value("collation_encoding") != "" {
				return nil, errors.New("legacy column manifest collation is invalid")
			}
		} else if value("collation_namespace") != "pg_catalog" || collation != "default" ||
			value("collation_provider") != "d" || value("collation_deterministic") != "t" ||
			value("collation_encoding") != "-1" {
			return nil, errors.New("legacy column manifest collation is not approved")
		}
		typeMod, typeModErr := strconv.Atoi(value("atttypmod"))
		if typeModErr != nil {
			return nil, errors.New("legacy column manifest type modifier is invalid")
		}
		collationEncoding := 0
		collationDeterministic := false
		if collation != "" {
			collationEncoding, parseErr = strconv.Atoi(value("collation_encoding"))
			if parseErr != nil {
				return nil, errors.New("legacy column manifest collation encoding is invalid")
			}
			collationDeterministic = true
		}
		tables[position].Columns = append(tables[position].Columns, Column{
			Position:               columnPosition,
			Name:                   value("attname"),
			Dropped:                false,
			Type:                   value("format_type"),
			TypeNamespace:          value("type_namespace"),
			TypeName:               value("type_name"),
			TypeKind:               value("type_kind"),
			TypeMod:                typeMod,
			NotNull:                notNull,
			Storage:                value("attstorage"),
			HasDefault:             false,
			DefaultExpression:      "",
			Identity:               "",
			Generated:              "",
			Compression:            "",
			HasMissing:             false,
			CollationNamespace:     value("collation_namespace"),
			Collation:              collation,
			CollationProvider:      value("collation_provider"),
			CollationDeterministic: collationDeterministic,
			CollationEncoding:      collationEncoding,
			ColumnACL:              "",
		})
	}

	for _, table := range tables {
		if len(table.Columns) == 0 {
			return nil, fmt.Errorf("legacy column manifest table %q has no columns", table.Name)
		}
		knownColumns := make(map[string]struct{}, len(table.Columns))
		for _, column := range table.Columns {
			if column.Name == "" {
				return nil, errors.New("legacy column manifest has an empty column name")
			}
			if _, duplicate := knownColumns[column.Name]; duplicate {
				return nil, errors.New("legacy column manifest has a duplicate column")
			}
			knownColumns[column.Name] = struct{}{}
		}
		for _, index := range table.Indexes {
			if len(index.Columns) == 0 {
				return nil, errors.New("legacy column manifest has an empty index")
			}
			for _, column := range index.Columns {
				if _, exists := knownColumns[column]; !exists {
					return nil, errors.New("legacy column manifest index references an unknown column")
				}
			}
		}
	}
	return tables, nil
}

func parsePostgresBool(value string) (bool, error) {
	switch value {
	case "t":
		return true, nil
	case "f":
		return false, nil
	default:
		return false, errors.New("invalid PostgreSQL boolean")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneIndexes(input []Index) []Index {
	result := make([]Index, len(input))
	for index, item := range input {
		result[index] = Index{Primary: item.Primary, Columns: append([]string(nil), item.Columns...)}
	}
	return result
}
