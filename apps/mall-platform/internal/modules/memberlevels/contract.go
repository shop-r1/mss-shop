// Package memberlevels restores the reviewed member-level workflow while the
// legacy member_levels relation remains the business source of truth.
package memberlevels

const (
	moduleName = "memberlevels"

	PermissionList       = "member-levels:list"
	PermissionRead       = "member-levels:read"
	PermissionCreate     = "member-levels:create"
	PermissionUpdate     = "member-levels:update"
	PermissionSetDefault = "member-levels:set-default"
	PermissionDelete     = "member-levels:delete"

	collectionRoutePath = "/admin/api/member-levels"
	itemRoutePath       = "/admin/api/member-levels/:id"
	defaultRoutePath    = itemRoutePath + "/default"
	menuPath            = "/business/customers/member-levels"

	listComponent       = menuPath + "/permissions/list"
	readComponent       = menuPath + "/permissions/read"
	createComponent     = menuPath + "/permissions/create"
	updateComponent     = menuPath + "/permissions/update"
	setDefaultComponent = menuPath + "/permissions/set-default"
	deleteComponent     = menuPath + "/permissions/delete"

	legacyEnabledStatus  = int64(1)
	legacyDisabledStatus = int64(2)
)

type authorizationRoute struct {
	method        string
	path          string
	componentPath string
}

var authorizationRoutes = map[string]authorizationRoute{
	PermissionList:       {method: "GET", path: collectionRoutePath, componentPath: listComponent},
	PermissionRead:       {method: "GET", path: itemRoutePath, componentPath: readComponent},
	PermissionCreate:     {method: "POST", path: collectionRoutePath, componentPath: createComponent},
	PermissionUpdate:     {method: "PUT", path: itemRoutePath, componentPath: updateComponent},
	PermissionSetDefault: {method: "PUT", path: defaultRoutePath, componentPath: setDefaultComponent},
	PermissionDelete:     {method: "DELETE", path: itemRoutePath, componentPath: deleteComponent},
}
