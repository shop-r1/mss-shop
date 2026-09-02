package sharedcatalog

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
)

type action string

const (
	actionRead   action = "read"
	actionCreate action = "create"
	actionUpdate action = "update"
	actionDelete action = "delete"
)

const (
	collectionRoute = "/admin/api/legacy/resources/:resource"
	detailRoute     = "/admin/api/legacy/resources/:resource/:id"
)

type permissionDefinition struct {
	Code         string
	Resource     string
	Action       action
	DisplayName  string
	DefaultRoles []string
}

// PermissionCode is retained for published migration compatibility. Runtime
// descriptors expose only the base read permission for each resource.
func PermissionCode(resource string, operation string) string {
	base := "/legacy/resources/" + resource
	if operation == "" || operation == string(actionRead) {
		return base
	}
	return base + "/" + operation
}

func permissionDefinitions(registry legacydb.Registry) []permissionDefinition {
	definitions := make([]permissionDefinition, 0, legacydb.ExpectedSharedResourceCount)
	for _, definition := range registry.All() {
		resource := definition.Resource
		definitions = append(definitions, permissionDefinition{
			Code:         PermissionCode(resource.Name, string(actionRead)),
			Resource:     resource.Name,
			Action:       actionRead,
			DisplayName:  fmt.Sprintf("Read shared %s", resource.Name),
			DefaultRoles: []string{"admin"},
		})
	}
	sort.SliceStable(definitions, func(left, right int) bool {
		return definitions[left].Code < definitions[right].Code
	})
	return definitions
}

func businessPermissions(registry legacydb.Registry) []business.Permission {
	definitions := permissionDefinitions(registry)
	permissions := make([]business.Permission, 0, len(definitions))
	for _, definition := range definitions {
		permissions = append(permissions, business.Permission{
			Code:         definition.Code,
			DisplayName:  definition.DisplayName,
			Description:  "MSS-enforced shared catalogue permission",
			DefaultRoles: append([]string(nil), definition.DefaultRoles...),
		})
	}
	return permissions
}

func permissionIndex(registry legacydb.Registry) map[string]permissionDefinition {
	result := make(map[string]permissionDefinition, len(permissionDefinitions(registry)))
	for _, definition := range permissionDefinitions(registry) {
		result[definition.Code] = definition
	}
	return result
}

type operationRoute struct {
	Name               string
	Method             string
	Path               string
	Permission         string
	Action             action
	ComponentOperation string
}

var operationRoutes = []operationRoute{
	{Name: "api.sharedcatalog.list", Method: http.MethodGet, Path: collectionRoute, Permission: "sharedcatalog:read", Action: actionRead, ComponentOperation: "list"},
	{Name: "api.sharedcatalog.detail", Method: http.MethodGet, Path: detailRoute, Permission: "sharedcatalog:read", Action: actionRead, ComponentOperation: "read"},
	{Name: "api.sharedcatalog.create", Method: http.MethodPost, Path: collectionRoute, Permission: "sharedcatalog:create", Action: actionCreate, ComponentOperation: "create"},
	{Name: "api.sharedcatalog.update", Method: http.MethodPut, Path: detailRoute, Permission: "sharedcatalog:update", Action: actionUpdate, ComponentOperation: "update"},
	{Name: "api.sharedcatalog.delete", Method: http.MethodDelete, Path: detailRoute, Permission: "sharedcatalog:delete", Action: actionDelete, ComponentOperation: "delete"},
}

func routeForRequest(method, path string, operation action) (operationRoute, bool) {
	for _, route := range operationRoutes {
		if route.Method == method && route.Path == path && route.Action == operation {
			return route, true
		}
	}
	return operationRoute{}, false
}

func menuPath(resource string) string {
	return "/business/shared-catalog/" + resource
}

func componentPath(resource, operation string) string {
	return menuPath(resource) + "/permissions/" + operation
}
