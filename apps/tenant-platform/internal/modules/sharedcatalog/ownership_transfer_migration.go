package sharedcatalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OwnershipTransferMigrationID is the forward-only DEC-0009 correction that
// removes seven product/logistics resources from the tenant control plane.
const OwnershipTransferMigrationID migration.MigrationID = "20260901120003"

const transferredTenantResourceCount = 7

func transferredTenantDefinitions(published, current legacydb.Registry) ([]legacydb.Definition, error) {
	if len(published.All()) != legacydb.PublishedSharedResourceCount || len(current.All()) != legacydb.ExpectedSharedResourceCount {
		return nil, errors.New("published/current shared catalogue registries are incomplete")
	}
	retained := make(map[string]struct{}, legacydb.ExpectedSharedResourceCount)
	for _, definition := range current.All() {
		retained[definition.Resource.Name] = struct{}{}
	}
	if _, ok := retained["payments"]; !ok || len(retained) != 1 {
		return nil, errors.New("payments must be the only retained tenant resource")
	}
	moved := make([]legacydb.Definition, 0, transferredTenantResourceCount)
	for _, definition := range published.All() {
		if _, keep := retained[definition.Resource.Name]; !keep {
			moved = append(moved, definition)
		}
	}
	if len(moved) != transferredTenantResourceCount {
		return nil, fmt.Errorf("transferred tenant resource count is %d, want %d", len(moved), transferredTenantResourceCount)
	}
	return moved, nil
}

func applyOwnershipTransferMigration(
	db *gorm.DB,
	binding fixedbinding.Binding,
	published legacydb.Registry,
	current legacydb.Registry,
	version string,
	afterCleanup func() error,
) error {
	if db == nil {
		return errors.New("shared catalogue ownership transfer database is required")
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("shared catalogue ownership transfer binding: %w", err)
	}
	if version != OwnershipTransferMigrationID.String() {
		return errors.New("shared catalogue ownership transfer version mismatch")
	}
	moved, err := transferredTenantDefinitions(published, current)
	if err != nil {
		return fmt.Errorf("shared catalogue ownership transfer: %w", err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		migrationTable := qualifiedCoreTable(binding, (&migrationmodels.Migration{}).TableName())
		var applied int64
		if err := tx.Table(migrationTable).Where("version = ?", version).Count(&applied).Error; err != nil {
			return errors.New("shared catalogue ownership transfer: check version")
		}
		if applied > 0 {
			return nil
		}
		var prerequisites int64
		if err := tx.Table(migrationTable).Where("version IN ?", []string{
			AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(), CapabilityLockdownMigrationID.String(),
		}).Count(&prerequisites).Error; err != nil {
			return errors.New("shared catalogue ownership transfer: check prerequisites")
		}
		if prerequisites != 3 {
			return errors.New("shared catalogue ownership transfer: prerequisite migrations are not applied")
		}

		adminRole, err := resolveAuthorizationRole(tx, binding, "admin")
		if err != nil {
			return err
		}
		affectedRoles := map[string]struct{}{adminRole.ID: {}}
		for _, definition := range moved {
			resource := definition.Resource.Name
			targets := []lockdownAuthorizationTarget{{
				accessType: adminpkg.MenuAccessType, path: menuPath(resource), method: httpGet,
			}}
			for _, operation := range []string{"list", "read", "create", "update", "delete"} {
				targets = append(targets, lockdownAuthorizationTarget{
					accessType: adminpkg.ComponentAccessType, path: componentPath(resource, operation), method: httpGet,
				})
			}
			for _, target := range targets {
				roles, err := ownershipTargetRoles(tx, binding, target)
				if err != nil {
					return err
				}
				for _, roleID := range roles {
					affectedRoles[roleID] = struct{}{}
				}
				if err := disableOwnershipTarget(tx, binding, target); err != nil {
					return err
				}
			}
		}

		root, err := upsertAuthorizationMenu(tx, binding, authorizationMenuSeed{
			name: sharedCatalogMenuNameToken, path: sharedCatalogRootPath, method: httpGet,
			accessType: adminpkg.DirectoryAccessType,
			permission: PermissionCode("payments", string(actionRead)), icon: "pay-circle", sort: 100,
		})
		if err != nil {
			return err
		}
		if err := seedAuthorizationRule(tx, binding, adminRole.ID, adminpkg.DirectoryAccessType, root.Path, httpGet); err != nil {
			return err
		}
		payment, ok := current.Lookup("payments")
		if !ok {
			return errors.New("shared catalogue ownership transfer: payment resource is unavailable")
		}
		paymentMenu, err := upsertAuthorizationMenu(tx, binding, authorizationMenuSeed{
			name: "legacy.resources.payments", path: menuPath("payments"), method: httpGet,
			parentID: root.ID, accessType: adminpkg.MenuAccessType,
			permission: PermissionCode("payments", string(actionRead)), icon: "table",
		})
		if err != nil {
			return err
		}
		if err := seedAuthorizationRule(tx, binding, adminRole.ID, adminpkg.MenuAccessType, paymentMenu.Path, httpGet); err != nil {
			return err
		}
		for _, operation := range []string{"list", "read"} {
			permissionOperation := operation
			if operation == "list" {
				permissionOperation = string(actionRead)
			}
			component, err := upsertAuthorizationMenu(tx, binding, authorizationMenuSeed{
				name: "legacy.permissions." + operation,
				path: componentPath(payment.Resource.Name, operation), method: httpGet,
				parentID: paymentMenu.ID, accessType: adminpkg.ComponentAccessType,
				permission: PermissionCode(payment.Resource.Name, permissionOperation), hidden: true,
			})
			if err != nil {
				return err
			}
			if err := seedAuthorizationRule(tx, binding, adminRole.ID, adminpkg.ComponentAccessType, component.Path, httpGet); err != nil {
				return err
			}
		}

		if afterCleanup != nil {
			if err := afterCleanup(); err != nil {
				return fmt.Errorf("shared catalogue ownership transfer interrupted after cleanup: %w", err)
			}
		}
		roleIDs := make([]string, 0, len(affectedRoles))
		for roleID := range affectedRoles {
			roleIDs = append(roleIDs, roleID)
		}
		sort.Strings(roleIDs)
		for _, roleID := range roleIDs {
			if err := advanceAuthorizationRevision(tx, binding, authorizationScopeRole, roleID); err != nil {
				return err
			}
		}
		if err := advanceAuthorizationRevision(tx, binding, authorizationScopeGlobal, ""); err != nil {
			return err
		}

		versionRow := new(migrationmodels.Migration)
		versionRow.SetVersion(version)
		if err := tx.Table(migrationTable).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "version"}}, DoNothing: true,
		}).Create(versionRow).Error; err != nil {
			return errors.New("shared catalogue ownership transfer: record version")
		}
		return nil
	})
}

func ownershipTargetRoles(tx *gorm.DB, binding fixedbinding.Binding, target lockdownAuthorizationTarget) ([]string, error) {
	var rows []struct {
		RoleID string `gorm:"column:v0"`
	}
	if err := tx.Table(qualifiedCoreTable(binding, (&models.CasbinRule{}).TableName())).Distinct("v0").Where(
		"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p", target.accessType.String(), target.path, target.method,
	).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("shared catalogue ownership transfer: inspect policy %s", target.path)
	}
	roles := make([]string, 0, len(rows))
	for _, row := range rows {
		if roleID := strings.TrimSpace(row.RoleID); roleID != "" {
			roles = append(roles, roleID)
		}
	}
	return roles, nil
}

func disableOwnershipTarget(tx *gorm.DB, binding fixedbinding.Binding, target lockdownAuthorizationTarget) error {
	menuTable := qualifiedCoreTable(binding, (&models.Menu{}).TableName())
	if err := tx.Table(menuTable).Unscoped().Where(
		"type = ? AND path = ? AND method = ?", target.accessType, target.path, target.method,
	).Updates(map[string]any{
		"status": enum.Disabled, "deleted_at": time.Now().UTC(), "updated_at": time.Now().UTC(),
	}).Error; err != nil {
		return fmt.Errorf("shared catalogue ownership transfer: disable menu %s", target.path)
	}
	if err := tx.Exec(
		"DELETE FROM "+qualifiedCoreSQL(binding, (&models.CasbinRule{}).TableName())+
			" WHERE ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p", target.accessType.String(), target.path, target.method,
	).Error; err != nil {
		return fmt.Errorf("shared catalogue ownership transfer: revoke policy %s", target.path)
	}
	return nil
}

func verifyOwnershipTransferReadiness(
	ctx context.Context,
	db *gorm.DB,
	binding fixedbinding.Binding,
	published legacydb.Registry,
	current legacydb.Registry,
) error {
	moved, err := transferredTenantDefinitions(published, current)
	if err != nil {
		return err
	}
	menuTable := qualifiedCoreTable(binding, (&models.Menu{}).TableName())
	policyTable := qualifiedCoreTable(binding, (&models.CasbinRule{}).TableName())
	for _, definition := range moved {
		resource := definition.Resource.Name
		targets := []lockdownAuthorizationTarget{{
			accessType: adminpkg.MenuAccessType, path: menuPath(resource), method: httpGet,
		}}
		for _, operation := range []string{"list", "read", "create", "update", "delete"} {
			targets = append(targets, lockdownAuthorizationTarget{
				accessType: adminpkg.ComponentAccessType, path: componentPath(resource, operation), method: httpGet,
			})
		}
		for _, target := range targets {
			var active int64
			if err := db.WithContext(ctx).Table(menuTable).Unscoped().Where(
				"type = ? AND path = ? AND method = ? AND deleted_at IS NULL AND status = ?",
				target.accessType, target.path, target.method, enum.Enabled,
			).Count(&active).Error; err != nil || active != 0 {
				return fmt.Errorf("transferred resource menu remains active for %s", target.path)
			}
			var policies int64
			if err := db.WithContext(ctx).Table(policyTable).Where(
				"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
				"p", target.accessType.String(), target.path, target.method,
			).Count(&policies).Error; err != nil || policies != 0 {
				return fmt.Errorf("transferred resource policy remains active for %s", target.path)
			}
		}
	}
	return nil
}
