package adminprojection

import (
	"testing"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"gorm.io/gorm"
)

func TestMigrationReusesExistingMenusSeedsExactPoliciesAndIsIdempotent(t *testing.T) {
	database := openProjectionTestDatabase(t)
	projection := projectionTestContract()

	role := models.Role{Name: "admin", Status: enum.Enabled, Remark: "existing role remark"}
	if err := database.Create(&role).Error; err != nil {
		t.Fatal(err)
	}
	root := models.Menu{
		Name: "stale root", Path: "/business", Method: "POST", Icon: "stale",
		Type: adminpkg.DirectoryAccessType, Permission: "stale", Status: enum.Disabled,
		Sort: -1, HideInMenu: true,
	}
	if err := database.Create(&root).Error; err != nil {
		t.Fatal(err)
	}
	feature := models.Menu{
		Name: "stale feature", Path: "/business/example", Method: "POST", ParentID: "",
		Icon: "stale", Type: adminpkg.MenuAccessType, Permission: "stale",
		Status: enum.Disabled, Sort: -1, HideInMenu: true,
	}
	if err := database.Create(&feature).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.CasbinRule{
		PType: "p", V0: role.ID, V1: adminpkg.DirectoryAccessType.String(),
		V2: "/business", V3: "GET",
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, revision := range []models.ConfigRevision{
		{Scope: "role", OwnerID: role.ID, Resource: authorizationRevisionResource, Revision: 7, UpdatedAt: time.Unix(1, 0)},
		{Scope: "global", OwnerID: "", Resource: authorizationRevisionResource, Revision: 9, UpdatedAt: time.Unix(1, 0)},
	} {
		if err := database.Create(&revision).Error; err != nil {
			t.Fatal(err)
		}
	}

	runner := migration.New()
	if err := RegisterMigration(runner, projection); err != nil {
		t.Fatal(err)
	}
	// Registration must have captured the validated copy rather than these
	// caller-owned slices.
	projection.Menus[0].Name = "mutated after registration"
	projection.Routes[0].Permission = "mutated after registration"
	runner.SetDb(database)
	runner.SetModel(new(migrationmodels.Migration))
	if err := runner.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	var persistedRole models.Role
	if err := database.Where("name = ?", "admin").Take(&persistedRole).Error; err != nil {
		t.Fatal(err)
	}
	if persistedRole.ID != role.ID || persistedRole.Remark != "existing role remark" {
		t.Fatalf("existing role was replaced or rewritten: %#v", persistedRole)
	}

	validated := projectionTestContract()
	persistedMenus := make(map[string]models.Menu, len(validated.Menus))
	for _, seed := range validated.Menus {
		query := database.Where("type = ? AND path = ?", seed.AccessType, seed.Path)
		if seed.AccessType == adminpkg.APIAccessType {
			query = query.Where("method = ?", seed.Method)
		}
		var matches []models.Menu
		if err := query.Find(&matches).Error; err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 {
			t.Fatalf("menu %s %s count = %d", seed.AccessType, seed.Path, len(matches))
		}
		menu := matches[0]
		parentID := ""
		if seed.ParentPath != "" {
			parentID = persistedMenus[seed.ParentPath].ID
		}
		if menu.Name != seed.Name || menu.Method != seed.Method || menu.ParentID != parentID ||
			menu.Icon != seed.Icon || menu.Permission != seed.Permission || menu.Status != enum.Enabled ||
			menu.Sort != seed.Sort || menu.HideInMenu != seed.Hidden {
			t.Fatalf("menu %s %s = %#v", seed.AccessType, seed.Path, menu)
		}
		if seed.Path == "/business" && menu.ID != root.ID {
			t.Fatal("existing business directory ID was not reused")
		}
		if seed.Path == "/business/example" && menu.ID != feature.ID {
			t.Fatal("existing feature menu ID was not reused")
		}
		if seed.AccessType != adminpkg.APIAccessType {
			persistedMenus[seed.Path] = menu
		}
	}

	var policies []models.CasbinRule
	if err := database.Order("id").Find(&policies).Error; err != nil {
		t.Fatal(err)
	}
	if len(policies) != len(validated.Menus) {
		t.Fatalf("policy count = %d, want %d", len(policies), len(validated.Menus))
	}
	wantPolicies := make(map[string]struct{}, len(validated.Menus))
	for _, seed := range validated.Menus {
		wantPolicies[policyTestKey("p", role.ID, seed.AccessType.String(), seed.Path, seed.Method)] = struct{}{}
	}
	for _, policy := range policies {
		key := policyTestKey(policy.PType, policy.V0, policy.V1, policy.V2, policy.V3)
		if _, exists := wantPolicies[key]; !exists || policy.V4 != "" || policy.V5 != "" {
			t.Fatalf("unexpected Casbin policy: %#v", policy)
		}
		delete(wantPolicies, key)
	}
	if len(wantPolicies) != 0 {
		t.Fatalf("missing Casbin policies: %#v", wantPolicies)
	}

	assertProjectionRevision(t, database, "role", role.ID, 8)
	assertProjectionRevision(t, database, "global", "", 10)
	assertProjectionCounts(t, database, 1, int64(len(validated.Menus)), int64(len(validated.Menus)), 1, 2)

	if err := runner.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertProjectionRevision(t, database, "role", role.ID, 8)
	assertProjectionRevision(t, database, "global", "", 10)
	assertProjectionCounts(t, database, 1, int64(len(validated.Menus)), int64(len(validated.Menus)), 1, 2)
}

func policyTestKey(values ...string) string {
	result := ""
	for _, value := range values {
		result += string(rune(len(value))) + value
	}
	return result
}

func assertProjectionRevision(t *testing.T, database *gorm.DB, scope, ownerID string, want int64) {
	t.Helper()
	var revision models.ConfigRevision
	if err := database.Where(
		"scope = ? AND owner_id = ? AND resource = ?", scope, ownerID, authorizationRevisionResource,
	).Take(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if revision.Revision != want {
		t.Fatalf("revision %s/%s = %d, want %d", scope, ownerID, revision.Revision, want)
	}
}

func assertProjectionCounts(
	t *testing.T,
	database *gorm.DB,
	roles, menus, policies, versions, revisions int64,
) {
	t.Helper()
	for name, expected := range map[string]struct {
		model any
		count int64
	}{
		"roles":     {model: new(models.Role), count: roles},
		"menus":     {model: new(models.Menu), count: menus},
		"policies":  {model: new(models.CasbinRule), count: policies},
		"versions":  {model: new(migrationmodels.Migration), count: versions},
		"revisions": {model: new(models.ConfigRevision), count: revisions},
	} {
		var count int64
		if err := database.Model(expected.model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != expected.count {
			t.Fatalf("%s count = %d, want %d", name, count, expected.count)
		}
	}
}
