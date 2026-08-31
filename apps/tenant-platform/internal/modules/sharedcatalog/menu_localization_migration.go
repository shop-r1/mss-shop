package sharedcatalog

import (
	"errors"
	"fmt"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MSS normalizes dot-delimited names to their final segment before the web
// client resolves menu.<name>, so the persisted root name must be one unique
// dot-free token.
const sharedCatalogMenuNameToken = "sharedCatalog"

func applyMenuLocalizationMigration(db *gorm.DB, binding fixedbinding.Binding, version string) error {
	if db == nil {
		return errors.New("shared catalogue menu localization migration database is required")
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("shared catalogue menu localization migration binding: %w", err)
	}
	if version != MenuLocalizationMigrationID.String() {
		return errors.New("shared catalogue menu localization migration version mismatch")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		migrationTable := qualifiedCoreTable(binding, (&migrationmodels.Migration{}).TableName())
		var applied int64
		if err := tx.Table(migrationTable).Where("version = ?", version).Count(&applied).Error; err != nil {
			return errors.New("shared catalogue menu localization migration: check version")
		}
		if applied > 0 {
			return nil
		}

		var prerequisite int64
		if err := tx.Table(migrationTable).Where("version = ?", AuthorizationMigrationID.String()).Count(&prerequisite).Error; err != nil {
			return errors.New("shared catalogue menu localization migration: check authorization prerequisite")
		}
		if prerequisite != 1 {
			return errors.New("shared catalogue menu localization migration: authorization prerequisite is not applied")
		}

		menuTable := qualifiedCoreTable(binding, (&models.Menu{}).TableName())
		var matches []models.Menu
		if err := tx.Table(menuTable).Unscoped().Where(
			"type = ? AND path = ?",
			adminpkg.DirectoryAccessType,
			sharedCatalogRootPath,
		).Order("id").Limit(2).Find(&matches).Error; err != nil {
			return errors.New("shared catalogue menu localization migration: resolve root menu")
		}
		if len(matches) != 1 {
			return fmt.Errorf("shared catalogue menu localization migration: root menu count is %d", len(matches))
		}
		root := matches[0]
		if root.DeletedAt.Valid || root.Status != enum.Enabled {
			return errors.New("shared catalogue menu localization migration: root menu is inactive")
		}
		if err := tx.Table(menuTable).Where("id = ?", root.ID).Updates(map[string]any{
			"name":       sharedCatalogMenuNameToken,
			"updated_at": time.Now(),
		}).Error; err != nil {
			return errors.New("shared catalogue menu localization migration: update root menu")
		}

		adminRole, err := resolveAuthorizationRole(tx, binding, "admin")
		if err != nil {
			return err
		}
		if err := advanceAuthorizationRevision(tx, binding, authorizationScopeRole, adminRole.ID); err != nil {
			return err
		}
		if err := advanceAuthorizationRevision(tx, binding, authorizationScopeGlobal, ""); err != nil {
			return err
		}

		versionRow := new(migrationmodels.Migration)
		versionRow.SetVersion(version)
		if err := tx.Table(migrationTable).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "version"}},
			DoNothing: true,
		}).Create(versionRow).Error; err != nil {
			return errors.New("shared catalogue menu localization migration: record version")
		}
		return nil
	})
}
