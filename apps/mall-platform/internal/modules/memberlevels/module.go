package memberlevels

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
	binding   *fixedbinding.Resolver
	mutations mutationGate
}

type descriptorModel struct{}

func Module() business.Module {
	return NewModule(fixedbinding.EnvironmentSource)
}

func NewModule(source fixedbinding.Source) business.Module {
	return &module{
		binding:   fixedbinding.NewResolver(source),
		mutations: environmentMutationGate(),
	}
}

func (*module) Name() string { return moduleName }

func (levels *module) Register(registry *business.Registry) error {
	if registry == nil {
		return errors.New("member levels business registry is required")
	}
	if levels == nil || levels.binding == nil {
		return errors.New("member levels module is incomplete")
	}
	return registry.Register(business.Registration{
		Descriptor: levels.descriptor(),
		Migrations: RegisterMigration,
		Readiness:  levels.verifyReadiness,
		Routes:     levels.registerRoutes,
	})
}

func (*module) descriptor() business.Descriptor {
	return business.Descriptor{
		Name: moduleName, DisplayName: "会员等级", Version: "v1alpha1",
		Description: "在兼容存量 member_levels 的前提下维护等级、折扣、状态和事务默认值。",
		Model:       new(descriptorModel),
		Permissions: []business.Permission{
			{Code: PermissionList, DisplayName: "查看会员等级列表", DefaultRoles: []string{"admin"}},
			{Code: PermissionRead, DisplayName: "查看会员等级详情", DefaultRoles: []string{"admin"}},
			{Code: PermissionCreate, DisplayName: "创建会员等级", DefaultRoles: []string{"admin"}},
			{Code: PermissionUpdate, DisplayName: "更新会员等级", DefaultRoles: []string{"admin"}},
			{Code: PermissionSetDefault, DisplayName: "设置默认会员等级", DefaultRoles: []string{"admin"}},
			{Code: PermissionDelete, DisplayName: "删除会员等级", DefaultRoles: []string{"admin"}},
		},
		Menu: business.Menu{
			Path: menuPath, DisplayName: "会员等级", DisplayNameEn: "Member levels",
			Icon: "team", Parent: "/business/customers", Order: 20,
		},
	}
}

func (levels *module) verifyReadiness(ctx context.Context, database *gorm.DB) error {
	binding, err := levels.binding.Resolve()
	if err != nil {
		return fmt.Errorf("member levels readiness: %w", err)
	}
	if err := verifyRuntimeReadiness(ctx, database, binding); err != nil {
		return fmt.Errorf("member levels readiness: %w", err)
	}
	return nil
}

func (levels *module) registerRoutes(group *gin.RouterGroup, runtime business.Runtime) error {
	binding, err := levels.binding.Resolve()
	if err != nil {
		return fmt.Errorf("member levels routes: %w", err)
	}
	authorizer, err := NewAdminAuthorizer(runtime.RequestDatabase, runtime.Principal, binding)
	if err != nil {
		return fmt.Errorf("member levels routes: %w", err)
	}
	application := &requestApplication{
		database: runtime.RequestDatabase, binding: binding, mutations: levels.mutations,
	}
	if err := RegisterRoutes(group, application, authorizer); err != nil {
		return fmt.Errorf("member levels routes: %w", err)
	}
	return nil
}
