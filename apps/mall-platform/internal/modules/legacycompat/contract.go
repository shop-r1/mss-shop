// Package legacycompat composes the fixed legacy repository into the MSS
// BusinessModule extension boundary. It owns HTTP/RBAC integration only; old
// business workflow semantics remain in explicit domain modules.
package legacycompat

import (
	"fmt"
	"strings"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
)

type Operation string

const (
	OperationList   Operation = "list"
	OperationRead   Operation = "read"
	OperationCreate Operation = "create"
	OperationUpdate Operation = "update"
	OperationDelete Operation = "delete"
)

const (
	moduleName          = "legacy-compat"
	businessMenuRoot    = "/business"
	collectionRoutePath = "/admin/api/legacy/resources/:resource"
	detailRoutePath     = "/admin/api/legacy/resources/:resource/:id"
)

func Permission(resource string, operation Operation) string {
	return "legacy." + resource + "." + string(operation)
}

func ComponentPath(resource string, operation Operation) string {
	definition, ok := legacydb.DefaultRegistry().Lookup(resource)
	if !ok {
		return ""
	}
	return componentPath(definition, operation)
}

func menuPath(definition legacydb.Definition) string {
	return businessMenuRoot + "/" + definition.Resource.Domain + "/" + definition.Resource.Name
}

func domainMenuPath(domain string) string {
	return businessMenuRoot + "/" + domain
}

func componentPath(definition legacydb.Definition, operation Operation) string {
	return menuPath(definition) + "/permissions/" + string(operation)
}

func operationsFor(definition legacydb.Definition) []Operation {
	operations := []Operation{OperationList}
	if hasDetail(definition) {
		operations = append(operations, OperationRead)
	}
	capabilities := definition.Resource.Capabilities
	if capabilities.Create {
		operations = append(operations, OperationCreate)
	}
	if capabilities.Update {
		operations = append(operations, OperationUpdate)
	}
	if capabilities.Delete {
		operations = append(operations, OperationDelete)
	}
	return operations
}

func operationAllowed(definition legacydb.Definition, operation Operation) bool {
	switch operation {
	case OperationList:
		return true
	case OperationRead:
		return hasDetail(definition)
	case OperationCreate:
		return definition.Resource.Capabilities.Create
	case OperationUpdate:
		return definition.Resource.Capabilities.Update && len(definition.Resource.PrimaryKey) == 1
	case OperationDelete:
		return definition.Resource.Capabilities.Delete && len(definition.Resource.PrimaryKey) == 1
	default:
		return false
	}
}

func hasDetail(definition legacydb.Definition) bool {
	return definition.Resource.Capabilities.Detail
}

func routeFor(operation Operation) (method, path string, ok bool) {
	switch operation {
	case OperationList:
		return "GET", collectionRoutePath, true
	case OperationRead:
		return "GET", detailRoutePath, true
	case OperationCreate:
		return "POST", collectionRoutePath, true
	case OperationUpdate:
		return "PUT", detailRoutePath, true
	case OperationDelete:
		return "DELETE", detailRoutePath, true
	default:
		return "", "", false
	}
}

func validateResourceName(name string) error {
	if name == "" || name != strings.TrimSpace(name) {
		return fmt.Errorf("invalid resource name")
	}
	return nil
}
