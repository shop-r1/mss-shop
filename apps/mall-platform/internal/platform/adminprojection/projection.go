// Package adminprojection projects one handwritten business module's fixed
// menu and Casbin contract into the MSS Admin core. It deliberately does not
// provide generic CRUD, legacy-table access, or request-controlled scope.
package adminprojection

import (
	"errors"
	"fmt"
	"strings"

	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
)

type RoleSeed struct {
	Name   string
	Remark string
}

type MenuSeed struct {
	Name       string
	Path       string
	Method     string
	ParentPath string
	AccessType adminpkg.AccessType
	Permission string
	Icon       string
	Sort       int
	Hidden     bool
}

type RouteGrant struct {
	Permission    string
	Method        string
	Path          string
	ComponentPath string
}

// Projection is a closed authorization contract. Menu seeds must be ordered
// parent before child so migration and readiness resolve the same tree.
type Projection struct {
	Name        string
	MigrationID migration.MigrationID
	DefaultRole RoleSeed
	Menus       []MenuSeed
	Routes      []RouteGrant
}

func (projection Projection) cloneAndValidate() (Projection, error) {
	projection.Name = strings.TrimSpace(projection.Name)
	projection.DefaultRole.Name = strings.TrimSpace(projection.DefaultRole.Name)
	projection.DefaultRole.Remark = strings.TrimSpace(projection.DefaultRole.Remark)
	projection.Menus = append([]MenuSeed(nil), projection.Menus...)
	projection.Routes = append([]RouteGrant(nil), projection.Routes...)
	if projection.Name == "" {
		return Projection{}, errors.New("Admin projection name is required")
	}
	if _, err := migration.ParseMigrationID(projection.MigrationID.String()); err != nil {
		return Projection{}, fmt.Errorf("%s projection migration ID: %w", projection.Name, err)
	}
	if projection.DefaultRole.Name == "" {
		return Projection{}, fmt.Errorf("%s projection default role is required", projection.Name)
	}
	if len(projection.Menus) == 0 || len(projection.Routes) == 0 {
		return Projection{}, fmt.Errorf("%s projection menus and routes are required", projection.Name)
	}

	menus := make(map[string]MenuSeed, len(projection.Menus))
	paths := make(map[string]MenuSeed, len(projection.Menus))
	for index := range projection.Menus {
		seed := projection.Menus[index]
		seed.Name = strings.TrimSpace(seed.Name)
		seed.Path = strings.TrimSpace(seed.Path)
		seed.Method = strings.ToUpper(strings.TrimSpace(seed.Method))
		seed.ParentPath = strings.TrimSpace(seed.ParentPath)
		seed.Permission = strings.TrimSpace(seed.Permission)
		seed.Icon = strings.TrimSpace(seed.Icon)
		if seed.Name == "" || seed.Path == "" || seed.Method == "" || seed.Permission == "" {
			return Projection{}, fmt.Errorf("%s projection menu %d is incomplete", projection.Name, index)
		}
		switch seed.AccessType {
		case adminpkg.DirectoryAccessType, adminpkg.MenuAccessType,
			adminpkg.ComponentAccessType, adminpkg.APIAccessType:
		default:
			return Projection{}, fmt.Errorf("%s projection menu %q has unsupported access type", projection.Name, seed.Path)
		}
		if (seed.AccessType == adminpkg.ComponentAccessType || seed.AccessType == adminpkg.APIAccessType) && !seed.Hidden {
			return Projection{}, fmt.Errorf("%s projection protected menu %q must be hidden", projection.Name, seed.Path)
		}
		if seed.ParentPath != "" {
			if _, exists := paths[seed.ParentPath]; !exists {
				return Projection{}, fmt.Errorf("%s projection parent %q must precede its child", projection.Name, seed.ParentPath)
			}
		}
		key := menuKey(seed.AccessType, seed.Path, seed.Method)
		if _, duplicate := menus[key]; duplicate {
			return Projection{}, fmt.Errorf("%s projection menu %q is duplicated", projection.Name, key)
		}
		projection.Menus[index] = seed
		menus[key] = seed
		if seed.AccessType != adminpkg.APIAccessType {
			if _, duplicate := paths[seed.Path]; duplicate {
				return Projection{}, fmt.Errorf("%s projection menu path %q is ambiguous", projection.Name, seed.Path)
			}
			paths[seed.Path] = seed
		}
	}

	permissions := make(map[string]struct{}, len(projection.Routes))
	usedProtectedMenus := make(map[string]struct{}, len(projection.Routes)*2)
	for index := range projection.Routes {
		route := projection.Routes[index]
		route.Permission = strings.TrimSpace(route.Permission)
		route.Method = strings.ToUpper(strings.TrimSpace(route.Method))
		route.Path = strings.TrimSpace(route.Path)
		route.ComponentPath = strings.TrimSpace(route.ComponentPath)
		if route.Permission == "" || route.Method == "" || route.Path == "" || route.ComponentPath == "" {
			return Projection{}, fmt.Errorf("%s projection route %d is incomplete", projection.Name, index)
		}
		if _, duplicate := permissions[route.Permission]; duplicate {
			return Projection{}, fmt.Errorf("%s projection permission %q is duplicated", projection.Name, route.Permission)
		}
		component, exists := menus[menuKey(adminpkg.ComponentAccessType, route.ComponentPath, "GET")]
		if !exists || component.Permission != route.Permission || component.Method != "GET" {
			return Projection{}, fmt.Errorf("%s projection route %q has no matching component", projection.Name, route.Permission)
		}
		api, exists := menus[menuKey(adminpkg.APIAccessType, route.Path, route.Method)]
		if !exists || api.Permission != route.Permission || api.ParentPath != route.ComponentPath {
			return Projection{}, fmt.Errorf("%s projection route %q has no matching API", projection.Name, route.Permission)
		}
		usedProtectedMenus[menuKey(adminpkg.ComponentAccessType, route.ComponentPath, "GET")] = struct{}{}
		usedProtectedMenus[menuKey(adminpkg.APIAccessType, route.Path, route.Method)] = struct{}{}
		projection.Routes[index] = route
		permissions[route.Permission] = struct{}{}
	}
	for key, seed := range menus {
		if seed.AccessType != adminpkg.ComponentAccessType && seed.AccessType != adminpkg.APIAccessType {
			continue
		}
		if _, used := usedProtectedMenus[key]; !used {
			return Projection{}, fmt.Errorf("%s projection protected menu %q has no matching route", projection.Name, key)
		}
	}
	return projection, nil
}

func menuKey(accessType adminpkg.AccessType, path, method string) string {
	if accessType == adminpkg.APIAccessType {
		return accessType.String() + ":" + path + "#" + method
	}
	return accessType.String() + ":" + path
}
