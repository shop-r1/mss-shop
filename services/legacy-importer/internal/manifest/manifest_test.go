package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestReviewedManifestIsExactAndSafe(t *testing.T) {
	digest := sha256.Sum256(reviewedColumnsCSV)
	if got := hex.EncodeToString(digest[:]); got != ReviewedColumnsSHA256 {
		t.Fatalf("reviewed manifest digest = %s, want %s", got, ReviewedColumnsSHA256)
	}

	tables, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(tables) != 51 {
		t.Fatalf("table count = %d, want 51", len(tables))
	}
	columns := 0
	ordersFound := false
	orderGoodsFound := false
	indexedTables := 0
	for _, table := range tables {
		columns += len(table.Columns)
		if len(table.Indexes) > 0 {
			indexedTables++
		}
		if table.Name == OrdersTable {
			ordersFound = true
			if table.CopyRows {
				t.Fatal("orders must be structure-only")
			}
		} else if table.Name == OrderGoodsTable {
			orderGoodsFound = true
			if table.CopyRows {
				t.Fatal("order_goods must be structure-only")
			}
		} else if !table.CopyRows {
			t.Fatalf("unexpected structure-only table %q", table.Name)
		}
		for _, column := range table.Columns {
			if column.HasDefault || column.Identity != "" || column.Generated != "" ||
				column.Compression != "" {
				t.Fatalf("unsafe compiled column %s.%s", table.Name, column.Name)
			}
		}
	}
	if !ordersFound || !orderGoodsFound || columns != 731 || indexedTables != 7 {
		t.Fatalf(
			"manifest totals: orders=%v order_goods=%v columns=%d indexed_tables=%d",
			ordersFound, orderGoodsFound, columns, indexedTables,
		)
	}
}

func TestLoadReturnsIndependentValues(t *testing.T) {
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	first[0].Name = "mutated"
	first[2].Indexes[0].Columns[0] = "mutated"
	second, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Name != "activities" || second[2].Indexes[0].Columns[0] != "id" {
		t.Fatal("Load returned shared mutable manifest state")
	}
}
