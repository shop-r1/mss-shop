package memberlevels

import (
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/adminprojection"
)

// AuthorizationMigrationID follows Mall Settings in the forward-only Mall
// Platform sequence and projects no legacy business-table DDL.
const AuthorizationMigrationID migration.MigrationID = "66966149766805"

var authorizationProjection = adminprojection.Projection{
	Name:        "member levels",
	MigrationID: AuthorizationMigrationID,
	DefaultRole: adminprojection.RoleSeed{Name: "admin", Remark: "member levels default role"},
	Menus: []adminprojection.MenuSeed{
		{
			Name: "legacyBusiness", Path: "/business", Method: "GET",
			AccessType: adminpkg.DirectoryAccessType, Permission: "legacy.business.access",
			Icon: "database", Sort: 10,
		},
		{
			Name: "menu.legacy.domain.customers", Path: "/business/customers", Method: "GET",
			ParentPath: "/business", AccessType: adminpkg.DirectoryAccessType,
			Permission: "legacy.domain.customers", Sort: 10,
		},
		{
			Name: "memberLevels", Path: menuPath, Method: "GET",
			ParentPath: "/business/customers", AccessType: adminpkg.MenuAccessType,
			Permission: PermissionList, Icon: "team", Sort: 20,
		},
		{Name: PermissionList, Path: listComponent, Method: "GET", ParentPath: menuPath, AccessType: adminpkg.ComponentAccessType, Permission: PermissionList, Hidden: true},
		{Name: PermissionRead, Path: readComponent, Method: "GET", ParentPath: menuPath, AccessType: adminpkg.ComponentAccessType, Permission: PermissionRead, Hidden: true},
		{Name: PermissionCreate, Path: createComponent, Method: "GET", ParentPath: menuPath, AccessType: adminpkg.ComponentAccessType, Permission: PermissionCreate, Hidden: true},
		{Name: PermissionUpdate, Path: updateComponent, Method: "GET", ParentPath: menuPath, AccessType: adminpkg.ComponentAccessType, Permission: PermissionUpdate, Hidden: true},
		{Name: PermissionSetDefault, Path: setDefaultComponent, Method: "GET", ParentPath: menuPath, AccessType: adminpkg.ComponentAccessType, Permission: PermissionSetDefault, Hidden: true},
		{Name: PermissionDelete, Path: deleteComponent, Method: "GET", ParentPath: menuPath, AccessType: adminpkg.ComponentAccessType, Permission: PermissionDelete, Hidden: true},
		{Name: "api.memberLevels.list", Path: collectionRoutePath, Method: "GET", ParentPath: listComponent, AccessType: adminpkg.APIAccessType, Permission: PermissionList, Hidden: true},
		{Name: "api.memberLevels.read", Path: itemRoutePath, Method: "GET", ParentPath: readComponent, AccessType: adminpkg.APIAccessType, Permission: PermissionRead, Hidden: true},
		{Name: "api.memberLevels.create", Path: collectionRoutePath, Method: "POST", ParentPath: createComponent, AccessType: adminpkg.APIAccessType, Permission: PermissionCreate, Hidden: true},
		{Name: "api.memberLevels.update", Path: itemRoutePath, Method: "PUT", ParentPath: updateComponent, AccessType: adminpkg.APIAccessType, Permission: PermissionUpdate, Hidden: true},
		{Name: "api.memberLevels.setDefault", Path: defaultRoutePath, Method: "PUT", ParentPath: setDefaultComponent, AccessType: adminpkg.APIAccessType, Permission: PermissionSetDefault, Hidden: true},
		{Name: "api.memberLevels.delete", Path: itemRoutePath, Method: "DELETE", ParentPath: deleteComponent, AccessType: adminpkg.APIAccessType, Permission: PermissionDelete, Hidden: true},
	},
	Routes: []adminprojection.RouteGrant{
		{Permission: PermissionList, Method: "GET", Path: collectionRoutePath, ComponentPath: listComponent},
		{Permission: PermissionRead, Method: "GET", Path: itemRoutePath, ComponentPath: readComponent},
		{Permission: PermissionCreate, Method: "POST", Path: collectionRoutePath, ComponentPath: createComponent},
		{Permission: PermissionUpdate, Method: "PUT", Path: itemRoutePath, ComponentPath: updateComponent},
		{Permission: PermissionSetDefault, Method: "PUT", Path: defaultRoutePath, ComponentPath: setDefaultComponent},
		{Permission: PermissionDelete, Method: "DELETE", Path: itemRoutePath, ComponentPath: deleteComponent},
	},
}

func RegisterMigration(runner *migration.Migration) error {
	return adminprojection.RegisterMigration(runner, authorizationProjection)
}
