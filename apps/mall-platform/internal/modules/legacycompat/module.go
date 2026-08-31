package legacycompat

import (
	"context"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/mss-boot-io/mss-boot-admin/admin/models"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
	"gorm.io/gorm"
)

type module struct {
	binding  *fixedbinding.Resolver
	registry legacydb.Registry
}

type descriptorModel struct{}

func Module() business.Module {
	return NewModule(fixedbinding.EnvironmentSource)
}

func NewModule(source fixedbinding.Source) business.Module {
	return &module{binding: fixedbinding.NewResolver(source), registry: legacydb.DefaultRegistry()}
}

func (*module) Name() string { return moduleName }

func (legacy *module) Register(registry *business.Registry) error {
	if registry == nil {
		return errors.New("legacy compatibility business registry is required")
	}
	if legacy == nil || legacy.binding == nil || len(legacy.registry.All()) != legacydb.ExpectedMallResourceCount {
		return errors.New("legacy compatibility module is incomplete")
	}
	return registry.Register(business.Registration{
		Descriptor: legacy.descriptor(),
		Migrations: RegisterMigration,
		Readiness:  legacy.verifyReadiness,
		Routes:     legacy.registerRoutes,
	})
}

func (legacy *module) descriptor() business.Descriptor {
	permissions := make([]business.Permission, 0, legacydb.ExpectedMallResourceCount*2)
	for _, definition := range legacy.registry.All() {
		for _, operation := range operationsFor(definition) {
			permissions = append(permissions, business.Permission{
				Code:         Permission(definition.Resource.Name, operation),
				DisplayName:  definition.Resource.TitleKey + "." + string(operation),
				DefaultRoles: []string{"admin"},
			})
		}
	}
	return business.Descriptor{
		Name:        moduleName,
		DisplayName: "存量商城兼容",
		Description: "使用固定租户与全限定 schema 只读访问存量商城表；业务写流程待逐项恢复旧语义后开放。",
		Version:     "v1alpha1",
		Model:       new(descriptorModel),
		Permissions: permissions,
		Menu: business.Menu{
			Path: businessMenuRoot, DisplayName: "业务管理", DisplayNameEn: "Business", Icon: "database", Order: 10,
		},
	}
}

func (legacy *module) verifyReadiness(ctx context.Context, db *gorm.DB) error {
	binding, err := legacy.binding.Resolve()
	if err != nil {
		return fmt.Errorf("legacy compatibility readiness: %w", err)
	}
	if err := business.RequireAppliedMigrations(ctx, db, AuthorizationMigrationID, MenuLocalizationMigrationID, CapabilityLockdownMigrationID); err != nil {
		return fmt.Errorf("legacy compatibility readiness: %w", err)
	}
	if err := verifyMenuLocalizationReadiness(ctx, db); err != nil {
		return fmt.Errorf("legacy compatibility readiness: %w", err)
	}
	if err := verifyCapabilityLockdownReadiness(ctx, db); err != nil {
		return fmt.Errorf("legacy compatibility readiness: %w", err)
	}
	if !db.WithContext(ctx).Migrator().HasTable(new(models.CasbinRule)) {
		return errors.New("legacy compatibility readiness: MSS policy table is unavailable")
	}
	if err := legacydb.VerifyReadiness(ctx, db, binding, legacy.registry); err != nil {
		return fmt.Errorf("legacy compatibility readiness: %w", err)
	}
	return nil
}

func (legacy *module) registerRoutes(group *gin.RouterGroup, runtime business.Runtime) error {
	binding, err := legacy.binding.Resolve()
	if err != nil {
		return fmt.Errorf("legacy compatibility routes: %w", err)
	}
	authorizer, err := NewAdminAuthorizer(runtime.RequestDatabase, runtime.Principal, binding, legacy.registry)
	if err != nil {
		return fmt.Errorf("legacy compatibility routes: %w", err)
	}
	resolver := requestRepositoryResolver{database: runtime.RequestDatabase, binding: binding, registry: legacy.registry}
	if err := RegisterRoutes(group, resolver, authorizer, legacy.registry); err != nil {
		return fmt.Errorf("legacy compatibility routes: %w", err)
	}
	return nil
}
