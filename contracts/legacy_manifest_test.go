package contracts_test

import (
	"os"
	"regexp"
	"sort"
	"testing"

	"go.yaml.in/yaml/v3"
)

type legacyTableManifest struct {
	Spec struct {
		ExpectedTableCount int `yaml:"expectedTableCount"`
		Tables             []struct {
			Name                      string   `yaml:"name"`
			Owner                     string   `yaml:"owner"`
			TenantScope               string   `yaml:"tenantScope"`
			PrimaryKey                []string `yaml:"primaryKey"`
			Columns                   []string `yaml:"columns"`
			RequiredMigrationEvidence bool     `yaml:"requiredMigrationEvidence"`
		} `yaml:"tables"`
	} `yaml:"spec"`
}

type legacyRebuildStatus struct {
	Spec struct {
		VerifiedInventory struct {
			LegacyTables                int `yaml:"legacyTables"`
			TenantPlatformTables        int `yaml:"tenantPlatformTables"`
			MallPlatformTables          int `yaml:"mallPlatformTables"`
			AdminBusinessViews          int `yaml:"adminBusinessViews"`
			LegacyHTTPRequests          int `yaml:"legacyHTTPRequests"`
			LegacyButtonPermissions     int `yaml:"legacyButtonPermissions"`
			RequiredAcceptanceScenarios int `yaml:"requiredAcceptanceScenarios"`
		} `yaml:"verifiedInventory"`
		Acceptance struct {
			ClosedScenarios int `yaml:"closedScenarios"`
		} `yaml:"acceptance"`
		SourcesOfTruth map[string]string `yaml:"sourcesOfTruth"`
		Safety         struct {
			ProductionReadOnly                     bool `yaml:"productionReadOnly"`
			ProductionOrderRowsToDevelopment       bool `yaml:"productionOrderRowsToDevelopment"`
			SystemTestsUseDisposableKubernetesPods bool `yaml:"systemTestsUseDisposableKubernetesPods"`
		} `yaml:"safety"`
	} `yaml:"spec"`
}

func TestLegacyTableManifestIsTheCompleteCompatibilityBoundary(t *testing.T) {
	content, err := os.ReadFile("../docs/migration/legacy-tables.yaml")
	if err != nil {
		t.Fatalf("read legacy table manifest: %v", err)
	}
	var manifest legacyTableManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("parse legacy table manifest: %v", err)
	}
	if manifest.Spec.ExpectedTableCount != 54 {
		t.Fatalf("expected table count = %d, want 54", manifest.Spec.ExpectedTableCount)
	}
	if len(manifest.Spec.Tables) != manifest.Spec.ExpectedTableCount {
		t.Fatalf("manifest tables = %d, want %d", len(manifest.Spec.Tables), manifest.Spec.ExpectedTableCount)
	}

	names := make([]string, 0, len(manifest.Spec.Tables))
	seen := make(map[string]struct{}, len(manifest.Spec.Tables))
	ownerCounts := map[string]int{}
	shippingWarehouseCovered := false
	for _, table := range manifest.Spec.Tables {
		if table.Name == "" || len(table.PrimaryKey) == 0 || len(table.Columns) == 0 {
			t.Fatalf("incomplete legacy table contract: %#v", table)
		}
		if _, duplicate := seen[table.Name]; duplicate {
			t.Fatalf("duplicate legacy table %q", table.Name)
		}
		seen[table.Name] = struct{}{}
		names = append(names, table.Name)
		ownerCounts[table.Owner]++

		columns := make(map[string]struct{}, len(table.Columns))
		for _, column := range table.Columns {
			if _, duplicate := columns[column]; duplicate {
				t.Fatalf("table %q has duplicate column %q", table.Name, column)
			}
			columns[column] = struct{}{}
		}
		for _, key := range table.PrimaryKey {
			if _, exists := columns[key]; !exists {
				t.Fatalf("table %q primary key %q is not a declared column", table.Name, key)
			}
		}
		if table.Name == "shipping_warehouses" {
			shippingWarehouseCovered = table.RequiredMigrationEvidence && len(table.Columns) == 16
		}
	}

	if _, exists := seen["area"]; exists {
		t.Fatal("area is a tool-owned model, not one of the 54 business tables")
	}
	if _, exists := seen["order_operate_logs"]; exists {
		t.Fatal("order_operate_logs has no migrated legacy table contract")
	}
	if !shippingWarehouseCovered {
		t.Fatal("shipping_warehouses must carry explicit migration evidence and all 16 columns")
	}
	if ownerCounts["tenant-platform"] != 11 || ownerCounts["mall-platform"] != 43 {
		t.Fatalf("owner split = %#v, want tenant-platform=11 mall-platform=43", ownerCounts)
	}

	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for index := range names {
		if names[index] != sorted[index] {
			t.Fatalf("legacy tables are not sorted at index %d: %q before %q", index, names[index], sorted[index])
		}
	}
}

func TestLegacyRebuildMemoryStaysAlignedWithExecutableContracts(t *testing.T) {
	content, err := os.ReadFile("../docs/project/legacy-rebuild-status.yaml")
	if err != nil {
		t.Fatalf("read legacy rebuild status: %v", err)
	}
	var status legacyRebuildStatus
	if err := yaml.Unmarshal(content, &status); err != nil {
		t.Fatalf("parse legacy rebuild status: %v", err)
	}

	inventory := status.Spec.VerifiedInventory
	if inventory.LegacyTables != 54 || inventory.TenantPlatformTables != 11 || inventory.MallPlatformTables != 43 {
		t.Fatalf("legacy owner inventory drifted: %#v", inventory)
	}
	if inventory.AdminBusinessViews != 54 || inventory.LegacyHTTPRequests != 185 || inventory.LegacyButtonPermissions != 171 {
		t.Fatalf("legacy Admin inventory drifted: %#v", inventory)
	}

	matrix, err := os.ReadFile("../docs/migration/legacy-admin-acceptance-matrix.md")
	if err != nil {
		t.Fatalf("read legacy acceptance matrix: %v", err)
	}
	scenarioPattern := regexp.MustCompile(`(?m)^\| [A-Z0-9]+-[0-9]{3} \|`)
	if actual := len(scenarioPattern.FindAll(matrix, -1)); actual != inventory.RequiredAcceptanceScenarios {
		t.Fatalf("acceptance scenarios = %d, status records %d", actual, inventory.RequiredAcceptanceScenarios)
	}
	if status.Spec.Acceptance.ClosedScenarios != 0 {
		t.Fatalf("closed acceptance scenarios = %d without system evidence", status.Spec.Acceptance.ClosedScenarios)
	}

	requiredSources := []string{
		"architecture", "invariants", "decision", "dataContract", "tableManifest",
		"acceptanceMatrix", "implementationSkill", "deliveryChecklist",
		"memoryGovernance", "memoryCheck", "mcpIntegration", "mcpConfiguration",
		"mss137GenerationNotes", "localBrowserAcceptance",
	}
	for _, name := range requiredSources {
		if _, exists := status.Spec.SourcesOfTruth[name]; !exists {
			t.Errorf("required source of truth %q is not registered", name)
		}
	}
	for name, source := range status.Spec.SourcesOfTruth {
		if source == "" {
			t.Fatalf("source of truth %q is empty", name)
		}
		info, err := os.Stat("../" + source)
		if err != nil {
			t.Fatalf("source of truth %q (%s): %v", name, source, err)
		}
		if name == "memoryCheck" && info.Mode()&0o111 == 0 {
			t.Fatalf("memory check %q is not executable", source)
		}
	}
	if !status.Spec.Safety.ProductionReadOnly || status.Spec.Safety.ProductionOrderRowsToDevelopment || !status.Spec.Safety.SystemTestsUseDisposableKubernetesPods {
		t.Fatalf("unsafe legacy verification memory: %#v", status.Spec.Safety)
	}
}

func TestMallAdminCatalogMatchesTheVersionedTableManifest(t *testing.T) {
	content, err := os.ReadFile("../docs/migration/legacy-tables.yaml")
	if err != nil {
		t.Fatalf("read legacy table manifest: %v", err)
	}
	var manifest legacyTableManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("parse legacy table manifest: %v", err)
	}
	want := make(map[string]struct{}, 43)
	for _, table := range manifest.Spec.Tables {
		if table.Owner == "mall-platform" {
			want[table.Name] = struct{}{}
		}
	}
	if len(want) != 43 {
		t.Fatalf("mall manifest resources = %d, want 43", len(want))
	}

	catalog, err := os.ReadFile("../apps/mall-platform/web/src/business/legacy/catalog.ts")
	if err != nil {
		t.Fatalf("read mall legacy frontend catalog: %v", err)
	}
	declaration := regexp.MustCompile(`resource\('[^']+', '([^']+)'\)`)
	matches := declaration.FindAllSubmatch(catalog, -1)
	got := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := string(match[1])
		if _, duplicate := got[name]; duplicate {
			t.Fatalf("duplicate mall frontend resource %q", name)
		}
		got[name] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("mall frontend resources = %d, manifest records %d", len(got), len(want))
	}
	for name := range want {
		if _, exists := got[name]; !exists {
			t.Errorf("mall frontend catalog is missing manifest resource %q", name)
		}
	}
	for name := range got {
		if _, exists := want[name]; !exists {
			t.Errorf("mall frontend catalog contains non-mall resource %q", name)
		}
	}
}

func TestTenantSharedCatalogMatchesTheVersionedTableManifest(t *testing.T) {
	content, err := os.ReadFile("../docs/migration/legacy-tables.yaml")
	if err != nil {
		t.Fatalf("read legacy table manifest: %v", err)
	}
	var manifest legacyTableManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("parse legacy table manifest: %v", err)
	}
	want := make(map[string]struct{}, 8)
	for _, table := range manifest.Spec.Tables {
		if table.Owner == "tenant-platform" && table.TenantScope == "global" {
			want[table.Name] = struct{}{}
		}
	}
	if len(want) != 8 {
		t.Fatalf("tenant shared manifest resources = %d, want 8", len(want))
	}

	catalog, err := os.ReadFile("../apps/tenant-platform/web/src/business/shared-catalog/catalog.ts")
	if err != nil {
		t.Fatalf("read tenant shared frontend catalog: %v", err)
	}
	declaration := regexp.MustCompile(`resource\(["']([^"']+)["'], (true|false)\)`)
	matches := declaration.FindAllSubmatch(catalog, -1)
	got := make(map[string]struct{}, len(matches))
	writable := make(map[string]struct{})
	for _, match := range matches {
		name := string(match[1])
		if _, duplicate := got[name]; duplicate {
			t.Fatalf("duplicate tenant shared frontend resource %q", name)
		}
		got[name] = struct{}{}
		if string(match[2]) == "true" {
			writable[name] = struct{}{}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("tenant shared frontend resources = %d, manifest records %d", len(got), len(want))
	}
	for name := range want {
		if _, exists := got[name]; !exists {
			t.Errorf("tenant shared frontend catalog is missing manifest resource %q", name)
		}
	}
	for name := range got {
		if _, exists := want[name]; !exists {
			t.Errorf("tenant shared frontend catalog contains non-shared resource %q", name)
		}
	}
	if len(writable) != 0 {
		t.Fatalf("tenant shared catalog exposes generic mutation before legacy workflows exist: %#v", writable)
	}
}
