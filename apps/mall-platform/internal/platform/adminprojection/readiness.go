package adminprojection

import (
	"context"
	"errors"
	"fmt"

	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	"gorm.io/gorm"
)

// VerifyReadiness proves the forward migration, exact menu projection, and
// default-role Component/API policies without mutating MSS or business tables.
func VerifyReadiness(ctx context.Context, database *gorm.DB, projection Projection) error {
	validated, err := projection.cloneAndValidate()
	if err != nil {
		return err
	}
	prefix := validated.Name + " authorization readiness"
	if ctx == nil || database == nil {
		return errors.New(prefix + " database/context is unavailable")
	}
	if err := business.RequireAppliedMigrations(ctx, database, validated.MigrationID); err != nil {
		return fmt.Errorf("%s: migration is unavailable", prefix)
	}
	if !database.WithContext(ctx).Migrator().HasTable(new(models.CasbinRule)) {
		return errors.New(prefix + ": MSS policy table is unavailable")
	}

	var roles []models.Role
	if err := database.WithContext(ctx).Unscoped().Where("name = ?", validated.DefaultRole.Name).
		Order("id").Limit(2).Find(&roles).Error; err != nil {
		return fmt.Errorf("%s: resolve %s role", prefix, validated.DefaultRole.Name)
	}
	if len(roles) != 1 || roles[0].DeletedAt.Valid || roles[0].Status != enum.Enabled {
		return fmt.Errorf("%s: active %s role is unavailable", prefix, validated.DefaultRole.Name)
	}

	menus := make(map[string]models.Menu, len(validated.Menus))
	for _, seed := range validated.Menus {
		query := database.WithContext(ctx).Unscoped().Where("type = ? AND path = ?", seed.AccessType, seed.Path)
		if seed.AccessType == adminpkg.APIAccessType {
			query = query.Where("method = ?", seed.Method)
		}
		var matches []models.Menu
		if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
			return fmt.Errorf("%s: resolve %s %q", prefix, seed.AccessType, seed.Path)
		}
		if len(matches) != 1 {
			return fmt.Errorf("%s: %s %q count is %d", prefix, seed.AccessType, seed.Path, len(matches))
		}
		menu := matches[0]
		parentID := ""
		if seed.ParentPath != "" {
			parent, exists := menus[seed.ParentPath]
			if !exists {
				return fmt.Errorf("%s: parent %q is unavailable", prefix, seed.ParentPath)
			}
			parentID = parent.ID
		}
		if menu.DeletedAt.Valid || menu.Status != enum.Enabled ||
			menu.Name != seed.Name || menu.Method != seed.Method || menu.ParentID != parentID ||
			menu.Permission != seed.Permission || menu.Icon != seed.Icon || menu.Sort != seed.Sort ||
			menu.HideInMenu != seed.Hidden {
			return fmt.Errorf("%s: %s %q projection is stale", prefix, seed.AccessType, seed.Path)
		}
		if seed.AccessType != adminpkg.APIAccessType {
			menus[seed.Path] = menu
		}

		var policyCount int64
		if err := database.WithContext(ctx).Model(new(models.CasbinRule)).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", roles[0].ID, seed.AccessType.String(), seed.Path, seed.Method,
		).Count(&policyCount).Error; err != nil {
			return fmt.Errorf("%s: read %s %q policy", prefix, seed.AccessType, seed.Path)
		}
		if policyCount != 1 {
			return fmt.Errorf("%s: %s %q policy count is %d", prefix, seed.AccessType, seed.Path, policyCount)
		}
	}
	return nil
}
