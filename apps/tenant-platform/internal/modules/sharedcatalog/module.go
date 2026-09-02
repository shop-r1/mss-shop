package sharedcatalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
	"gorm.io/gorm"
)

const ModuleName = "sharedcatalog"

type descriptorModel struct{}

func (*descriptorModel) TableName() string { return "shared_catalog_compatibility" }

type module struct {
	resolver *fixedbinding.Resolver
	registry legacydb.Registry
}

// Module uses only server environment at startup.
func Module() business.Module {
	return NewModule(fixedbinding.NewResolver(fixedbinding.EnvironmentSource))
}

// NewModule exists for deterministic composition and negative binding tests.
func NewModule(resolver *fixedbinding.Resolver) business.Module {
	return &module{resolver: resolver, registry: legacydb.DefaultRegistry()}
}

func (*module) Name() string { return ModuleName }

func (current *module) Register(registry *business.Registry) error {
	if registry == nil {
		return errors.New("shared catalogue business registry is required")
	}
	if current == nil || current.resolver == nil {
		return errors.New("shared catalogue fixed binding resolver is required")
	}
	if len(current.registry.All()) != legacydb.ExpectedSharedResourceCount {
		return errors.New("shared catalogue reviewed registry is incomplete")
	}
	return registry.Register(business.Registration{
		Descriptor: business.Descriptor{
			Name:        ModuleName,
			DisplayName: "Platform payment catalogue compatibility",
			Description: "Schema-qualified read-only compatibility access to the platform payment catalogue",
			Version:     "1.0.0",
			Model:       new(descriptorModel),
			Permissions: businessPermissions(current.registry),
			Menu: business.Menu{
				Path:          "/business/shared-catalog",
				DisplayName:   "平台支付目录",
				DisplayNameEn: "Platform payment catalogue",
				Icon:          "shop",
				Order:         100,
				Hidden:        true,
			},
		},
		Migrations: current.registerMigration,
		Readiness:  current.verifyReadiness,
		Routes:     current.registerRoutes,
	})
}

func (current *module) registerMigration(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("shared catalogue migration runner is required")
	}
	if err := runner.Register(AuthorizationMigrationID, func(db *gorm.DB, version string) error {
		binding, err := current.resolver.Resolve()
		if err != nil {
			return fmt.Errorf("shared catalogue fixed binding: %w", err)
		}
		return applyAuthorizationMigration(db, binding, legacydb.PublishedRegistry(), version, nil)
	}); err != nil {
		return err
	}
	if err := runner.Register(MenuLocalizationMigrationID, func(db *gorm.DB, version string) error {
		binding, err := current.resolver.Resolve()
		if err != nil {
			return fmt.Errorf("shared catalogue fixed binding: %w", err)
		}
		return applyMenuLocalizationMigration(db, binding, version)
	}); err != nil {
		return err
	}
	if err := runner.Register(CapabilityLockdownMigrationID, func(db *gorm.DB, version string) error {
		binding, err := current.resolver.Resolve()
		if err != nil {
			return fmt.Errorf("shared catalogue fixed binding: %w", err)
		}
		return applyCapabilityLockdownMigration(db, binding, legacydb.PublishedRegistry(), version, nil)
	}); err != nil {
		return err
	}
	return runner.Register(OwnershipTransferMigrationID, func(db *gorm.DB, version string) error {
		binding, err := current.resolver.Resolve()
		if err != nil {
			return fmt.Errorf("shared catalogue fixed binding: %w", err)
		}
		return applyOwnershipTransferMigration(db, binding, legacydb.PublishedRegistry(), current.registry, version, nil)
	})
}

func (current *module) verifyReadiness(ctx context.Context, db *gorm.DB) error {
	if ctx == nil {
		return errors.New("shared catalogue readiness context is required")
	}
	binding, err := current.resolver.Resolve()
	if err != nil {
		return fmt.Errorf("shared catalogue fixed binding readiness: %w", err)
	}
	if err := legacydb.VerifyReadiness(ctx, db, binding, current.registry); err != nil {
		return fmt.Errorf("shared catalogue legacy readiness: %w", err)
	}
	if err := verifyAuthorizationReadiness(ctx, db, binding, current.registry); err != nil {
		return fmt.Errorf("shared catalogue authorization readiness: %w", err)
	}
	return nil
}

func (current *module) registerRoutes(group *gin.RouterGroup, runtime business.Runtime) error {
	if group == nil {
		return errors.New("shared catalogue protected route group is required")
	}
	binding, err := current.resolver.Resolve()
	if err != nil {
		return fmt.Errorf("shared catalogue fixed binding routes: %w", err)
	}
	return RegisterRoutes(group, runtime, binding, current.registry)
}
