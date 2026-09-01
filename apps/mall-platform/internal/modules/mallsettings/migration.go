package mallsettings

import (
	"errors"
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

// AuthorizationMigrationID follows the mall-platform forward-only sequence
// after the four published legacy compatibility migrations.
const AuthorizationMigrationID migration.MigrationID = "66966149766804"

const authorizationRevisionResource = "authorization"

type authorizationMenuSeed struct {
	name       string
	path       string
	method     string
	parentPath string
	accessType adminpkg.AccessType
	permission string
	icon       string
	sort       int
	hidden     bool
}

var authorizationMenuSeeds = []authorizationMenuSeed{
	{
		name: "legacyBusiness", path: "/business", method: "GET",
		accessType: adminpkg.DirectoryAccessType, permission: "legacy.business.access",
		icon: "database", sort: 10,
	},
	{
		name: "menu.legacy.domain.settings", path: "/business/settings", method: "GET",
		parentPath: "/business", accessType: adminpkg.DirectoryAccessType,
		permission: "legacy.domain.settings", sort: 2,
	},
	{
		name: "mallSettings", path: menuPath, method: "GET",
		parentPath: "/business/settings", accessType: adminpkg.MenuAccessType,
		permission: PermissionRead, icon: "setting", sort: 10,
	},
	{
		name: "mall-settings:read", path: readComponent, method: "GET",
		parentPath: menuPath, accessType: adminpkg.ComponentAccessType,
		permission: PermissionRead, hidden: true,
	},
	{
		name: "mall-settings:update", path: updateComponent, method: "GET",
		parentPath: menuPath, accessType: adminpkg.ComponentAccessType,
		permission: PermissionUpdate, hidden: true,
	},
	{
		name: "api.mallSettings.general.read", path: generalRoutePath, method: "GET",
		parentPath: readComponent, accessType: adminpkg.APIAccessType,
		permission: PermissionRead, hidden: true,
	},
	{
		name: "api.mallSettings.general.update", path: generalRoutePath, method: "PUT",
		parentPath: updateComponent, accessType: adminpkg.APIAccessType,
		permission: PermissionUpdate, hidden: true,
	},
}

func RegisterMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("mall settings migration runner is required")
	}
	return runner.Register(AuthorizationMigrationID, migrateAuthorization)
}

// migrateAuthorization projects only MSS menu, permission, and policy state.
// It never creates or alters system_configs or any other legacy relation.
func migrateAuthorization(database *gorm.DB, version string) error {
	if database == nil {
		return errors.New("mall settings authorization migration database is required")
	}
	if version != AuthorizationMigrationID.String() {
		return errors.New("mall settings authorization migration version mismatch")
	}
	return database.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version = ?", version).Count(&applied).Error; err != nil {
			return fmt.Errorf("mall settings authorization migration: check version: %w", err)
		}
		if applied > 0 {
			return nil
		}
		adminRole, err := resolveAuthorizationRole(tx)
		if err != nil {
			return err
		}
		menus := make(map[string]*models.Menu, len(authorizationMenuSeeds))
		for _, seed := range authorizationMenuSeeds {
			parentID := ""
			if seed.parentPath != "" {
				parent := menus[seed.parentPath]
				if parent == nil {
					return fmt.Errorf("mall settings authorization migration: parent %q is unavailable", seed.parentPath)
				}
				parentID = parent.ID
			}
			menu, err := upsertAuthorizationMenu(tx, seed, parentID)
			if err != nil {
				return err
			}
			menus[authorizationMenuKey(seed)] = menu
			if err := seedAuthorizationPolicy(tx, adminRole.ID, seed.accessType, seed.path, seed.method); err != nil {
				return err
			}
		}
		if err := advanceAuthorizationRevision(tx, "role", adminRole.ID); err != nil {
			return err
		}
		if err := advanceAuthorizationRevision(tx, "global", ""); err != nil {
			return err
		}
		versionRow := new(migrationmodels.Migration)
		versionRow.SetVersion(version)
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "version"}}, DoNothing: true,
		}).Create(versionRow).Error; err != nil {
			return fmt.Errorf("mall settings authorization migration: record version: %w", err)
		}
		return nil
	})
}

func resolveAuthorizationRole(tx *gorm.DB) (*models.Role, error) {
	var matches []models.Role
	if err := tx.Unscoped().Where("name = ?", "admin").Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("mall settings authorization migration: resolve admin role: %w", err)
	}
	if len(matches) > 1 {
		return nil, errors.New("mall settings authorization migration: admin role is ambiguous")
	}
	if len(matches) == 1 {
		role := &matches[0]
		if role.DeletedAt.Valid || role.Status != enum.Enabled {
			return nil, errors.New("mall settings authorization migration: admin role is inactive")
		}
		return role, nil
	}
	role := &models.Role{Name: "admin", Status: enum.Enabled, Remark: "mall settings default role"}
	if err := tx.Create(role).Error; err != nil {
		return nil, fmt.Errorf("mall settings authorization migration: create admin role: %w", err)
	}
	return role, nil
}

func upsertAuthorizationMenu(tx *gorm.DB, seed authorizationMenuSeed, parentID string) (*models.Menu, error) {
	query := tx.Unscoped().Where("type = ? AND path = ?", seed.accessType, seed.path)
	if seed.accessType == adminpkg.APIAccessType {
		query = query.Where("method = ?", seed.method)
	}
	var matches []models.Menu
	if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("mall settings authorization migration: resolve %s %q: %w", seed.accessType, seed.path, err)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("mall settings authorization migration: %s %q is ambiguous", seed.accessType, seed.path)
	}
	if len(matches) == 0 {
		menu := &models.Menu{
			Name: seed.name, Path: seed.path, Method: seed.method, ParentID: parentID,
			Icon: strings.TrimSpace(seed.icon), Type: seed.accessType, Permission: seed.permission,
			Status: enum.Enabled, Sort: seed.sort, HideInMenu: seed.hidden,
		}
		if err := tx.Create(menu).Error; err != nil {
			return nil, fmt.Errorf("mall settings authorization migration: create %s %q: %w", seed.accessType, seed.path, err)
		}
		return menu, nil
	}
	menu := &matches[0]
	if menu.DeletedAt.Valid {
		return nil, fmt.Errorf("mall settings authorization migration: %s %q is soft-deleted", seed.accessType, seed.path)
	}
	if err := tx.Model(menu).Updates(map[string]any{
		"name": seed.name, "method": seed.method, "parent_id": parentID,
		"icon": strings.TrimSpace(seed.icon), "permission": seed.permission,
		"status": enum.Enabled, "sort": seed.sort, "hide_in_menu": seed.hidden,
	}).Error; err != nil {
		return nil, fmt.Errorf("mall settings authorization migration: update %s %q: %w", seed.accessType, seed.path, err)
	}
	return menu, nil
}

func seedAuthorizationPolicy(tx *gorm.DB, roleID string, accessType adminpkg.AccessType, path, method string) error {
	if strings.TrimSpace(roleID) == "" {
		return errors.New("mall settings authorization migration: admin role ID is empty")
	}
	rule := &models.CasbinRule{PType: "p", V0: roleID, V1: accessType.String(), V2: path, V3: method}
	if err := tx.Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		rule.PType, rule.V0, rule.V1, rule.V2, rule.V3,
	).FirstOrCreate(rule).Error; err != nil {
		return fmt.Errorf("mall settings authorization migration: seed %s %s %s: %w", accessType, method, path, err)
	}
	return nil
}

func advanceAuthorizationRevision(tx *gorm.DB, scope, ownerID string) error {
	key := &models.ConfigRevision{
		Scope: scope, OwnerID: ownerID, Resource: authorizationRevisionResource,
		Revision: 0, UpdatedAt: time.Now(),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(key).Error; err != nil {
		return fmt.Errorf("mall settings authorization migration: ensure revision %s/%s: %w", scope, ownerID, err)
	}
	var current models.ConfigRevision
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"scope = ? AND owner_id = ? AND resource = ?",
		scope, ownerID, authorizationRevisionResource,
	).Take(&current).Error; err != nil {
		return fmt.Errorf("mall settings authorization migration: lock revision %s/%s: %w", scope, ownerID, err)
	}
	if current.Revision < 0 || current.Revision == 1<<63-1 {
		return fmt.Errorf("mall settings authorization migration: revision cannot advance for %s/%s", scope, ownerID)
	}
	result := tx.Model(new(models.ConfigRevision)).Where(
		"scope = ? AND owner_id = ? AND resource = ? AND revision = ?",
		scope, ownerID, authorizationRevisionResource, current.Revision,
	).Updates(map[string]any{"revision": current.Revision + 1, "updated_at": time.Now()})
	if result.Error != nil {
		return fmt.Errorf("mall settings authorization migration: advance revision %s/%s: %w", scope, ownerID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("mall settings authorization migration: revision changed concurrently for %s/%s", scope, ownerID)
	}
	return nil
}

func authorizationMenuKey(seed authorizationMenuSeed) string {
	if seed.accessType == adminpkg.APIAccessType {
		return seed.path + "#" + seed.method
	}
	return seed.path
}
