package legacycompat

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const transferredResourceCount = 7

func transferredDefinitions(registry legacydb.Registry) ([]legacydb.Definition, error) {
	definitions := make([]legacydb.Definition, 0, transferredResourceCount)
	for _, definition := range registry.All() {
		if definition.Scope == legacydb.ScopeSchema {
			definitions = append(definitions, definition)
		}
	}
	if len(definitions) != transferredResourceCount {
		return nil, fmt.Errorf("DEC-0009 resource count is %d, want %d", len(definitions), transferredResourceCount)
	}
	return definitions, nil
}

// migrateOwnershipTransfer changes only MSS core authorization metadata. The
// reconciler owns the separate data snapshot/materialization step and this
// migration never creates or alters a legacy business relation.
func migrateOwnershipTransfer(db *gorm.DB, version string) error {
	if db == nil {
		return errors.New("legacy ownership transfer migration database is required")
	}
	if version != OwnershipTransferMigrationID.String() {
		return errors.New("legacy ownership transfer migration version mismatch")
	}
	definitions, err := transferredDefinitions(legacydb.DefaultRegistry())
	if err != nil {
		return fmt.Errorf("legacy ownership transfer migration: %w", err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version = ?", version).Count(&applied).Error; err != nil {
			return fmt.Errorf("legacy ownership transfer migration: check version: %w", err)
		}
		if applied > 0 {
			return nil
		}

		var prerequisites int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version IN ?", []string{
			AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(), CapabilityLockdownMigrationID.String(),
		}).Count(&prerequisites).Error; err != nil {
			return fmt.Errorf("legacy ownership transfer migration: check prerequisites: %w", err)
		}
		if prerequisites != 3 {
			return errors.New("legacy ownership transfer migration: authorization prerequisites are not applied")
		}

		adminRole, err := resolveAdminRole(tx)
		if err != nil {
			return err
		}
		root, err := upsertMenu(tx, menuSeed{
			name: businessMenuNameToken, path: businessMenuRoot, method: http.MethodGet,
			accessType: adminpkg.DirectoryAccessType, permission: "legacy.business.access", icon: "database", sort: 10,
		})
		if err != nil {
			return err
		}
		if err := seedPolicy(tx, adminRole.ID, adminpkg.DirectoryAccessType, root.Path, http.MethodGet); err != nil {
			return err
		}

		domainMenus := make(map[string]*models.Menu, 2)
		for _, domain := range []string{"catalog", "fulfillment"} {
			domainMenu, err := upsertMenu(tx, menuSeed{
				name: "menu.legacy.domain." + domain, path: domainMenuPath(domain), method: http.MethodGet,
				parentID: root.ID, accessType: adminpkg.DirectoryAccessType,
				permission: "legacy.domain." + domain,
			})
			if err != nil {
				return err
			}
			if err := seedPolicy(tx, adminRole.ID, adminpkg.DirectoryAccessType, domainMenu.Path, http.MethodGet); err != nil {
				return err
			}
			domainMenus[domain] = domainMenu
		}

		for index, definition := range definitions {
			resource := definition.Resource.Name
			parent := domainMenus[definition.Resource.Domain]
			if parent == nil {
				return fmt.Errorf("legacy ownership transfer migration: unsupported domain %q", definition.Resource.Domain)
			}
			menu, err := upsertMenu(tx, menuSeed{
				name: "legacy." + definition.Resource.Domain + "." + resource,
				path: menuPath(definition), method: http.MethodGet, parentID: parent.ID,
				accessType: adminpkg.MenuAccessType, permission: Permission(resource, OperationList),
				sort: transferredResourceCount - index,
			})
			if err != nil {
				return err
			}
			if err := seedPolicy(tx, adminRole.ID, adminpkg.MenuAccessType, menu.Path, http.MethodGet); err != nil {
				return err
			}
			for _, operation := range operationsFor(definition) {
				component, err := upsertMenu(tx, menuSeed{
					name: Permission(resource, operation), path: componentPath(definition, operation), method: http.MethodGet,
					parentID: menu.ID, accessType: adminpkg.ComponentAccessType,
					permission: Permission(resource, operation), hidden: true,
				})
				if err != nil {
					return err
				}
				if err := seedPolicy(tx, adminRole.ID, adminpkg.ComponentAccessType, component.Path, http.MethodGet); err != nil {
					return err
				}
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
			return fmt.Errorf("legacy ownership transfer migration: record version: %w", err)
		}
		return nil
	})
}

func verifyOwnershipTransferReadiness(ctx context.Context, db *gorm.DB, registry legacydb.Registry) error {
	if ctx == nil || db == nil {
		return errors.New("legacy ownership transfer readiness database/context is unavailable")
	}
	definitions, err := transferredDefinitions(registry)
	if err != nil {
		return fmt.Errorf("legacy ownership transfer readiness: %w", err)
	}
	var roles []models.Role
	if err := db.WithContext(ctx).Unscoped().Where("name = ?", "admin").Order("id").Limit(2).Find(&roles).Error; err != nil {
		return fmt.Errorf("legacy ownership transfer readiness: resolve admin role: %w", err)
	}
	if len(roles) != 1 || roles[0].DeletedAt.Valid || roles[0].Status != enum.Enabled {
		return errors.New("legacy ownership transfer readiness: active admin role is unavailable")
	}
	adminRole := &roles[0]
	for _, definition := range definitions {
		if definition.Scope != legacydb.ScopeSchema || definition.TenantColumn != "" || definition.Inherited != nil {
			return fmt.Errorf("legacy ownership transfer readiness: resource %s is not schema-scoped", definition.Resource.Name)
		}
		targets := []authorizationLockdownTarget{{
			accessType: adminpkg.MenuAccessType, path: menuPath(definition), method: http.MethodGet,
		}}
		for _, operation := range operationsFor(definition) {
			targets = append(targets, authorizationLockdownTarget{
				accessType: adminpkg.ComponentAccessType, path: componentPath(definition, operation), method: http.MethodGet,
			})
		}
		for _, target := range targets {
			var menus []models.Menu
			query := db.WithContext(ctx).Unscoped().Where("type = ? AND path = ?", target.accessType, target.path)
			if err := query.Order("id").Limit(2).Find(&menus).Error; err != nil || len(menus) != 1 {
				return fmt.Errorf("legacy ownership transfer readiness: menu unavailable for %s", target.path)
			}
			if menus[0].DeletedAt.Valid || menus[0].Status != enum.Enabled {
				return fmt.Errorf("legacy ownership transfer readiness: menu inactive for %s", target.path)
			}
			var policies int64
			if err := db.WithContext(ctx).Model(new(models.CasbinRule)).Where(
				"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
				"p", adminRole.ID, target.accessType.String(), target.path, target.method,
			).Count(&policies).Error; err != nil || policies != 1 {
				return fmt.Errorf("legacy ownership transfer readiness: policy unavailable for %s", target.path)
			}
		}
	}
	return nil
}
