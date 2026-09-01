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
		Rules              struct {
			TargetMallCompatibilityMutations   string `yaml:"targetMallCompatibilityMutations"`
			TargetTenantCompatibilityMutations string `yaml:"targetTenantCompatibilityMutations"`
			SourceRuntimeProjection            string `yaml:"sourceRuntimeProjection"`
			OwnershipDeployment                string `yaml:"ownershipDeployment"`
		} `yaml:"rules"`
		Tables []struct {
			Name                      string   `yaml:"name"`
			Owner                     string   `yaml:"owner"`
			Domain                    string   `yaml:"domain"`
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
			LegacyTables                 int `yaml:"legacyTables"`
			TenantPlatformTables         int `yaml:"tenantPlatformTables"`
			MallPlatformTables           int `yaml:"mallPlatformTables"`
			CompatibilityResources       int `yaml:"compatibilityResources"`
			TenantCompatibilityResources int `yaml:"tenantPlatformCompatibilityResources"`
			MallCompatibilityResources   int `yaml:"mallPlatformCompatibilityResources"`
			AdminBusinessViews           int `yaml:"adminBusinessViews"`
			LegacyHTTPRequests           int `yaml:"legacyHTTPRequests"`
			LegacyButtonPermissions      int `yaml:"legacyButtonPermissions"`
			RequiredAcceptanceScenarios  int `yaml:"requiredAcceptanceScenarios"`
		} `yaml:"verifiedInventory"`
		Acceptance struct {
			ClosedScenarios int `yaml:"closedScenarios"`
		} `yaml:"acceptance"`
		EvidenceState struct {
			CompatibilityOwnershipReassignment string `yaml:"compatibilityOwnershipReassignment"`
		} `yaml:"evidenceState"`
		LocalEvidence struct {
			MallAdminWeb struct {
				ResourceRoutes      int    `yaml:"resourceRoutes"`
				OwnershipDeployment string `yaml:"ownershipDeployment"`
			} `yaml:"mallAdminWeb"`
			TenantAdminWeb struct {
				ResourceRoutes      int    `yaml:"resourceRoutes"`
				TargetResource      string `yaml:"targetResource"`
				OwnershipDeployment string `yaml:"ownershipDeployment"`
			} `yaml:"tenantAdminWeb"`
		} `yaml:"localEvidence"`
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
	if ownerCounts["tenant-platform"] != 4 || ownerCounts["mall-platform"] != 50 {
		t.Fatalf("owner split = %#v, want tenant-platform=4 mall-platform=50", ownerCounts)
	}
	rules := manifest.Spec.Rules
	if rules.TargetMallCompatibilityMutations != "read-only-50-resources" ||
		rules.TargetTenantCompatibilityMutations != "read-only-1-payment-resource" ||
		rules.SourceRuntimeProjection != "read-only-50-mall-1-tenant-payment" ||
		rules.OwnershipDeployment != "pending-forward-migrations-not-applied" {
		t.Fatalf("legacy compatibility safety rules drifted: %#v", rules)
	}

	wantTenantOwned := map[string]string{
		"brands":             "catalog_master",
		"categories":         "catalog_master",
		"classes":            "catalog_master",
		"goods_infos":        "catalog_master",
		"couriers":           "logistics_rule",
		"courier_pack_rules": "logistics_rule",
		"courier_links":      "logistics_rule",
	}
	for _, table := range manifest.Spec.Tables {
		domain, moved := wantTenantOwned[table.Name]
		if !moved {
			continue
		}
		if table.Owner != "mall-platform" || table.Domain != domain || table.TenantScope != "tenant" {
			t.Errorf("tenant-owned product/logistics contract for %q = owner %q domain %q scope %q", table.Name, table.Owner, table.Domain, table.TenantScope)
		}
		delete(wantTenantOwned, table.Name)
	}
	if len(wantTenantOwned) != 0 {
		t.Fatalf("tenant-owned product/logistics tables missing from manifest: %#v", wantTenantOwned)
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
	if inventory.LegacyTables != 54 || inventory.TenantPlatformTables != 4 || inventory.MallPlatformTables != 50 {
		t.Fatalf("legacy owner inventory drifted: %#v", inventory)
	}
	if inventory.CompatibilityResources != 51 || inventory.TenantCompatibilityResources != 1 || inventory.MallCompatibilityResources != 50 {
		t.Fatalf("legacy compatibility allocation drifted: %#v", inventory)
	}
	if status.Spec.EvidenceState.CompatibilityOwnershipReassignment != "implemented-source-not-deployed" {
		t.Fatalf("ownership reassignment state = %q, want source-only implementation", status.Spec.EvidenceState.CompatibilityOwnershipReassignment)
	}
	if status.Spec.LocalEvidence.MallAdminWeb.ResourceRoutes != 50 || status.Spec.LocalEvidence.MallAdminWeb.OwnershipDeployment != "pending-forward-migrations-not-applied" {
		t.Fatalf("mall source/deployment state drifted: %#v", status.Spec.LocalEvidence.MallAdminWeb)
	}
	if status.Spec.LocalEvidence.TenantAdminWeb.ResourceRoutes != 1 || status.Spec.LocalEvidence.TenantAdminWeb.TargetResource != "payments" || status.Spec.LocalEvidence.TenantAdminWeb.OwnershipDeployment != "pending-forward-migrations-not-applied" {
		t.Fatalf("tenant source/deployment state drifted: %#v", status.Spec.LocalEvidence.TenantAdminWeb)
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
		"architecture", "invariants", "decision", "qualificationDecision", "dataContract", "tableManifest",
		"acceptanceMatrix", "implementationSkill", "deliveryChecklist",
		"memoryGovernance", "memoryCheck", "mcpIntegration", "mcpConfiguration",
		"mss137GenerationNotes", "localBrowserAcceptance",
		"remoteDevelopmentDecision", "remoteDevelopmentRunbook", "catalogLogisticsDesignReview",
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
	if status.Spec.SourcesOfTruth["decision"] != "docs/decisions/DEC-0009-tenant-owned-catalog-and-logistics.md" ||
		status.Spec.SourcesOfTruth["qualificationDecision"] != "docs/decisions/DEC-0007-qualified-legacy-business-contract.md" {
		t.Fatalf("legacy decisions drifted: %#v", status.Spec.SourcesOfTruth)
	}
	if !status.Spec.Safety.ProductionReadOnly || status.Spec.Safety.ProductionOrderRowsToDevelopment || !status.Spec.Safety.SystemTestsUseDisposableKubernetesPods {
		t.Fatalf("unsafe legacy verification memory: %#v", status.Spec.Safety)
	}
}

func TestRuntimeCatalogsMatchTheDeclaredOwnership(t *testing.T) {
	content, err := os.ReadFile("../docs/migration/legacy-tables.yaml")
	if err != nil {
		t.Fatalf("read legacy table manifest: %v", err)
	}
	var manifest legacyTableManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("parse legacy table manifest: %v", err)
	}
	targetMall := make(map[string]struct{}, 50)
	targetTenant := make(map[string]struct{}, 1)
	for _, table := range manifest.Spec.Tables {
		if table.Owner == "mall-platform" {
			targetMall[table.Name] = struct{}{}
		}
		if table.Owner == "tenant-platform" && table.TenantScope == "global" {
			targetTenant[table.Name] = struct{}{}
		}
	}
	if len(targetMall) != 50 || len(targetTenant) != 1 {
		t.Fatalf("target compatibility allocation = mall %d tenant %d, want 50/1", len(targetMall), len(targetTenant))
	}
	if _, exists := targetTenant["payments"]; !exists {
		t.Fatalf("tenant target compatibility resource = %#v, want payments", targetTenant)
	}

	mallCatalog, err := os.ReadFile("../apps/mall-platform/web/src/business/legacy/catalog.ts")
	if err != nil {
		t.Fatalf("read mall legacy frontend catalog: %v", err)
	}
	mallDeclaration := regexp.MustCompile(`resource\('[^']+', '([^']+)'\)`)
	matches := mallDeclaration.FindAllSubmatch(mallCatalog, -1)
	currentMall := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := string(match[1])
		if _, duplicate := currentMall[name]; duplicate {
			t.Fatalf("duplicate mall frontend resource %q", name)
		}
		currentMall[name] = struct{}{}
	}
	if len(currentMall) != len(targetMall) {
		t.Fatalf("mall frontend resources = %d, manifest target records %d", len(currentMall), len(targetMall))
	}
	for name := range targetMall {
		if _, exists := currentMall[name]; !exists {
			t.Errorf("mall frontend catalog is missing manifest resource %q", name)
		}
	}
	for name := range currentMall {
		if _, exists := targetMall[name]; !exists {
			t.Errorf("mall frontend catalog contains non-mall resource %q", name)
		}
	}

	tenantCatalog, err := os.ReadFile("../apps/tenant-platform/web/src/business/shared-catalog/catalog.ts")
	if err != nil {
		t.Fatalf("read tenant legacy frontend catalog: %v", err)
	}
	tenantDeclaration := regexp.MustCompile(`resource\(["']([^"']+)["'], (true|false)\)`)
	matches = tenantDeclaration.FindAllSubmatch(tenantCatalog, -1)
	currentTenant := make(map[string]struct{}, len(matches))
	writable := make(map[string]struct{})
	for _, match := range matches {
		name := string(match[1])
		if _, duplicate := currentTenant[name]; duplicate {
			t.Fatalf("duplicate tenant frontend resource %q", name)
		}
		currentTenant[name] = struct{}{}
		if string(match[2]) == "true" {
			writable[name] = struct{}{}
		}
	}
	if len(currentTenant) != len(targetTenant) {
		t.Fatalf("tenant frontend resources = %d, manifest target records %d", len(currentTenant), len(targetTenant))
	}
	if len(writable) != 0 {
		t.Fatalf("tenant payment catalog exposes generic mutation: %#v", writable)
	}

	for name := range targetTenant {
		if _, exists := currentTenant[name]; !exists {
			t.Errorf("tenant frontend catalog is missing manifest resource %q", name)
		}
	}
	for name := range currentTenant {
		if _, exists := targetTenant[name]; !exists {
			t.Errorf("tenant frontend catalog contains non-tenant resource %q", name)
		}
	}
}
