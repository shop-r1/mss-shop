package legacycompat

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
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AuthorizationMigrationID projects MSS core menu/RBAC state and never creates
// or alters a legacy business table. It is immutable because installations may
// already have recorded it.
const AuthorizationMigrationID migration.MigrationID = "66966149766800"

// MenuLocalizationMigrationID is the forward-only correction for the root
// menu token written by AuthorizationMigrationID.
const MenuLocalizationMigrationID migration.MigrationID = "66966149766801"

// CapabilityLockdownMigrationID is the forward-only correction that revokes
// the generic mutation permissions published before legacy workflow semantics
// were fully inventoried.
const CapabilityLockdownMigrationID migration.MigrationID = "66966149766802"

// OwnershipTransferMigrationID is the forward-only DEC-0009 projection that
// adds the seven tenant-owned catalogue/logistics snapshots to mall Admin.
const OwnershipTransferMigrationID migration.MigrationID = "66966149766803"

const authorizationRevisionResource = "authorization"

func RegisterMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("legacy compatibility migration runner is required")
	}
	if err := runner.Register(AuthorizationMigrationID, migrateAuthorization); err != nil {
		return err
	}
	if err := runner.Register(MenuLocalizationMigrationID, migrateMenuLocalization); err != nil {
		return err
	}
	if err := runner.Register(CapabilityLockdownMigrationID, migrateCapabilityLockdown); err != nil {
		return err
	}
	return runner.Register(OwnershipTransferMigrationID, migrateOwnershipTransfer)
}

func migrateAuthorization(db *gorm.DB, version string) error {
	if db == nil {
		return errors.New("legacy compatibility migration database is required")
	}
	if version != AuthorizationMigrationID.String() {
		return errors.New("legacy compatibility migration version mismatch")
	}
	registry := legacydb.PublishedRegistry()
	if len(registry.All()) != legacydb.PublishedMallResourceCount {
		return errors.New("legacy compatibility migration manifest is incomplete")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version = ?", version).Count(&applied).Error; err != nil {
			return fmt.Errorf("legacy compatibility migration: check version: %w", err)
		}
		if applied > 0 {
			return nil
		}
		adminRole, err := resolveAdminRole(tx)
		if err != nil {
			return err
		}
		root, err := upsertMenu(tx, menuSeed{
			name: "业务管理", path: businessMenuRoot, method: "GET", accessType: adminpkg.DirectoryAccessType,
			permission: "legacy.business.access", icon: "database", sort: 10,
		})
		if err != nil {
			return err
		}
		if err := seedPolicy(tx, adminRole.ID, adminpkg.DirectoryAccessType, root.Path, "GET"); err != nil {
			return err
		}
		domainMenus := make(map[string]*models.Menu, len(legacydb.FrontendDomains()))
		for index, domain := range legacydb.FrontendDomains() {
			domainMenu, err := upsertMenu(tx, menuSeed{
				name: "menu.legacy.domain." + domain, path: domainMenuPath(domain), method: "GET",
				parentID: root.ID, accessType: adminpkg.DirectoryAccessType,
				permission: "legacy.domain." + domain, sort: len(legacydb.FrontendDomains()) - index,
			})
			if err != nil {
				return err
			}
			if err := seedPolicy(tx, adminRole.ID, adminpkg.DirectoryAccessType, domainMenu.Path, "GET"); err != nil {
				return err
			}
			domainMenus[domain] = domainMenu
		}

		for index, definition := range registry.All() {
			resource := definition.Resource.Name
			domainMenu := domainMenus[definition.Resource.Domain]
			if domainMenu == nil {
				return fmt.Errorf("legacy compatibility migration: frontend domain %q is not declared", definition.Resource.Domain)
			}
			menu, err := upsertMenu(tx, menuSeed{
				name: "legacy." + definition.Resource.Domain + "." + resource, path: menuPath(definition), method: "GET",
				parentID: domainMenu.ID, accessType: adminpkg.MenuAccessType,
				permission: Permission(resource, OperationList), sort: legacydb.PublishedMallResourceCount - index,
			})
			if err != nil {
				return err
			}
			if err := seedPolicy(tx, adminRole.ID, adminpkg.MenuAccessType, menu.Path, "GET"); err != nil {
				return err
			}
			for _, operation := range authorizationMigrationOperationsFor(definition) {
				component, err := upsertMenu(tx, menuSeed{
					name:       Permission(resource, operation),
					path:       componentPath(definition, operation),
					method:     "GET",
					parentID:   menu.ID,
					accessType: adminpkg.ComponentAccessType,
					permission: Permission(resource, operation),
					hidden:     true,
				})
				if err != nil {
					return err
				}
				if err := seedPolicy(tx, adminRole.ID, adminpkg.ComponentAccessType, component.Path, "GET"); err != nil {
					return err
				}
			}
		}

		apiSeeds := []menuSeed{
			{name: "api.legacy.list", path: collectionRoutePath, method: "GET", parentID: root.ID, accessType: adminpkg.APIAccessType, permission: "legacy.api.list", hidden: true},
			{name: "api.legacy.read", path: detailRoutePath, method: "GET", parentID: root.ID, accessType: adminpkg.APIAccessType, permission: "legacy.api.read", hidden: true},
			{name: "api.legacy.create", path: collectionRoutePath, method: "POST", parentID: root.ID, accessType: adminpkg.APIAccessType, permission: "legacy.api.create", hidden: true},
			{name: "api.legacy.update", path: detailRoutePath, method: "PUT", parentID: root.ID, accessType: adminpkg.APIAccessType, permission: "legacy.api.update", hidden: true},
			{name: "api.legacy.delete", path: detailRoutePath, method: "DELETE", parentID: root.ID, accessType: adminpkg.APIAccessType, permission: "legacy.api.delete", hidden: true},
		}
		for _, seed := range apiSeeds {
			api, err := upsertMenu(tx, seed)
			if err != nil {
				return err
			}
			if err := seedPolicy(tx, adminRole.ID, adminpkg.APIAccessType, api.Path, api.Method); err != nil {
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
			return fmt.Errorf("legacy compatibility migration: record version: %w", err)
		}
		return nil
	})
}

func resolveAdminRole(tx *gorm.DB) (*models.Role, error) {
	var matches []models.Role
	if err := tx.Unscoped().Where("name = ?", "admin").Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("legacy compatibility migration: resolve admin role: %w", err)
	}
	if len(matches) > 1 {
		return nil, errors.New("legacy compatibility migration: admin role is ambiguous")
	}
	if len(matches) == 1 {
		role := &matches[0]
		if role.DeletedAt.Valid || role.Status != enum.Enabled {
			return nil, errors.New("legacy compatibility migration: admin role is inactive")
		}
		return role, nil
	}
	role := &models.Role{Name: "admin", Status: enum.Enabled, Remark: "legacy compatibility default role"}
	if err := tx.Create(role).Error; err != nil {
		return nil, fmt.Errorf("legacy compatibility migration: create admin role: %w", err)
	}
	return role, nil
}

type menuSeed struct {
	name       string
	path       string
	method     string
	parentID   string
	accessType adminpkg.AccessType
	permission string
	icon       string
	sort       int
	hidden     bool
}

func upsertMenu(tx *gorm.DB, seed menuSeed) (*models.Menu, error) {
	query := tx.Unscoped().Where("type = ? AND path = ?", seed.accessType, seed.path)
	if seed.accessType == adminpkg.APIAccessType {
		query = query.Where("method = ?", seed.method)
	}
	var matches []models.Menu
	if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("legacy compatibility migration: resolve %s %q: %w", seed.accessType, seed.path, err)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("legacy compatibility migration: %s %q is ambiguous", seed.accessType, seed.path)
	}
	if len(matches) == 0 {
		menu := &models.Menu{
			Name: seed.name, Path: seed.path, Method: seed.method, ParentID: seed.parentID,
			Icon: strings.TrimSpace(seed.icon), Type: seed.accessType, Permission: seed.permission,
			Status: enum.Enabled, Sort: seed.sort, HideInMenu: seed.hidden,
		}
		if err := tx.Create(menu).Error; err != nil {
			return nil, fmt.Errorf("legacy compatibility migration: create %s %q: %w", seed.accessType, seed.path, err)
		}
		return menu, nil
	}
	menu := &matches[0]
	if menu.DeletedAt.Valid {
		return nil, fmt.Errorf("legacy compatibility migration: %s %q is soft-deleted", seed.accessType, seed.path)
	}
	if err := tx.Model(menu).Updates(map[string]any{
		"name": seed.name, "method": seed.method, "parent_id": seed.parentID, "icon": strings.TrimSpace(seed.icon),
		"permission": seed.permission, "status": enum.Enabled, "sort": seed.sort, "hide_in_menu": seed.hidden,
	}).Error; err != nil {
		return nil, fmt.Errorf("legacy compatibility migration: update %s %q: %w", seed.accessType, seed.path, err)
	}
	return menu, nil
}

func seedPolicy(tx *gorm.DB, roleID string, accessType adminpkg.AccessType, path, method string) error {
	if strings.TrimSpace(roleID) == "" {
		return errors.New("legacy compatibility migration: admin role ID is empty")
	}
	rule := &models.CasbinRule{PType: "p", V0: roleID, V1: accessType.String(), V2: path, V3: method}
	if err := tx.Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		rule.PType, rule.V0, rule.V1, rule.V2, rule.V3,
	).FirstOrCreate(rule).Error; err != nil {
		return fmt.Errorf("legacy compatibility migration: seed %s %s %s: %w", accessType, method, path, err)
	}
	return nil
}

func advanceAuthorizationRevision(tx *gorm.DB, scope, ownerID string) error {
	key := &models.ConfigRevision{
		Scope: scope, OwnerID: ownerID, Resource: authorizationRevisionResource,
		Revision: 0, UpdatedAt: time.Now(),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(key).Error; err != nil {
		return fmt.Errorf("legacy compatibility migration: ensure revision %s/%s: %w", scope, ownerID, err)
	}
	result := tx.Model(new(models.ConfigRevision)).Where(
		"scope = ? AND owner_id = ? AND resource = ?",
		scope, ownerID, authorizationRevisionResource,
	).Updates(map[string]any{
		"revision": gorm.Expr("revision + 1"), "updated_at": time.Now(),
	})
	if result.Error != nil {
		return fmt.Errorf("legacy compatibility migration: advance revision %s/%s: %w", scope, ownerID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("legacy compatibility migration: revision unavailable for %s/%s", scope, ownerID)
	}
	return nil
}
