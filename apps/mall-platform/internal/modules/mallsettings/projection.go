package mallsettings

import (
	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/adminprojection"
)

// authorizationProjection mirrors the immutable, already-published
// AuthorizationMigrationID data contract. The published migration keeps its
// original implementation; this projection is used only by shared runtime
// authorization/readiness code and is guarded by an equivalence test.
var authorizationProjection = adminprojection.Projection{
	Name:        "mall settings",
	MigrationID: AuthorizationMigrationID,
	DefaultRole: adminprojection.RoleSeed{Name: "admin", Remark: "mall settings default role"},
	Menus: []adminprojection.MenuSeed{
		{
			Name: "legacyBusiness", Path: "/business", Method: "GET",
			AccessType: adminpkg.DirectoryAccessType, Permission: "legacy.business.access",
			Icon: "database", Sort: 10,
		},
		{
			Name: "menu.legacy.domain.settings", Path: "/business/settings", Method: "GET",
			ParentPath: "/business", AccessType: adminpkg.DirectoryAccessType,
			Permission: "legacy.domain.settings", Sort: 2,
		},
		{
			Name: "mallSettings", Path: menuPath, Method: "GET",
			ParentPath: "/business/settings", AccessType: adminpkg.MenuAccessType,
			Permission: PermissionRead, Icon: "setting", Sort: 10,
		},
		{
			Name: "mall-settings:read", Path: readComponent, Method: "GET",
			ParentPath: menuPath, AccessType: adminpkg.ComponentAccessType,
			Permission: PermissionRead, Hidden: true,
		},
		{
			Name: "mall-settings:update", Path: updateComponent, Method: "GET",
			ParentPath: menuPath, AccessType: adminpkg.ComponentAccessType,
			Permission: PermissionUpdate, Hidden: true,
		},
		{
			Name: "api.mallSettings.general.read", Path: generalRoutePath, Method: "GET",
			ParentPath: readComponent, AccessType: adminpkg.APIAccessType,
			Permission: PermissionRead, Hidden: true,
		},
		{
			Name: "api.mallSettings.general.update", Path: generalRoutePath, Method: "PUT",
			ParentPath: updateComponent, AccessType: adminpkg.APIAccessType,
			Permission: PermissionUpdate, Hidden: true,
		},
	},
	Routes: []adminprojection.RouteGrant{
		{
			Permission: PermissionRead, Method: "GET", Path: generalRoutePath,
			ComponentPath: readComponent,
		},
		{
			Permission: PermissionUpdate, Method: "PUT", Path: generalRoutePath,
			ComponentPath: updateComponent,
		},
	},
}
