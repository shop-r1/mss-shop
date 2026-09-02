package importer

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type shortReceiptWriter struct{}

func (shortReceiptWriter) Write(value []byte) (int, error) {
	return len(value) - 1, nil
}

func TestWriteCompleteReceiptRejectsSilentShortWrite(t *testing.T) {
	t.Parallel()
	encoded := []byte("{\"sha256\":\"receipt\"}\n")
	var complete bytes.Buffer
	if err := writeCompleteReceipt(&complete, encoded); err != nil || !bytes.Equal(complete.Bytes(), encoded) {
		t.Fatalf("complete receipt write failed: %v", err)
	}
	if err := writeCompleteReceipt(shortReceiptWriter{}, encoded); err == nil {
		t.Fatal("silent short receipt write was accepted")
	}
}

func TestReceiptIsDeterministicCountOnlyAndOrdersAreEmpty(t *testing.T) {
	tables := mustManifest(t)
	evidence := make(map[string]tableEvidence, len(tables))
	for index, table := range tables {
		rows := int64(index + 1)
		targetRows := rows
		if !table.CopyRows {
			targetRows = 0
		}
		targetHash := strings.Repeat("a", 64)
		if !table.CopyRows {
			targetHash = strings.Repeat("b", 64)
		}
		evidence[table.Name] = tableEvidence{
			SourceRows: rows, TargetRows: targetRows,
			SourceSHA256: strings.Repeat("a", 64), TargetSHA256: targetHash,
		}
	}
	first, err := buildReceipt(tables, evidence)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildReceipt(tables, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.SHA256) != 64 || len(first.SchemaSHA256) != 64 {
		t.Fatalf("receipts are not deterministic: %#v %#v", first, second)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "postgres://", "business-row-value"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("receipt contains forbidden value %q", forbidden)
		}
	}
	orders := first.Tables[37]
	if orders.Name != "orders" || orders.Mode != "structure-only" || orders.TargetRows != 0 {
		t.Fatalf("orders receipt = %#v", orders)
	}
	orderGoods := first.Tables[35]
	if orderGoods.Name != "order_goods" || orderGoods.Mode != "structure-only" || orderGoods.TargetRows != 0 {
		t.Fatalf("order_goods receipt = %#v", orderGoods)
	}
}

func TestReceiptRejectsCopiedCountMismatch(t *testing.T) {
	tables := mustManifest(t)
	evidence := make(map[string]tableEvidence, len(tables))
	for _, table := range tables {
		evidence[table.Name] = tableEvidence{
			SourceSHA256: strings.Repeat("a", 64),
			TargetSHA256: strings.Repeat("a", 64),
		}
	}
	evidence["activities"] = tableEvidence{
		SourceRows: 1, TargetRows: 0,
		SourceSHA256: strings.Repeat("a", 64), TargetSHA256: strings.Repeat("a", 64),
	}
	if _, err := buildReceipt(tables, evidence); err == nil {
		t.Fatal("buildReceipt() accepted mismatched copied counts")
	}
}

func TestReceiptRejectsNonCanonicalSHA256(t *testing.T) {
	tables := mustManifest(t)
	evidence := make(map[string]tableEvidence, len(tables))
	for _, table := range tables {
		evidence[table.Name] = tableEvidence{
			SourceSHA256: strings.Repeat("a", 64),
			TargetSHA256: strings.Repeat("a", 64),
		}
	}
	evidence["activities"] = tableEvidence{
		SourceSHA256: strings.Repeat("A", 64),
		TargetSHA256: strings.Repeat("A", 64),
	}
	if _, err := buildReceipt(tables, evidence); err == nil {
		t.Fatal("buildReceipt() accepted uppercase SHA-256 evidence")
	}
}
