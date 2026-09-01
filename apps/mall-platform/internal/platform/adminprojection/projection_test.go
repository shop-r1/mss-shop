package adminprojection

import (
	"strings"
	"testing"

	adminpkg "github.com/mss-boot-io/mss-boot-admin/admin/pkg"
)

func TestCloneAndValidateCanonicalizesAnIndependentCopy(t *testing.T) {
	t.Parallel()

	projection := projectionTestContract()
	projection.Name = "  example  "
	projection.DefaultRole.Name = " admin "
	projection.DefaultRole.Remark = " example default role "
	for index := range projection.Menus {
		projection.Menus[index].Name = " " + projection.Menus[index].Name + " "
		projection.Menus[index].Path = " " + projection.Menus[index].Path + " "
		projection.Menus[index].Method = strings.ToLower(projection.Menus[index].Method)
		projection.Menus[index].ParentPath = " " + projection.Menus[index].ParentPath + " "
		projection.Menus[index].Permission = " " + projection.Menus[index].Permission + " "
		projection.Menus[index].Icon = " " + projection.Menus[index].Icon + " "
	}
	projection.Routes[0].Permission = " " + projection.Routes[0].Permission + " "
	projection.Routes[0].Method = "get"
	projection.Routes[0].Path = " " + projection.Routes[0].Path + " "
	projection.Routes[0].ComponentPath = " " + projection.Routes[0].ComponentPath + " "

	validated, err := projection.cloneAndValidate()
	if err != nil {
		t.Fatal(err)
	}
	if validated.Name != "example" || validated.DefaultRole.Name != "admin" ||
		validated.Menus[0].Method != "GET" || validated.Routes[0].Method != "GET" {
		t.Fatalf("validated projection was not canonicalized: %#v", validated)
	}
	if projection.Name != "  example  " || projection.Menus[0].Method != "get" {
		t.Fatal("clone validation mutated the caller-owned projection")
	}
	projection.Menus[0].Name = "mutated-after-clone"
	projection.Routes[0].Permission = "mutated-after-clone"
	if validated.Menus[0].Name != "business" || validated.Routes[0].Permission != projectionTestPermission {
		t.Fatal("validated projection retained caller-owned slice storage")
	}
}

func TestCloneAndValidateRejectsAmbiguousOrUnroutableContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Projection)
	}{
		{name: "missing role", mutate: func(value *Projection) { value.DefaultRole.Name = "" }},
		{name: "parent after child", mutate: func(value *Projection) {
			value.Menus[0], value.Menus[1] = value.Menus[1], value.Menus[0]
		}},
		{name: "unsupported access type", mutate: func(value *Projection) {
			value.Menus[0].AccessType = adminpkg.AccessType("unsupported")
		}},
		{name: "duplicate menu", mutate: func(value *Projection) {
			value.Menus = append(value.Menus, value.Menus[0])
		}},
		{name: "protected menu visible", mutate: func(value *Projection) {
			value.Menus[2].Hidden = false
		}},
		{name: "orphan protected menu", mutate: func(value *Projection) {
			value.Menus = append(value.Menus, MenuSeed{
				Name: "orphan", Path: "/business/example/permissions/orphan", Method: "GET",
				ParentPath: "/business/example", AccessType: adminpkg.ComponentAccessType,
				Permission: "example:orphan", Hidden: true,
			})
		}},
		{name: "route without exact API", mutate: func(value *Projection) {
			value.Routes[0].Method = "POST"
		}},
		{name: "duplicate permission", mutate: func(value *Projection) {
			value.Routes = append(value.Routes, value.Routes[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := projectionTestContract()
			test.mutate(&projection)
			if _, err := projection.cloneAndValidate(); err == nil {
				t.Fatal("invalid projection was accepted")
			}
		})
	}
}
