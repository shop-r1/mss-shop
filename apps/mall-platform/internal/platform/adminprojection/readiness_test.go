package adminprojection

import (
	"testing"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
)

func TestReadinessRequiresAppliedMigrationExactProjectionAndExactPolicyCounts(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		database := openProjectionTestDatabase(t)
		projection := projectionTestContract()
		applyProjectionTestMigration(t, database, projection)
		if err := VerifyReadiness(t.Context(), database, projection); err != nil {
			t.Fatalf("ready projection rejected: %v", err)
		}
	})

	t.Run("missing migration", func(t *testing.T) {
		database := openProjectionTestDatabase(t)
		if err := VerifyReadiness(t.Context(), database, projectionTestContract()); err == nil {
			t.Fatal("projection without its forward migration was ready")
		}
	})

	t.Run("stale menu icon", func(t *testing.T) {
		database := openProjectionTestDatabase(t)
		projection := projectionTestContract()
		applyProjectionTestMigration(t, database, projection)
		if err := database.Model(new(models.Menu)).Where(
			"type = ? AND path = ?", adminpkg.MenuAccessType, "/business/example",
		).Update("icon", "drifted").Error; err != nil {
			t.Fatal(err)
		}
		if err := VerifyReadiness(t.Context(), database, projection); err == nil {
			t.Fatal("projection with stale menu data was ready")
		}
	})

	t.Run("duplicate exact policy", func(t *testing.T) {
		database := openProjectionTestDatabase(t)
		projection := projectionTestContract()
		applyProjectionTestMigration(t, database, projection)
		var role models.Role
		if err := database.Where("name = ?", "admin").Take(&role).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Create(&models.CasbinRule{
			PType: "p", V0: role.ID, V1: adminpkg.APIAccessType.String(),
			V2: projectionTestRoute, V3: "GET",
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := VerifyReadiness(t.Context(), database, projection); err == nil {
			t.Fatal("projection with duplicate exact policy was ready")
		}
	})
}
