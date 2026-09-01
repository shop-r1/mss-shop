package legacycompat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These resources were granted generic mutation operations by the immutable
// 66966149766800 projection. Keep that historical projection reproducible and
// revoke it only through CapabilityLockdownMigrationID.
var historicallyWritableMallResources = map[string]struct{}{
	"function_circles":  {},
	"message_events":    {},
	"message_templates": {},
	"show_categories":   {},
}

func authorizationMigrationOperationsFor(definition legacydb.Definition) []Operation {
	operations := operationsFor(definition)
	if _, wasWritable := historicallyWritableMallResources[definition.Resource.Name]; !wasWritable {
		return operations
	}
	return append(operations, OperationCreate, OperationUpdate, OperationDelete)
}

type authorizationLockdownTarget struct {
	accessType adminpkg.AccessType
	path       string
	method     string
}

func capabilityLockdownTargets(registry legacydb.Registry) []authorizationLockdownTarget {
	targets := make([]authorizationLockdownTarget, 0, legacydb.ExpectedMallResourceCount*3+3)
	for _, definition := range registry.All() {
		for _, operation := range []Operation{OperationCreate, OperationUpdate, OperationDelete} {
			targets = append(targets, authorizationLockdownTarget{
				accessType: adminpkg.ComponentAccessType,
				path:       componentPath(definition, operation),
				method:     http.MethodGet,
			})
		}
	}
	targets = append(targets,
		authorizationLockdownTarget{accessType: adminpkg.APIAccessType, path: collectionRoutePath, method: http.MethodPost},
		authorizationLockdownTarget{accessType: adminpkg.APIAccessType, path: detailRoutePath, method: http.MethodPut},
		authorizationLockdownTarget{accessType: adminpkg.APIAccessType, path: detailRoutePath, method: http.MethodDelete},
	)
	return targets
}

// migrateCapabilityLockdown changes MSS authorization state only. Legacy
// business relations remain read-only and are never created, altered, or
// migrated here.
func migrateCapabilityLockdown(db *gorm.DB, version string) error {
	if db == nil {
		return errors.New("legacy capability lockdown migration database is required")
	}
	if version != CapabilityLockdownMigrationID.String() {
		return errors.New("legacy capability lockdown migration version mismatch")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version = ?", version).Count(&applied).Error; err != nil {
			return fmt.Errorf("legacy capability lockdown migration: check version: %w", err)
		}
		if applied > 0 {
			return nil
		}

		var prerequisites int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version IN ?", []string{
			AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(),
		}).Count(&prerequisites).Error; err != nil {
			return fmt.Errorf("legacy capability lockdown migration: check prerequisites: %w", err)
		}
		if prerequisites != 2 {
			return errors.New("legacy capability lockdown migration: authorization prerequisites are not applied")
		}

		adminRole, err := resolveAdminRole(tx)
		if err != nil {
			return err
		}
		affectedRoles := map[string]struct{}{adminRole.ID: {}}
		for _, target := range capabilityLockdownTargets(legacydb.PublishedRegistry()) {
			roles, err := authorizationRolesForTarget(tx, target)
			if err != nil {
				return err
			}
			for _, roleID := range roles {
				affectedRoles[roleID] = struct{}{}
			}
			if err := disableAuthorizationTarget(tx, target); err != nil {
				return err
			}
		}
		roleIDs := make([]string, 0, len(affectedRoles))
		for roleID := range affectedRoles {
			roleIDs = append(roleIDs, roleID)
		}
		sort.Strings(roleIDs)
		for _, roleID := range roleIDs {
			if err := advanceAuthorizationRevision(tx, "role", roleID); err != nil {
				return err
			}
		}
		if err := advanceAuthorizationRevision(tx, "global", ""); err != nil {
			return err
		}

		versionRow := new(migrationmodels.Migration)
		versionRow.SetVersion(version)
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "version"}}, DoNothing: true,
		}).Create(versionRow).Error; err != nil {
			return fmt.Errorf("legacy capability lockdown migration: record version: %w", err)
		}
		return nil
	})
}

func authorizationRolesForTarget(tx *gorm.DB, target authorizationLockdownTarget) ([]string, error) {
	var rows []struct {
		RoleID string `gorm:"column:v0"`
	}
	if err := tx.Model(new(models.CasbinRule)).Distinct("v0").Where(
		"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p", target.accessType.String(), target.path, target.method,
	).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("legacy capability lockdown migration: inspect %s %s %s roles: %w", target.accessType, target.method, target.path, err)
	}
	roles := make([]string, 0, len(rows))
	for _, row := range rows {
		if roleID := strings.TrimSpace(row.RoleID); roleID != "" {
			roles = append(roles, roleID)
		}
	}
	return roles, nil
}

func disableAuthorizationTarget(tx *gorm.DB, target authorizationLockdownTarget) error {
	query := tx.Unscoped().Where("type = ? AND path = ?", target.accessType, target.path)
	if target.accessType == adminpkg.APIAccessType {
		query = query.Where("method = ?", target.method)
	}
	var matches []models.Menu
	if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
		return fmt.Errorf("legacy capability lockdown migration: resolve %s %s %s: %w", target.accessType, target.method, target.path, err)
	}
	if len(matches) > 1 {
		return fmt.Errorf("legacy capability lockdown migration: %s %s %s is ambiguous", target.accessType, target.method, target.path)
	}
	if len(matches) == 1 && !matches[0].DeletedAt.Valid {
		if err := tx.Model(&matches[0]).Updates(map[string]any{
			"status": enum.Disabled, "updated_at": time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("legacy capability lockdown migration: disable %s %s %s: %w", target.accessType, target.method, target.path, err)
		}
	}
	if err := tx.Where(
		"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p", target.accessType.String(), target.path, target.method,
	).Delete(new(models.CasbinRule)).Error; err != nil {
		return fmt.Errorf("legacy capability lockdown migration: revoke %s %s %s: %w", target.accessType, target.method, target.path, err)
	}
	return nil
}

func verifyCapabilityLockdownReadiness(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil {
		return errors.New("legacy capability lockdown readiness database/context is unavailable")
	}
	for _, target := range capabilityLockdownTargets(legacydb.DefaultRegistry()) {
		query := db.WithContext(ctx).Unscoped().Where("type = ? AND path = ?", target.accessType, target.path)
		if target.accessType == adminpkg.APIAccessType {
			query = query.Where("method = ?", target.method)
		}
		var matches []models.Menu
		if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
			return fmt.Errorf("legacy capability lockdown readiness: resolve %s %s %s: %w", target.accessType, target.method, target.path, err)
		}
		if len(matches) > 1 {
			return fmt.Errorf("legacy capability lockdown readiness: %s %s %s is ambiguous", target.accessType, target.method, target.path)
		}
		if len(matches) == 1 && !matches[0].DeletedAt.Valid && matches[0].Status != enum.Disabled {
			return fmt.Errorf("legacy capability lockdown readiness: %s %s %s is enabled", target.accessType, target.method, target.path)
		}
		var policies int64
		if err := db.WithContext(ctx).Model(new(models.CasbinRule)).Where(
			"ptype = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", target.accessType.String(), target.path, target.method,
		).Count(&policies).Error; err != nil {
			return fmt.Errorf("legacy capability lockdown readiness: inspect %s %s %s policies: %w", target.accessType, target.method, target.path, err)
		}
		if policies != 0 {
			return fmt.Errorf("legacy capability lockdown readiness: %s %s %s still has policies", target.accessType, target.method, target.path)
		}
	}
	return nil
}
