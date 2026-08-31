package sharedcatalog

import (
	"context"
	"errors"
	"fmt"
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

const AuthorizationMigrationID migration.MigrationID = "20260901120000"

// MenuLocalizationMigrationID is a forward-only correction for the root menu
// token written by the already-published authorization migration.
const MenuLocalizationMigrationID migration.MigrationID = "20260901120001"

const (
	authorizationScopeRole   = "role"
	authorizationScopeGlobal = "global"
	authorizationResource    = "authorization"
	sharedCatalogRootPath    = "/business/shared-catalog"
)

func applyAuthorizationMigration(
	db *gorm.DB,
	binding fixedbinding.Binding,
	registry legacydb.Registry,
	version string,
	afterPolicies func() error,
) error {
	if db == nil {
		return errors.New("shared catalogue authorization migration database is required")
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("shared catalogue authorization migration binding: %w", err)
	}
	if version != AuthorizationMigrationID.String() {
		return errors.New("shared catalogue authorization migration version mismatch")
	}
	if len(registry.All()) != legacydb.ExpectedSharedResourceCount {
		return errors.New("shared catalogue authorization migration registry is incomplete")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Table(qualifiedCoreTable(binding, "mss_boot_migration")).
			Where("version = ?", version).
			Count(&applied).Error; err != nil {
			return errors.New("shared catalogue authorization migration: check version")
		}
		if applied > 0 {
			return nil
		}

		adminRole, err := resolveAuthorizationRole(tx, binding, "admin")
		if err != nil {
			return err
		}
		root, err := upsertAuthorizationMenu(tx, binding, authorizationMenuSeed{
			name:       "shared-catalog",
			path:       sharedCatalogRootPath,
			method:     httpGet,
			accessType: adminpkg.DirectoryAccessType,
			permission: PermissionCode("brands", string(actionRead)),
			icon:       "shop",
			sort:       100,
		})
		if err != nil {
			return err
		}
		if err := seedAuthorizationRule(tx, binding, adminRole.ID, adminpkg.DirectoryAccessType, root.Path, httpGet); err != nil {
			return err
		}

		for _, definition := range registry.All() {
			resource := definition.Resource
			menu, err := upsertAuthorizationMenu(tx, binding, authorizationMenuSeed{
				name:       "legacy.resources." + resource.Name,
				path:       menuPath(resource.Name),
				method:     httpGet,
				parentID:   root.ID,
				accessType: adminpkg.MenuAccessType,
				permission: PermissionCode(resource.Name, string(actionRead)),
				icon:       "table",
				sort:       0,
			})
			if err != nil {
				return err
			}
			if err := seedAuthorizationRule(tx, binding, adminRole.ID, adminpkg.MenuAccessType, menu.Path, httpGet); err != nil {
				return err
			}

			componentOperations := publishedAuthorizationComponentOperations(resource.Name)
			for _, operation := range componentOperations {
				permissionOperation := operation
				if operation == "list" {
					permissionOperation = string(actionRead)
				}
				component, err := upsertAuthorizationMenu(tx, binding, authorizationMenuSeed{
					name:       "legacy.permissions." + operation,
					path:       componentPath(resource.Name, operation),
					method:     httpGet,
					parentID:   menu.ID,
					accessType: adminpkg.ComponentAccessType,
					permission: PermissionCode(resource.Name, permissionOperation),
					hidden:     true,
				})
				if err != nil {
					return err
				}
				if err := seedAuthorizationRule(tx, binding, adminRole.ID, adminpkg.ComponentAccessType, component.Path, httpGet); err != nil {
					return err
				}
			}
		}

		for _, route := range operationRoutes {
			if _, err := upsertAuthorizationMenu(tx, binding, authorizationMenuSeed{
				name:       route.Name,
				path:       route.Path,
				method:     route.Method,
				parentID:   root.ID,
				accessType: adminpkg.APIAccessType,
				permission: route.Permission,
				hidden:     true,
			}); err != nil {
				return err
			}
			if err := seedAuthorizationRule(tx, binding, adminRole.ID, adminpkg.APIAccessType, route.Path, route.Method); err != nil {
				return err
			}
		}

		if afterPolicies != nil {
			if err := afterPolicies(); err != nil {
				return fmt.Errorf("shared catalogue authorization migration interrupted after policy seed: %w", err)
			}
		}
		if err := advanceAuthorizationRevision(tx, binding, authorizationScopeRole, adminRole.ID); err != nil {
			return err
		}
		if err := advanceAuthorizationRevision(tx, binding, authorizationScopeGlobal, ""); err != nil {
			return err
		}

		versionRow := &migrationmodels.Migration{}
		versionRow.SetVersion(version)
		if err := tx.Table(qualifiedCoreTable(binding, versionRow.TableName())).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "version"}},
			DoNothing: true,
		}).Create(versionRow).Error; err != nil {
			return errors.New("shared catalogue authorization migration: record version")
		}
		return nil
	})
}

func resolveAuthorizationRole(tx *gorm.DB, binding fixedbinding.Binding, name string) (*models.Role, error) {
	table := qualifiedCoreTable(binding, (&models.Role{}).TableName())
	var matches []models.Role
	if err := tx.Table(table).Unscoped().Where("name = ?", name).Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("shared catalogue authorization migration: resolve role %q", name)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("shared catalogue authorization migration: role %q is ambiguous", name)
	}
	if len(matches) == 1 {
		role := &matches[0]
		if role.DeletedAt.Valid || role.Status != enum.Enabled {
			return nil, fmt.Errorf("shared catalogue authorization migration: role %q is not active", name)
		}
		return role, nil
	}
	role := &models.Role{Name: name, Status: enum.Enabled, Remark: "shared catalogue default role"}
	if err := tx.Table(table).Create(role).Error; err != nil {
		return nil, fmt.Errorf("shared catalogue authorization migration: create role %q", name)
	}
	return role, nil
}

type authorizationMenuSeed struct {
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

func upsertAuthorizationMenu(tx *gorm.DB, binding fixedbinding.Binding, seed authorizationMenuSeed) (*models.Menu, error) {
	table := qualifiedCoreTable(binding, (&models.Menu{}).TableName())
	query := tx.Table(table).Unscoped().Where("type = ? AND path = ?", seed.accessType, seed.path)
	if seed.accessType == adminpkg.APIAccessType {
		query = query.Where("method = ?", seed.method)
	}
	var matches []models.Menu
	if err := query.Order("id").Limit(2).Find(&matches).Error; err != nil {
		return nil, fmt.Errorf("shared catalogue authorization migration: resolve %s %q", seed.accessType, seed.path)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("shared catalogue authorization migration: %s %q is ambiguous", seed.accessType, seed.path)
	}
	if len(matches) == 0 {
		menu := &models.Menu{
			ParentID:   seed.parentID,
			Name:       seed.name,
			Path:       seed.path,
			Method:     seed.method,
			Icon:       seed.icon,
			Type:       seed.accessType,
			Permission: seed.permission,
			Status:     enum.Enabled,
			Sort:       seed.sort,
			HideInMenu: seed.hidden,
		}
		if err := tx.Table(table).Create(menu).Error; err != nil {
			return nil, fmt.Errorf("shared catalogue authorization migration: create %s %q", seed.accessType, seed.path)
		}
		return menu, nil
	}
	menu := &matches[0]
	if menu.DeletedAt.Valid {
		return nil, fmt.Errorf("shared catalogue authorization migration: %s %q is soft-deleted", seed.accessType, seed.path)
	}
	if err := tx.Table(table).Where("id = ?", menu.ID).Updates(map[string]any{
		"name":         seed.name,
		"method":       seed.method,
		"parent_id":    seed.parentID,
		"icon":         seed.icon,
		"permission":   seed.permission,
		"status":       enum.Enabled,
		"sort":         seed.sort,
		"hide_in_menu": seed.hidden,
		"updated_at":   time.Now(),
	}).Error; err != nil {
		return nil, fmt.Errorf("shared catalogue authorization migration: update %s %q", seed.accessType, seed.path)
	}
	return menu, nil
}

func seedAuthorizationRule(
	tx *gorm.DB,
	binding fixedbinding.Binding,
	roleID string,
	accessType adminpkg.AccessType,
	path string,
	method string,
) error {
	if strings.TrimSpace(roleID) == "" {
		return errors.New("shared catalogue authorization migration: role ID is empty")
	}
	table := qualifiedCoreTable(binding, (&models.CasbinRule{}).TableName())
	where := []any{"p", roleID, accessType.String(), path, method}
	var count int64
	if err := tx.Table(table).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		where...,
	).Count(&count).Error; err != nil {
		return fmt.Errorf("shared catalogue authorization migration: inspect %s %s %s", accessType, method, path)
	}
	if count > 0 {
		return nil
	}
	rule := &models.CasbinRule{PType: "p", V0: roleID, V1: accessType.String(), V2: path, V3: method}
	if err := tx.Table(table).Create(rule).Error; err != nil {
		return fmt.Errorf("shared catalogue authorization migration: seed %s %s %s", accessType, method, path)
	}
	return nil
}

func advanceAuthorizationRevision(tx *gorm.DB, binding fixedbinding.Binding, scope, ownerID string) error {
	table := qualifiedCoreTable(binding, (&models.ConfigRevision{}).TableName())
	var current models.ConfigRevision
	err := tx.Table(table).Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"scope = ? AND owner_id = ? AND resource = ?",
		scope,
		ownerID,
		authorizationResource,
	).Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		current = models.ConfigRevision{
			Scope: scope, OwnerID: ownerID, Resource: authorizationResource,
			Revision: 0, UpdatedAt: time.Now(),
		}
		if err := tx.Table(table).Create(&current).Error; err != nil {
			return fmt.Errorf("shared catalogue authorization migration: ensure revision %s/%s", scope, ownerID)
		}
	} else if err != nil {
		return fmt.Errorf("shared catalogue authorization migration: lock revision %s/%s", scope, ownerID)
	}
	if current.Revision < 0 || current.Revision == 1<<63-1 {
		return fmt.Errorf("shared catalogue authorization migration: revision cannot advance for %s/%s", scope, ownerID)
	}
	result := tx.Table(table).Where(
		"scope = ? AND owner_id = ? AND resource = ? AND revision = ?",
		scope,
		ownerID,
		authorizationResource,
		current.Revision,
	).Updates(map[string]any{"revision": current.Revision + 1, "updated_at": time.Now()})
	if result.Error != nil || result.RowsAffected != 1 {
		return fmt.Errorf("shared catalogue authorization migration: advance revision %s/%s", scope, ownerID)
	}
	return nil
}

func verifyAuthorizationReadiness(ctx context.Context, db *gorm.DB, binding fixedbinding.Binding, registry legacydb.Registry) error {
	if ctx == nil || db == nil {
		return errors.New("authorization database/context is unavailable")
	}
	var versions int64
	if err := db.WithContext(ctx).Table(qualifiedCoreTable(binding, "mss_boot_migration")).
		Where("version IN ?", []string{
			AuthorizationMigrationID.String(), MenuLocalizationMigrationID.String(), CapabilityLockdownMigrationID.String(),
		}).Count(&versions).Error; err != nil || versions != 3 {
		return errors.New("authorization migrations are not applied")
	}
	adminRole, err := resolveReadinessRole(ctx, db, binding, "admin")
	if err != nil {
		return err
	}
	required := authorizationRequirements(registry)
	menuTable := qualifiedCoreTable(binding, (&models.Menu{}).TableName())
	for _, item := range required {
		var menus []models.Menu
		err := db.WithContext(ctx).Table(menuTable).Unscoped().Where(
			"type = ? AND path = ? AND method = ?",
			item.accessType,
			item.path,
			item.method,
		).Order("id").Limit(2).Find(&menus).Error
		if err != nil || len(menus) != 1 {
			return fmt.Errorf("required authorization menu is unavailable for %s %s", item.method, item.path)
		}
		menu := menus[0]
		if menu.DeletedAt.Valid || menu.Status != enum.Enabled || menu.Permission != item.permission {
			return fmt.Errorf("required authorization menu is inactive for %s %s", item.method, item.path)
		}
		if item.accessType == adminpkg.DirectoryAccessType && item.path == sharedCatalogRootPath && menu.Name != sharedCatalogMenuNameToken {
			return errors.New("localized shared catalogue root menu is unavailable")
		}
	}
	policyTable := qualifiedCoreTable(binding, (&models.CasbinRule{}).TableName())
	for _, policy := range required {
		var count int64
		if err := db.WithContext(ctx).Table(policyTable).Where(
			"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
			"p", adminRole.ID, policy.accessType.String(), policy.path, policy.method,
		).Count(&count).Error; err != nil || count != 1 {
			return fmt.Errorf("required authorization policy is unavailable for %s %s", policy.method, policy.path)
		}
	}
	return verifyCapabilityLockdownReadiness(ctx, db, binding, registry)
}

type authorizationRequirement struct {
	accessType adminpkg.AccessType
	path       string
	method     string
	permission string
}

func authorizationRequirements(registry legacydb.Registry) []authorizationRequirement {
	required := make([]authorizationRequirement, 0, 27)
	required = append(required, authorizationRequirement{
		accessType: adminpkg.DirectoryAccessType,
		path:       sharedCatalogRootPath, method: httpGet,
		permission: PermissionCode("brands", string(actionRead)),
	})
	for _, definition := range registry.All() {
		resource := definition.Resource
		required = append(required, authorizationRequirement{
			accessType: adminpkg.MenuAccessType,
			path:       menuPath(resource.Name), method: httpGet,
			permission: PermissionCode(resource.Name, string(actionRead)),
		})
		for _, operation := range []string{"list", "read"} {
			permissionOperation := operation
			if operation == "list" {
				permissionOperation = string(actionRead)
			}
			required = append(required, authorizationRequirement{
				accessType: adminpkg.ComponentAccessType,
				path:       componentPath(resource.Name, operation), method: httpGet,
				permission: PermissionCode(resource.Name, permissionOperation),
			})
		}
	}
	for _, route := range readOnlyAuthorizationRoutes() {
		required = append(required, authorizationRequirement{
			accessType: adminpkg.APIAccessType,
			path:       route.Path, method: route.Method, permission: route.Permission,
		})
	}
	return required
}

// The 120000 migration is already published. Preserve its original 42-row
// projection for deterministic replay, then let 120002 revoke every write
// grant. Do not derive this historical snapshot from the current read-only
// resource capabilities.
func publishedAuthorizationComponentOperations(resource string) []string {
	operations := []string{"list", "read"}
	switch resource {
	case "brands", "classes", "couriers", "payments":
		operations = append(operations, "create", "update", "delete")
	}
	return operations
}

func readOnlyAuthorizationRoutes() []operationRoute {
	routes := make([]operationRoute, 0, 2)
	for _, route := range operationRoutes {
		if route.Action == actionRead {
			routes = append(routes, route)
		}
	}
	return routes
}

func resolveReadinessRole(ctx context.Context, db *gorm.DB, binding fixedbinding.Binding, name string) (*models.Role, error) {
	var roles []models.Role
	if err := db.WithContext(ctx).Table(qualifiedCoreTable(binding, (&models.Role{}).TableName())).Unscoped().
		Where("name = ?", name).Order("id").Limit(2).Find(&roles).Error; err != nil || len(roles) != 1 {
		return nil, fmt.Errorf("authorization role %q is unavailable", name)
	}
	if roles[0].DeletedAt.Valid || roles[0].Status != enum.Enabled {
		return nil, fmt.Errorf("authorization role %q is inactive", name)
	}
	return &roles[0], nil
}
