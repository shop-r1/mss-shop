package mallsettings

import (
	"context"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
)

type module struct {
	binding *fixedbinding.Resolver
}

type descriptorModel struct{}

func Module() business.Module {
	return NewModule(fixedbinding.EnvironmentSource)
}

func NewModule(source fixedbinding.Source) business.Module {
	return &module{binding: fixedbinding.NewResolver(source)}
}

func (*module) Name() string { return moduleName }

func (settings *module) Register(registry *business.Registry) error {
	if registry == nil {
		return errors.New("mall settings business registry is required")
	}
	if settings == nil || settings.binding == nil {
		return errors.New("mall settings module is incomplete")
	}
	return registry.Register(business.Registration{
		Descriptor: settings.descriptor(),
		Migrations: RegisterMigration,
		Readiness:  settings.verifyReadiness,
		Routes:     settings.registerRoutes,
	})
}

func (*module) descriptor() business.Descriptor {
	return business.Descriptor{
		Name:        moduleName,
		DisplayName: "商城通用设置",
		Description: "维护兼容存量 appConfig 的已审核非敏感商城通用设置。",
		Version:     "v1alpha1",
		Model:       new(descriptorModel),
		Permissions: []business.Permission{
			{Code: PermissionRead, DisplayName: "查看商城通用设置", DefaultRoles: []string{"admin"}},
			{Code: PermissionUpdate, DisplayName: "更新商城通用设置", DefaultRoles: []string{"admin"}},
		},
		Menu: business.Menu{
			Path: menuPath, DisplayName: "商城通用设置", DisplayNameEn: "Mall settings",
			Icon: "setting", Parent: "/business/settings", Order: 10,
		},
	}
}

func (settings *module) verifyReadiness(ctx context.Context, database *gorm.DB) error {
	binding, err := settings.binding.Resolve()
	if err != nil {
		return fmt.Errorf("mall settings readiness: %w", err)
	}
	if err := verifyRuntimeReadiness(ctx, database, binding); err != nil {
		return fmt.Errorf("mall settings readiness: %w", err)
	}
	return nil
}

func (settings *module) registerRoutes(group *gin.RouterGroup, runtime business.Runtime) error {
	binding, err := settings.binding.Resolve()
	if err != nil {
		return fmt.Errorf("mall settings routes: %w", err)
	}
	authorizer, err := NewAdminAuthorizer(runtime.RequestDatabase, runtime.Principal, binding)
	if err != nil {
		return fmt.Errorf("mall settings routes: %w", err)
	}
	application := &requestApplication{database: runtime.RequestDatabase, binding: binding}
	if err := RegisterRoutes(group, application, authorizer); err != nil {
		return fmt.Errorf("mall settings routes: %w", err)
	}
	return nil
}

type requestApplication struct {
	database business.RequestDatabase
	binding  fixedbinding.Binding
}

func (application *requestApplication) repository(ctx context.Context) (*Repository, error) {
	if application == nil || application.database == nil || ctx == nil {
		return nil, ErrPersistence
	}
	database, available := application.database(ctx)
	if !available || database == nil {
		return nil, ErrPersistence
	}
	repository, err := NewRepository(database, application.binding)
	if err != nil {
		return nil, err
	}
	return repository, nil
}

func (application *requestApplication) GetGeneral(ctx context.Context) (GeneralSettings, error) {
	repository, err := application.repository(ctx)
	if err != nil {
		return GeneralSettings{}, err
	}
	return repository.GetGeneral(ctx)
}

func (application *requestApplication) PutGeneral(
	ctx context.Context,
	input UpdateGeneralSettingsInput,
) (GeneralSettings, error) {
	settings, err := input.settings()
	if err != nil {
		return GeneralSettings{}, err
	}
	repository, err := application.repository(ctx)
	if err != nil {
		return GeneralSettings{}, err
	}
	return repository.PutGeneral(ctx, settings)
}
