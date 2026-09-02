package importer

import (
	"strings"
	"testing"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/manifest"
)

func TestCreateTableStatementsUseOnlyCompiledLiteralDDL(t *testing.T) {
	tables := mustManifest(t)
	statements, err := createTableStatements(tables)
	if err != nil {
		t.Fatalf("createTableStatements() error = %v", err)
	}
	if len(statements) != 102 {
		t.Fatalf("statement count = %d, want 102", len(statements))
	}
	joined := make([]string, 0, len(statements))
	for _, item := range statements {
		joined = append(joined, item.SQL)
	}
	sql := strings.Join(joined, "\n")
	for _, forbidden := range []string{" DEFAULT ", " GENERATED ", " IDENTITY ", "CREATE TRIGGER", "CREATE RULE"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("generated DDL contains %q", forbidden)
		}
	}
	if !strings.Contains(sql, `CREATE TABLE "public"."orders"`) {
		t.Fatal("orders structure is missing")
	}
}

func TestCopySQLUsesOnlyAndBinaryFormat(t *testing.T) {
	table := manifest.Table{Name: "safe", Columns: []manifest.Column{{Name: "id"}, {Name: "payload"}}}
	if got := sourceCopySQL(table); got != `COPY (SELECT "id", "payload" FROM ONLY "public"."safe") TO STDOUT (FORMAT binary)` {
		t.Fatalf("sourceCopySQL() = %q", got)
	}
	if got := targetCopySQL(table); got != `COPY "public"."safe" ("id", "payload") FROM STDIN (FORMAT binary)` {
		t.Fatalf("targetCopySQL() = %q", got)
	}
}

func TestOnlySevenReviewedTablesReceiveIndexes(t *testing.T) {
	statements, err := createIndexStatements(mustManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 18 {
		t.Fatalf("index statement count = %d, want 18", len(statements))
	}
	for _, item := range statements {
		if strings.Contains(item.SQL, `"public"."orders"`) {
			t.Fatal("orders unexpectedly received an index")
		}
	}
}
