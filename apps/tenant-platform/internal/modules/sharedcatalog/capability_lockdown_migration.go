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

// CapabilityLockdownMigrationID revokes the write projection published by
// 120000. Both earlier migrations may already be applied and are immutable in
// deployed databases, so this correction must always remain forward-only.
const CapabilityLockdownMigrationID migration.MigrationID = "20260901120002"

type lockdownAuthorizationTarget struct {
	accessType adminpkg.AccessType
	path       string
	method     string
}

func applyCapabilityLockdownMigration(
	db *gorm.DB,
	binding fixedbinding.Binding,
	registry legacydb.Registry,
	version string,
	afterCleanup func() error,
) error {
	if db == nil {
		return errors.New("shared catalogue capability lockdown database is required")
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("shared catalogue capability lockdown binding: %w", err)
	}
	if version != CapabilityLockdownMigrationID.String() {
		return errors.New("shared catalogue capability lockdown version mismatch")
	}
	if len(registry.All()) != legacydb.PublishedSharedResourceCount {
		return errors.New("shared catalogue capability lockdown registry is incomplete")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		migrationTable := qualifiedCoreTable(binding, (&migrationmodels.Migration{}).TableName())
		var applied int64
		if err := tx.Table(migrationTable).Where("version = ?", version).Count(&applied).Error; err != nil {
			return errors.New("shared catalogue capability lockdown: check version")
		}
		if applied > 0 {
			return nil
		}

		var prerequisites int64
		if err := tx.Table(migrationTable).Where("version IN ?", []string{
			AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(),
		}).Count(&prerequisites).Error; err != nil {
			return errors.New("shared catalogue capability lockdown: check prerequisites")
		}
		if prerequisites != 2 {
			return errors.New("shared catalogue capability lockdown: prerequisite migrations are not applied")
		}

		adminRole, err := resolveReadinessRole(tx.Statement.Context, tx, binding, "admin")
		if err != nil {
			return fmt.Errorf("shared catalogue capability lockdown: %w", err)
		}
		affectedRoles := map[string]struct{}{adminRole.ID: {}}
		menuTable := qualifiedCoreTable(binding, (&models.Menu{}).TableName())
		policyTable := qualifiedCoreTable(binding, (&models.CasbinRule{}).TableName())
		policySQLTable := qualifiedCoreSQL(binding, (&models.CasbinRule{}).TableName())
		now := time.Now().UTC()

		for _, target := range capabilityLockdownTargets(registry) {
			var roles []struct {
				RoleID string `gorm:"column:v0"`
			}
			if err := tx.Table(policyTable).Distinct("v0").Where(
				"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
				"p", target.accessType.String(), target.path, target.method,
			).Scan(&roles).Error; err != nil {
				return fmt.Errorf("shared catalogue capability lockdown: inspect policy %s %s", target.method, target.path)
			}
			for _, role := range roles {
				if roleID := strings.TrimSpace(role.RoleID); roleID != "" {
					affectedRoles[roleID] = struct{}{}
				}
			}

			if err := tx.Table(menuTable).Unscoped().Where(
				"type = ? AND path = ? AND method = ?",
				target.accessType, target.path, target.method,
			).Updates(map[string]any{
				"status": enum.Disabled, "deleted_at": now, "updated_at": now,
			}).Error; err != nil {
				return fmt.Errorf("shared catalogue capability lockdown: disable menu %s %s", target.method, target.path)
			}
			if err := tx.Exec(
				"DELETE FROM "+policySQLTable+" WHERE ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
				"p", target.accessType.String(), target.path, target.method,
			).Error; err != nil {
				return fmt.Errorf("shared catalogue capability lockdown: delete policy %s %s", target.method, target.path)
			}
		}

		if afterCleanup != nil {
			if err := afterCleanup(); err != nil {
				return fmt.Errorf("shared catalogue capability lockdown interrupted after cleanup: %w", err)
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
			return errors.New("shared catalogue capability lockdown: record version")
		}
		return nil
	})
}

func capabilityLockdownTargets(registry legacydb.Registry) []lockdownAuthorizationTarget {
	targets := make([]lockdownAuthorizationTarget, 0, legacydb.PublishedSharedResourceCount*3+3)
	for _, definition := range registry.All() {
		for _, operation := range []string{"create", "update", "delete"} {
			targets = append(targets, lockdownAuthorizationTarget{
				accessType: adminpkg.ComponentAccessType,
				path:       componentPath(definition.Resource.Name, operation), method: httpGet,
			})
		}
	}
	for _, route := range operationRoutes {
		if route.Action == actionRead {
			continue
		}
		targets = append(targets, lockdownAuthorizationTarget{
			accessType: adminpkg.APIAccessType, path: route.Path, method: route.Method,
		})
	}
	return targets
}

func verifyCapabilityLockdownReadiness(
	ctx context.Context,
	db *gorm.DB,
	binding fixedbinding.Binding,
	registry legacydb.Registry,
) error {
	for _, definition := range registry.All() {
		capabilities := definition.Resource.Capabilities
		if !capabilities.Detail || capabilities.Create || capabilities.Update || capabilities.Delete {
			return fmt.Errorf("shared catalogue resource %s is not read-only", definition.Resource.Name)
		}
		for _, column := range definition.Resource.Columns {
			if column.Writable {
				return fmt.Errorf("shared catalogue resource %s exposes writable column %s", definition.Resource.Name, column.Name)
			}
		}
	}

	menuTable := qualifiedCoreTable(binding, (&models.Menu{}).TableName())
	policyTable := qualifiedCoreTable(binding, (&models.CasbinRule{}).TableName())
	for _, target := range capabilityLockdownTargets(registry) {
		var activeMenus int64
		if err := db.WithContext(ctx).Table(menuTable).Unscoped().Where(
			"type = ? AND path = ? AND method = ? AND deleted_at IS NULL AND status = ?",
			target.accessType, target.path, target.method, enum.Enabled,
		).Count(&activeMenus).Error; err != nil || activeMenus != 0 {
			return fmt.Errorf("write authorization menu remains active for %s %s", target.method, target.path)
		}
		var policies int64
		if err := db.WithContext(ctx).Table(policyTable).Where(
			"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", target.accessType.String(), target.path, target.method,
		).Count(&policies).Error; err != nil || policies != 0 {
			return fmt.Errorf("write authorization policy remains active for %s %s", target.method, target.path)
		}
	}
	return nil
}

func qualifiedCoreSQL(binding fixedbinding.Binding, table string) string {
	quote := func(identifier string) string {
		return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
	}
	return quote(binding.CoreSchema) + "." + quote(table)
}
