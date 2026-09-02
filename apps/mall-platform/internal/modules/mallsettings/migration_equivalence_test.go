package mallsettings

import (
	"reflect"
	"testing"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/adminprojection"
)

func TestRuntimeAuthorizationProjectionMatchesPublishedMigrationContract(t *testing.T) {
	t.Parallel()

	if authorizationProjection.MigrationID != AuthorizationMigrationID {
		t.Fatalf(
			"runtime migration ID = %s, published migration ID = %s",
			authorizationProjection.MigrationID,
			AuthorizationMigrationID,
		)
	}
	if authorizationProjection.DefaultRole != (adminprojection.RoleSeed{
		Name: "admin", Remark: "mall settings default role",
	}) {
		t.Fatalf("runtime default role drifted: %#v", authorizationProjection.DefaultRole)
	}
	if len(authorizationProjection.Menus) != len(authorizationMenuSeeds) {
		t.Fatalf(
			"runtime menu count = %d, published menu count = %d",
			len(authorizationProjection.Menus),
			len(authorizationMenuSeeds),
		)
	}
	for index, published := range authorizationMenuSeeds {
		want := adminprojection.MenuSeed{
			Name: published.name, Path: published.path, Method: published.method,
			ParentPath: published.parentPath, AccessType: published.accessType,
			Permission: published.permission, Icon: published.icon,
			Sort: published.sort, Hidden: published.hidden,
		}
		if got := authorizationProjection.Menus[index]; !reflect.DeepEqual(got, want) {
			t.Fatalf("runtime menu %d = %#v, published menu = %#v", index, got, want)
		}
	}

	wantRoutes := []adminprojection.RouteGrant{
		{
			Permission: PermissionRead, Method: "GET", Path: generalRoutePath,
			ComponentPath: readComponent,
		},
		{
			Permission: PermissionUpdate, Method: "PUT", Path: generalRoutePath,
			ComponentPath: updateComponent,
		},
	}
	if !reflect.DeepEqual(authorizationProjection.Routes, wantRoutes) {
		t.Fatalf("runtime routes = %#v, want %#v", authorizationProjection.Routes, wantRoutes)
	}
}
