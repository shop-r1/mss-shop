// Package legacydb provides the schema-qualified access boundary for the eight
// source-global R1Shop catalogue tables owned by tenant-platform.
package legacydb

import (
	"sort"
	"strings"
)

const ExpectedSharedResourceCount = 8

type ColumnType string

const (
	ColumnString   ColumnType = "string"
	ColumnNumber   ColumnType = "number"
	ColumnBoolean  ColumnType = "boolean"
	ColumnDatetime ColumnType = "datetime"
	ColumnJSON     ColumnType = "json"
	ColumnSecret   ColumnType = "secret"
)

type Column struct {
	Name     string     `json:"name"`
	Label    string     `json:"label"`
	Type     ColumnType `json:"type"`
	Writable bool       `json:"writable"`
	Secret   bool       `json:"secret"`
	Required bool       `json:"required"`
}

type Capabilities struct {
	Detail bool `json:"detail"`
	Create bool `json:"create"`
	Update bool `json:"update"`
	Delete bool `json:"delete"`
}

type Resource struct {
	Name         string       `json:"name"`
	Domain       string       `json:"domain"`
	TitleKey     string       `json:"titleKey"`
	PrimaryKey   []string     `json:"primaryKey"`
	Columns      []Column     `json:"columns"`
	Capabilities Capabilities `json:"capabilities"`
}

type Definition struct {
	Resource      Resource
	SoftDelete    bool
	NestedSecrets []string
}

type Registry struct {
	definitions map[string]Definition
	names       []string
}

type resourceSeed struct {
	name       string
	domain     string
	columns    string
	required   string
	jsonFields string
	softDelete bool
}

// DefaultRegistry is a compiled allow-list, never database discovery. All
// eight shared resources are read-only until audited domain workflows own
// their legacy hooks, cache invalidation, and cross-tenant side effects.
func DefaultRegistry() Registry {
	seeds := []resourceSeed{
		{name: "brands", domain: "shared-catalog", softDelete: true, required: "name_zh name_en status", columns: "id created_at updated_at deleted_at name_zh name_en logo site_url index_img bg_img description sort status"},
		{name: "categories", domain: "shared-catalog", softDelete: true, required: "name", jsonFields: "pack_rule", columns: "id created_at updated_at deleted_at parent_id name alias description sort img tag pack_rule"},
		{name: "classes", domain: "shared-catalog", softDelete: true, required: "name", jsonFields: "attributes", columns: "id created_at updated_at deleted_at category_id name attributes status"},
		{name: "goods_infos", domain: "shared-catalog", softDelete: true, required: "name weight", jsonFields: "pack_rule", columns: "id created_at updated_at deleted_at category_id parent_category_id brand_id name album description image video keywords bar_code content weight has_pack_rule pack_rule unit goods_type"},
		{name: "couriers", domain: "shared-catalog", softDelete: true, required: "name region method", columns: "id created_at updated_at deleted_at name logo status site_url region method"},
		{name: "courier_pack_rules", domain: "shared-catalog", softDelete: true, required: "courier_id name", columns: "id created_at updated_at deleted_at courier_id name simple mixed mixed_sum price_unit price_total"},
		{name: "courier_links", domain: "shared-catalog", columns: "id link_id left_rule_id object_ids_data created_at"},
		{name: "payments", domain: "shared-catalog", softDelete: true, required: "name method status", columns: "id created_at updated_at deleted_at logo name method status site_url type description terminals"},
	}

	definitions := make(map[string]Definition, len(seeds))
	names := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		definition := definitionFromSeed(seed)
		definitions[seed.name] = definition
		names = append(names, seed.name)
	}
	sort.Strings(names)
	return Registry{definitions: definitions, names: names}
}

func definitionFromSeed(seed resourceSeed) Definition {
	required := stringSet(seed.required)
	jsonFields := stringSet(seed.jsonFields)
	capabilities := Capabilities{Detail: true}
	columns := make([]Column, 0, len(strings.Fields(seed.columns)))
	for _, name := range strings.Fields(seed.columns) {
		_, isRequired := required[name]
		columnType := inferColumnType(name)
		if _, isJSON := jsonFields[name]; isJSON {
			columnType = ColumnJSON
		}
		columns = append(columns, Column{
			Name:     name,
			Label:    "legacy.fields." + name,
			Type:     columnType,
			Writable: false,
			Required: isRequired,
		})
	}
	return Definition{
		Resource: Resource{
			Name:         seed.name,
			Domain:       seed.domain,
			TitleKey:     "legacy.resources." + seed.name,
			PrimaryKey:   []string{"id"},
			Columns:      columns,
			Capabilities: capabilities,
		},
		SoftDelete: seed.softDelete,
	}
}

func (registry Registry) Lookup(name string) (Definition, bool) {
	definition, ok := registry.definitions[name]
	if !ok {
		return Definition{}, false
	}
	return cloneDefinition(definition), true
}

func (registry Registry) All() []Definition {
	result := make([]Definition, 0, len(registry.names))
	for _, name := range registry.names {
		definition, _ := registry.Lookup(name)
		result = append(result, definition)
	}
	return result
}

func cloneDefinition(definition Definition) Definition {
	clone := definition
	clone.Resource.PrimaryKey = append([]string(nil), definition.Resource.PrimaryKey...)
	clone.Resource.Columns = append([]Column(nil), definition.Resource.Columns...)
	clone.NestedSecrets = append([]string(nil), definition.NestedSecrets...)
	return clone
}

func stringSet(value string) map[string]struct{} {
	result := make(map[string]struct{}, len(strings.Fields(value)))
	for _, item := range strings.Fields(value) {
		result[item] = struct{}{}
	}
	return result
}

func inferColumnType(name string) ColumnType {
	switch name {
	case "created_at", "updated_at", "deleted_at":
		return ColumnDatetime
	case "has_pack_rule":
		return ColumnBoolean
	case "sort", "status", "weight", "goods_type", "simple", "mixed", "mixed_sum", "price_unit", "price_total":
		return ColumnNumber
	default:
		return ColumnString
	}
}
