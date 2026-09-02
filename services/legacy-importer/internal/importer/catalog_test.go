package importer

import (
	"sort"
	"strings"
	"testing"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/manifest"
)

func TestValidateSourceCatalogAcceptsOnlyExactReviewedShape(t *testing.T) {
	tables := mustManifest(t)
	catalog := safeSourceCatalog(tables)
	if err := validateSourceCatalog(catalog, tables); err != nil {
		t.Fatalf("validateSourceCatalog() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*sourceCatalog)
	}{
		{name: "unexpected table", mutate: func(c *sourceCatalog) {
			c.Relations = append(c.Relations, safeRelation("shadow_table"))
		}},
		{name: "custom type", mutate: func(c *sourceCatalog) {
			c.Columns["activities"][0].TypeNamespace = "public"
		}},
		{name: "default", mutate: func(c *sourceCatalog) {
			c.Columns["activities"][0].HasDefault = true
			c.Columns["activities"][0].DefaultExpression = "dangerous()"
		}},
		{name: "generated", mutate: func(c *sourceCatalog) {
			c.Columns["activities"][0].Generated = "s"
		}},
		{name: "RLS", mutate: func(c *sourceCatalog) {
			c.Relations[0].RowSecurity = true
		}},
		{name: "trigger", mutate: func(c *sourceCatalog) {
			c.Relations[0].Triggers = 1
		}},
		{name: "rule", mutate: func(c *sourceCatalog) {
			c.Relations[0].Rules = 1
		}},
		{name: "inheritance", mutate: func(c *sourceCatalog) {
			c.Relations[0].InheritanceEdges = 1
		}},
		{name: "public routine", mutate: func(c *sourceCatalog) {
			c.UnreviewedPublicRoutines = 1
		}},
		{name: "standalone type", mutate: func(c *sourceCatalog) {
			c.StandaloneTypes = 1
		}},
		{name: "reviewed routine count", mutate: func(c *sourceCatalog) {
			c.ReviewedPublicRoutines--
		}},
		{name: "additional reviewed routine", mutate: func(c *sourceCatalog) {
			c.ReviewedPublicRoutines++
		}},
		{name: "reviewed routine fingerprint", mutate: func(c *sourceCatalog) {
			c.ReviewedRoutineSHA256 = strings.Repeat("0", 64)
		}},
		{name: "source extension", mutate: func(c *sourceCatalog) {
			c.Extensions[1] = "timescaledb|2.20.1|public"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := safeSourceCatalog(tables)
			test.mutate(&candidate)
			if err := validateSourceCatalog(candidate, tables); err == nil {
				t.Fatal("validateSourceCatalog() accepted unsafe catalog")
			}
		})
	}
}

func TestSourceExecutableInventoryExemptsOnlyReviewedTimescaleDBObjects(t *testing.T) {
	for _, expected := range []string{
		"dependency.classid = 'pg_catalog.pg_proc'::pg_catalog.regclass",
		"dependency.objid = routine.oid",
		"dependency.objsubid = 0",
		"dependency.refclassid = 'pg_catalog.pg_extension'::pg_catalog.regclass",
		"dependency.refobjsubid = 0",
		"extension.oid = dependency.refobjid",
		"dependency.deptype = 'e'",
		"extension.extname = 'timescaledb'",
		"extension.extversion = '2.20.2'",
		"extension_namespace.nspname = 'public'",
	} {
		if count := strings.Count(sourceExecutableObjectInventorySQL, expected); count != 1 {
			t.Fatalf("source executable inventory contains %d copies of %q, want 1", count, expected)
		}
	}
	if !strings.Contains(sourceExecutableObjectInventorySQL, "pg_catalog.jsonb_agg(pg_catalog.to_jsonb(reviewed_routine)") {
		t.Fatal("source executable inventory lacks the complete routine catalog fingerprint")
	}
	if strings.Contains(sourceExecutableObjectInventorySQL, "dependency.classid = 'pg_catalog.pg_type'") {
		t.Fatal("source executable inventory exempts extension-owned standalone types")
	}
	if expectedSourceRoutineCount != 91 || len(expectedSourceRoutineSHA256) != 64 ||
		expectedSourceRoutineSHA256 == strings.Repeat("0", 64) {
		t.Fatal("reviewed source routine binding is incomplete")
	}
}

func TestSourceColumnInventorySQLUsesUnambiguousCollationAlias(t *testing.T) {
	for _, expected := range []string{
		"pg_catalog.pg_collation AS collation_record",
		"collation_record.collname",
		"collation_record.collprovider",
		"collation_record.collisdeterministic",
		"collation_record.collencoding",
		"collation_record.collnamespace",
	} {
		if !strings.Contains(sourceColumnInventorySQL, expected) {
			t.Fatalf("sourceColumnInventorySQL is missing %q", expected)
		}
	}
	for _, ambiguous := range []string{"AS collation ", "collation."} {
		if strings.Contains(sourceColumnInventorySQL, ambiguous) {
			t.Fatalf("sourceColumnInventorySQL contains ambiguous alias %q", ambiguous)
		}
	}
}

func TestValidateTargetBoundaryRequiresFreshIsolatedDatabase(t *testing.T) {
	safe := safeTargetBoundary()
	if err := validateTargetBoundary(safe); err != nil {
		t.Fatalf("validateTargetBoundary() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*targetBoundary)
	}{
		{"wrong database", func(b *targetBoundary) { b.DatabaseName = "r1shop" }},
		{"not TLS", func(b *targetBoundary) { b.SSL = false }},
		{"wrong marker", func(b *targetBoundary) { b.Marker = importedMarkerPrefix + strings.Repeat("0", 64) }},
		{"user object", func(b *targetBoundary) { b.UserObjects = 1 }},
		{"PUBLIC privilege", func(b *targetBoundary) { b.PublicSchemaPrivileges = 1 }},
		{"foreign public owner", func(b *targetBoundary) { b.PublicSchemaOwnerCurrent = false }},
		{"event trigger", func(b *targetBoundary) { b.EventTriggers = 1 }},
		{"extra extension", func(b *targetBoundary) { b.Extensions = []string{"plpgsql", "unsafe"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := safe
			candidate.Extensions = append([]string(nil), safe.Extensions...)
			test.mutate(&candidate)
			if err := validateTargetBoundary(candidate); err == nil {
				t.Fatal("validateTargetBoundary() accepted unsafe database")
			}
		})
	}
}

func safeSourceCatalog(tables []manifest.Table) sourceCatalog {
	names := manifest.ImportNames()
	names = append(names, manifest.SourceIdentityNames()...)
	sort.Strings(names)
	catalog := sourceCatalog{
		Relations:              make([]sourceRelation, 0, len(names)),
		Columns:                make(map[string][]manifest.Column, len(tables)),
		Extensions:             []string{"plpgsql|1.0|pg_catalog", "timescaledb|2.20.2|public"},
		ReviewedPublicRoutines: expectedSourceRoutineCount,
		ReviewedRoutineSHA256:  expectedSourceRoutineSHA256,
	}
	for _, name := range names {
		catalog.Relations = append(catalog.Relations, safeRelation(name))
	}
	for _, table := range tables {
		catalog.Columns[table.Name] = append([]manifest.Column(nil), table.Columns...)
	}
	return catalog
}

func safeRelation(name string) sourceRelation {
	return sourceRelation{Name: name, Kind: "r", Persistence: "p", AccessMethod: "heap"}
}

func safeTargetBoundary() targetBoundary {
	return targetBoundary{
		ServerVersion:            expectedTargetPG,
		EventTriggersDisabled:    true,
		SSL:                      true,
		DatabaseName:             targetDatabase,
		SessionIdentityExact:     true,
		DatabaseOwnerCurrent:     true,
		CurrentRoleSuperuser:     true,
		Marker:                   emptyDatabaseMarker,
		PublicSchemaOwnerCurrent: true,
		Extensions:               []string{"plpgsql"},
	}
}

func mustManifest(t *testing.T) []manifest.Table {
	t.Helper()
	tables, err := manifest.Load()
	if err != nil {
		t.Fatalf("manifest.Load() error = %v", err)
	}
	return tables
}
