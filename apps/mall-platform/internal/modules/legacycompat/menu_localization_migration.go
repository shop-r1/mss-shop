package legacycompat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/enum"
	migrationmodels "github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MSS normalizes dot-delimited names to their final segment before the web
// client resolves menu.<name>, so the persisted root name must be one unique
// dot-free token.
const businessMenuNameToken = "legacyBusiness"

// migrateMenuLocalization deliberately follows the already-published
// authorization migration instead of rewriting its historical behavior.
func migrateMenuLocalization(db *gorm.DB, version string) error {
	if db == nil {
		return errors.New("legacy menu localization migration database is required")
	}
	if version != MenuLocalizationMigrationID.String() {
		return errors.New("legacy menu localization migration version mismatch")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version = ?", version).Count(&applied).Error; err != nil {
			return fmt.Errorf("legacy menu localization migration: check version: %w", err)
		}
		if applied > 0 {
			return nil
		}

		var prerequisite int64
		if err := tx.Model(new(migrationmodels.Migration)).Where("version = ?", AuthorizationMigrationID.String()).Count(&prerequisite).Error; err != nil {
			return fmt.Errorf("legacy menu localization migration: check authorization prerequisite: %w", err)
		}
		if prerequisite != 1 {
			return errors.New("legacy menu localization migration: authorization prerequisite is not applied")
		}

		var matches []models.Menu
		if err := tx.Unscoped().Where(
			"type = ? AND path = ?",
			adminpkg.DirectoryAccessType,
			businessMenuRoot,
		).Order("id").Limit(2).Find(&matches).Error; err != nil {
			return fmt.Errorf("legacy menu localization migration: resolve business root: %w", err)
		}
		if len(matches) != 1 {
			return fmt.Errorf("legacy menu localization migration: business root count is %d", len(matches))
		}
		root := &matches[0]
		if root.DeletedAt.Valid || root.Status != enum.Enabled {
			return errors.New("legacy menu localization migration: business root is inactive")
		}
		if err := tx.Model(root).Updates(map[string]any{
			"name":       businessMenuNameToken,
			"updated_at": time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("legacy menu localization migration: update business root: %w", err)
		}

		adminRole, err := resolveAdminRole(tx)
		if err != nil {
			return err
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
			return fmt.Errorf("legacy menu localization migration: record version: %w", err)
		}
		return nil
	})
}

func verifyMenuLocalizationReadiness(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil {
		return errors.New("legacy menu localization readiness database/context is unavailable")
	}
	var matches []models.Menu
	if err := db.WithContext(ctx).Unscoped().Where(
		"type = ? AND path = ?",
		adminpkg.DirectoryAccessType,
		businessMenuRoot,
	).Order("id").Limit(2).Find(&matches).Error; err != nil {
		return fmt.Errorf("legacy menu localization readiness: resolve business root: %w", err)
	}
	if len(matches) != 1 {
		return fmt.Errorf("legacy menu localization readiness: business root count is %d", len(matches))
	}
	root := matches[0]
	if root.DeletedAt.Valid || root.Status != enum.Enabled || root.Name != businessMenuNameToken {
		return errors.New("legacy menu localization readiness: localized business root is unavailable")
	}
	return nil
}
