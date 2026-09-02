package adminprojection

import (
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const authorizationRevisionResource = "authorization"

func RegisterMigration(runner *migration.Migration, projection Projection) error {
	name := strings.TrimSpace(projection.Name)
	if name == "" {
		name = "Admin projection"
	}
	if runner == nil {
		return fmt.Errorf("%s migration runner is required", name)
	}
	validated, err := projection.cloneAndValidate()
	if err != nil {
		return err
	}
	return runner.Register(validated.MigrationID, func(database *gorm.DB, version string) error {
		return migrateAuthorization(database, version, validated)
	})
}

func migrateAuthorization(database *gorm.DB, version string, projection Projection) error {
	prefix := projection.Name + " authorization migration"
	if database == nil {
		return fmt.Errorf("%s database is required", prefix)
	}
	if version != projection.MigrationID.String() {
		return fmt.Errorf("%s version mismatch", prefix)
	}
	return database.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version = ?", version).Count(&applied).Error; err != nil {
			return fmt.Errorf("%s: check version: %w", prefix, err)
		}
		if applied > 0 {
			return nil
		}
		role, err := resolveRole(tx, projection, prefix)
		if err != nil {
			return err
		}
		menus := make(map[string]*models.Menu, len(projection.Menus))
		for _, seed := range projection.Menus {
			parentID := ""
			if seed.ParentPath != "" {
				parent := menus[seed.ParentPath]
				if parent == nil {
					return fmt.Errorf("%s: parent %q is unavailable", prefix, seed.ParentPath)
				}
				parentID = parent.ID
			}
			menu, err := upsertMenu(tx, seed, parentID, prefix)
			if err != nil {
				return err
			}
			if seed.AccessType != adminpkg.APIAccessType {
				menus[seed.Path] = menu
			}
			if err := seedPolicy(tx, role.ID, projection.DefaultRole.Name, seed, prefix); err != nil {
				return err
			}
		}
		if err := advanceAuthorizationRevision(tx, "role", role.ID, prefix); err != nil {
			return err
		}
		if err := advanceAuthorizationRevision(tx, "global", "", prefix); err != nil {
			return err
		}
		versionRow := new(migrationmodels.Migration)
		versionRow.SetVersion(version)
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "version"}}, DoNothing: true,
		}).Create(versionRow).Error; err != nil {
			return fmt.Errorf("%s: record version: %w", prefix, err)
		}
		return nil
	})
}

func resolveRole(tx *gorm.DB, projection Projection, prefix string) (*models.Role, error) {
	var matches []models.Role
	if err := tx.Unscoped().Where("name = ?", projection.DefaultRole.Name).
		Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("%s: resolve %s role: %w", prefix, projection.DefaultRole.Name, err)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%s: %s role is ambiguous", prefix, projection.DefaultRole.Name)
	}
	if len(matches) == 1 {
		role := &matches[0]
		if role.DeletedAt.Valid || role.Status != enum.Enabled {
			return nil, fmt.Errorf("%s: %s role is inactive", prefix, projection.DefaultRole.Name)
		}
		return role, nil
	}
	role := &models.Role{
		Name: projection.DefaultRole.Name, Status: enum.Enabled, Remark: projection.DefaultRole.Remark,
	}
	if err := tx.Create(role).Error; err != nil {
		return nil, fmt.Errorf("%s: create %s role: %w", prefix, projection.DefaultRole.Name, err)
	}
	return role, nil
}

func upsertMenu(tx *gorm.DB, seed MenuSeed, parentID, prefix string) (*models.Menu, error) {
	query := tx.Unscoped().Where("type = ? AND path = ?", seed.AccessType, seed.Path)
	if seed.AccessType == adminpkg.APIAccessType {
		query = query.Where("method = ?", seed.Method)
	}
	var matches []models.Menu
	if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("%s: resolve %s %q: %w", prefix, seed.AccessType, seed.Path, err)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%s: %s %q is ambiguous", prefix, seed.AccessType, seed.Path)
	}
	if len(matches) == 0 {
		menu := &models.Menu{
			Name: seed.Name, Path: seed.Path, Method: seed.Method, ParentID: parentID,
			Icon: seed.Icon, Type: seed.AccessType, Permission: seed.Permission,
			Status: enum.Enabled, Sort: seed.Sort, HideInMenu: seed.Hidden,
		}
		if err := tx.Create(menu).Error; err != nil {
			return nil, fmt.Errorf("%s: create %s %q: %w", prefix, seed.AccessType, seed.Path, err)
		}
		return menu, nil
	}
	menu := &matches[0]
	if menu.DeletedAt.Valid {
		return nil, fmt.Errorf("%s: %s %q is soft-deleted", prefix, seed.AccessType, seed.Path)
	}
	if err := tx.Model(menu).Updates(map[string]any{
		"name": seed.Name, "method": seed.Method, "parent_id": parentID,
		"icon": seed.Icon, "permission": seed.Permission, "status": enum.Enabled,
		"sort": seed.Sort, "hide_in_menu": seed.Hidden,
	}).Error; err != nil {
		return nil, fmt.Errorf("%s: update %s %q: %w", prefix, seed.AccessType, seed.Path, err)
	}
	return menu, nil
}

func seedPolicy(tx *gorm.DB, roleID, roleName string, seed MenuSeed, prefix string) error {
	if strings.TrimSpace(roleID) == "" {
		return fmt.Errorf("%s: %s role ID is empty", prefix, roleName)
	}
	rule := &models.CasbinRule{
		PType: "p", V0: roleID, V1: seed.AccessType.String(), V2: seed.Path, V3: seed.Method,
	}
	if err := tx.Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		rule.PType, rule.V0, rule.V1, rule.V2, rule.V3,
	).FirstOrCreate(rule).Error; err != nil {
		return fmt.Errorf("%s: seed %s %s %s: %w", prefix, seed.AccessType, seed.Method, seed.Path, err)
	}
	return nil
}

func advanceAuthorizationRevision(tx *gorm.DB, scope, ownerID, prefix string) error {
	key := &models.ConfigRevision{
		Scope: scope, OwnerID: ownerID, Resource: authorizationRevisionResource,
		Revision: 0, UpdatedAt: time.Now(),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(key).Error; err != nil {
		return fmt.Errorf("%s: ensure revision %s/%s: %w", prefix, scope, ownerID, err)
	}
	var current models.ConfigRevision
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"scope = ? AND owner_id = ? AND resource = ?",
		scope, ownerID, authorizationRevisionResource,
	).Take(&current).Error; err != nil {
		return fmt.Errorf("%s: lock revision %s/%s: %w", prefix, scope, ownerID, err)
	}
	if current.Revision < 0 || current.Revision == 1<<63-1 {
		return fmt.Errorf("%s: revision cannot advance for %s/%s", prefix, scope, ownerID)
	}
	result := tx.Model(new(models.ConfigRevision)).Where(
		"scope = ? AND owner_id = ? AND resource = ? AND revision = ?",
		scope, ownerID, authorizationRevisionResource, current.Revision,
	).Updates(map[string]any{"revision": current.Revision + 1, "updated_at": time.Now()})
	if result.Error != nil {
		return fmt.Errorf("%s: advance revision %s/%s: %w", prefix, scope, ownerID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%s: revision changed concurrently for %s/%s", prefix, scope, ownerID)
	}
	return nil
}
