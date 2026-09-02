package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shop-r1/mss-shop/services/internal/legacyreceipt"
	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/manifest"
)

func verifiedReceipt(t *testing.T) legacyreceipt.Receipt {
	t.Helper()
	tables, err := manifest.Load()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(tables)
	if err != nil {
		t.Fatal(err)
	}
	schema := sha256.Sum256(encoded)
	receipt := legacyreceipt.Receipt{Version: legacyreceipt.Version, TargetDatabase: postgresDatabase, ManifestSHA256: manifest.ReviewedColumnsSHA256, SchemaSHA256: hex.EncodeToString(schema[:]), Tables: make([]legacyreceipt.Table, 0, len(tables))}
	for _, table := range tables {
		mode := "copied"
		if !table.CopyRows {
			mode = "structure-only"
		}
		receipt.Tables = append(receipt.Tables, legacyreceipt.Table{Name: table.Name, Mode: mode, SourceSHA256: strings.Repeat("a", 64), TargetSHA256: strings.Repeat("a", 64)})
	}
	payload, err := json.Marshal(struct {
		Version        string                `json:"version"`
		TargetDatabase string                `json:"targetDatabase"`
		ManifestSHA256 string                `json:"manifestSHA256"`
		SchemaSHA256   string                `json:"schemaSHA256"`
		Tables         []legacyreceipt.Table `json:"tables"`
	}{receipt.Version, receipt.TargetDatabase, receipt.ManifestSHA256, receipt.SchemaSHA256, receipt.Tables})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	receipt.SHA256 = hex.EncodeToString(sum[:])
	return receipt
}

func TestValidateReceiptRequiresCompiled51TableManifest(t *testing.T) {
	t.Parallel()
	tables, err := manifest.Load()
	if err != nil {
		t.Fatal(err)
	}
	receipt := verifiedReceipt(t)
	if err := validateReceipt(receipt, tables); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}
	receipt.Tables[0], receipt.Tables[1] = receipt.Tables[1], receipt.Tables[0]
	if err := validateReceipt(receipt, tables); err == nil {
		t.Fatal("out of order receipt accepted")
	}
}

func TestVerifierJSONNeverIncludesReceiptPayload(t *testing.T) {
	t.Parallel()
	receipt := verifiedReceipt(t)
	output, err := json.Marshal(successRecord{Version: "mss-shop-disposable-verification/v1", TargetDatabase: postgresDatabase, DatabaseMarker: markerPrefix + receipt.SHA256, ReceiptSHA256: receipt.SHA256, ManifestSHA256: receipt.ManifestSHA256, SchemaSHA256: receipt.SchemaSHA256, TableCount: 51, Namespace: namespace, PodName: "mss-shop-import-verifier", PodUID: "01234567-89ab-cdef-0123-456789abcdef", Revision: strings.Repeat("a", 40), ImageRepository: imageRepository, ImageDigest: "sha256:" + strings.Repeat("b", 64), ImageReference: imageRepository + ":" + strings.Repeat("a", 40) + "@sha256:" + strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"tableCount":51`) || !strings.Contains(string(output), `"imageReference":"`+imageRepository) || strings.Contains(string(output), "sourceSHA256") || strings.Contains(string(output), "postgres://") {
		t.Fatalf("unsafe verifier output: %s", output)
	}
}

func TestVerifierFailureRecordCannotBeMistakenForEvidence(t *testing.T) {
	t.Parallel()
	output, err := json.Marshal(failureRecord{Version: "mss-shop-disposable-verification-failure/v1", Verified: false, Failure: "receipt"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), `"version":"mss-shop-disposable-verification/v1"`) || strings.Contains(string(output), "receiptSHA256") {
		t.Fatalf("failure record resembles evidence: %s", output)
	}
}

func TestVerifierRejectsZeroBindingsAndShortPodUID(t *testing.T) {
	t.Parallel()
	if nonZeroRevision(strings.Repeat("0", 40)) || nonZeroImageDigest("sha256:"+strings.Repeat("0", 64)) || nonZeroReceiptSHA(strings.Repeat("0", 64)) {
		t.Fatal("zero binding accepted")
	}
	if !podUID.MatchString("01234567-89ab-cdef-0123-456789abcdef") {
		t.Fatalf("real pod UID rejected by %q", podUID.String())
	}
	if podUID.MatchString("01234567-89ab-cdef") {
		t.Fatal("short pod UID accepted")
	}
}

func TestValidateTargetSchemaRejectsColumnAndIndexDrift(t *testing.T) {
	t.Parallel()
	tables, err := manifest.Load()
	if err != nil {
		t.Fatal(err)
	}
	actual := matchingTargetSchema(tables)
	if err := validateTargetSchema(actual, tables); err != nil {
		t.Fatalf("matching target schema rejected: %v", err)
	}
	actual = matchingTargetSchema(tables)
	actual.Columns[tables[0].Name][0].NotNull = !actual.Columns[tables[0].Name][0].NotNull
	if err := validateTargetSchema(actual, tables); err == nil {
		t.Fatal("column drift accepted")
	}
	actual = matchingTargetSchema(tables)
	actual.Indexes = append(actual.Indexes, targetIndex{Table: tables[0].Name, Name: "unreviewed", Reviewed: true})
	if err := validateTargetSchema(actual, tables); err == nil {
		t.Fatal("extra index accepted")
	}
	actual = matchingTargetSchema(tables)
	actual.Constraints = append(actual.Constraints, targetConstraint{Table: tables[0].Name, Name: "unreviewed", Type: "c"})
	if err := validateTargetSchema(actual, tables); err == nil {
		t.Fatal("extra constraint accepted")
	}
}

func matchingTargetSchema(tables []manifest.Table) targetSchema {
	result := targetSchema{Columns: make(map[string][]manifest.Column, len(tables))}
	for _, table := range tables {
		result.Columns[table.Name] = append([]manifest.Column(nil), table.Columns...)
		for position, index := range table.Indexes {
			name := "mss_import_" + table.Name + "_" + twoDigits(position) + "_idx"
			if index.Primary {
				name = "mss_import_" + table.Name + "_pkey"
				result.Constraints = append(result.Constraints, targetConstraint{Table: table.Name, Name: name, Type: "p"})
			}
			result.Indexes = append(result.Indexes, targetIndex{Table: table.Name, Name: name, Primary: index.Primary, Unique: index.Primary, Columns: append([]string(nil), index.Columns...), Reviewed: true})
		}
	}
	return result
}

func TestCopySQLUsesOnlyCompiledNames(t *testing.T) {
	t.Parallel()
	tables, err := manifest.Load()
	if err != nil {
		t.Fatal(err)
	}
	sql := copySQL(tables[0])
	if !strings.HasPrefix(sql, "COPY (SELECT ") || !strings.Contains(sql, `FROM ONLY "public"."activities"`) || strings.Contains(sql, ";") {
		t.Fatalf("unexpected COPY SQL: %s", sql)
	}
}

func TestCatalogInventorySQLAvoidsPostgreSQLKeywordsAsAliases(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"columns":     targetColumnInventorySQL,
		"constraints": targetConstraintInventorySQL,
	} {
		t.Run(name, func(t *testing.T) {
			for _, ambiguous := range []string{
				"AS collation ",
				"collation.",
				"AS constraint ",
				"constraint.",
			} {
				if strings.Contains(query, ambiguous) {
					t.Fatalf("inventory SQL contains ambiguous alias %q", ambiguous)
				}
			}
		})
	}
	if !strings.Contains(targetColumnInventorySQL, "AS collation_record") ||
		!strings.Contains(targetConstraintInventorySQL, "AS constraint_record") {
		t.Fatal("inventory SQL is missing explicit catalog aliases")
	}
}

func TestReadReceiptAllowsProjectedVolumeStyleLinkOnlyInsideEvidenceDirectory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "..data", "receipt.json")
	if err := os.Mkdir(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{}`), 0o444); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "receipt.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if encoded, err := readReceipt(path); err != nil || string(encoded) != `{}` {
		t.Fatalf("projected receipt read = %q, %v", encoded, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "outside.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := readReceipt(filepath.Join(directory, "outside.json")); err == nil {
		t.Fatal("receipt symlink escaping evidence directory was accepted")
	}
}
