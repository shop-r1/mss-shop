package legacydb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	defaultPageSize = 20
	maximumPageSize = 100
	maximumSearch   = 200
	maximumIDLength = 256
)

type Query struct {
	Page      int
	PageSize  int
	Search    string
	SortBy    string
	SortOrder string
	Exact     map[string]string
	Contains  map[string]string
	IContains map[string]string
}

type Page struct {
	Data     []map[string]any `json:"data"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"pageSize"`
	Resource Resource         `json:"resource"`
}

// Repository is deliberately generic only inside the compiled eight-table
// allow-list. It never discovers tables and never accepts a schema name.
type Repository struct {
	db       *gorm.DB
	binding  fixedbinding.Binding
	registry Registry
}

func NewRepository(db *gorm.DB, binding fixedbinding.Binding, registry Registry) (*Repository, error) {
	if db == nil {
		return nil, errors.New("shared catalogue database is required")
	}
	if err := binding.Validate(); err != nil {
		return nil, fmt.Errorf("shared catalogue fixed binding: %w", err)
	}
	if len(registry.All()) != ExpectedSharedResourceCount {
		return nil, fmt.Errorf("shared catalogue registry must contain %d reviewed resources", ExpectedSharedResourceCount)
	}
	// Legacy rows can contain credentials and historical customer data. Keep
	// query parameters out of application logs even when GORM reports a slow or
	// failed statement. The scoped session does not change the MSS host logger.
	legacySession := db.Session(&gorm.Session{Logger: logger.Discard})
	return &Repository{db: legacySession, binding: binding, registry: registry}, nil
}

func (repository *Repository) Resource(name string) (Resource, error) {
	definition, ok := repository.registry.Lookup(name)
	if !ok {
		return Resource{}, ErrResourceNotFound
	}
	return definition.Resource, nil
}

func (repository *Repository) List(ctx context.Context, name string, query Query) (Page, error) {
	if ctx == nil {
		return Page{}, invalidField("context", "required")
	}
	definition, ok := repository.registry.Lookup(name)
	if !ok {
		return Page{}, ErrResourceNotFound
	}
	normalized, err := normalizeQuery(definition, query)
	if err != nil {
		return Page{}, err
	}

	database := repository.scopedQuery(ctx, definition)
	database, err = repository.applyQuery(database, definition, normalized)
	if err != nil {
		return Page{}, err
	}
	var total int64
	if err := database.Count(&total).Error; err != nil {
		return Page{}, classifyDatabaseError(err)
	}

	ordered := database.Select(selectProjection(definition)).
		Order(qualifiedColumn("legacy", normalized.SortBy) + " " + strings.ToUpper(normalized.SortOrder)).
		Limit(normalized.PageSize).
		Offset((normalized.Page - 1) * normalized.PageSize)
	var records []map[string]any
	if err := ordered.Find(&records).Error; err != nil {
		return Page{}, classifyDatabaseError(err)
	}
	for index := range records {
		records[index] = sanitizeRecord(definition, records[index])
	}
	if records == nil {
		records = make([]map[string]any, 0)
	}
	return Page{
		Data:     records,
		Total:    total,
		Page:     normalized.Page,
		PageSize: normalized.PageSize,
		Resource: definition.Resource,
	}, nil
}

func (repository *Repository) Get(ctx context.Context, name, id string) (map[string]any, Resource, error) {
	if ctx == nil {
		return nil, Resource{}, invalidField("context", "required")
	}
	definition, ok := repository.registry.Lookup(name)
	if !ok {
		return nil, Resource{}, ErrResourceNotFound
	}
	if err := validateID(id); err != nil {
		return nil, definition.Resource, err
	}
	record, err := repository.getDefinition(ctx, definition, id)
	return record, definition.Resource, err
}

func (repository *Repository) Create(ctx context.Context, name string, input map[string]any) (map[string]any, Resource, error) {
	_ = ctx
	_ = input
	definition, ok := repository.registry.Lookup(name)
	if !ok {
		return nil, Resource{}, ErrResourceNotFound
	}
	return nil, definition.Resource, ErrOperationNotSupported
}

func (repository *Repository) Update(ctx context.Context, name, id string, input map[string]any) (map[string]any, Resource, error) {
	_ = ctx
	_ = id
	_ = input
	definition, ok := repository.registry.Lookup(name)
	if !ok {
		return nil, Resource{}, ErrResourceNotFound
	}
	return nil, definition.Resource, ErrOperationNotSupported
}

func (repository *Repository) Delete(ctx context.Context, name, id string) (Resource, error) {
	_ = ctx
	_ = id
	definition, ok := repository.registry.Lookup(name)
	if !ok {
		return Resource{}, ErrResourceNotFound
	}
	return definition.Resource, ErrOperationNotSupported
}

func (repository *Repository) getDefinition(ctx context.Context, definition Definition, id string) (map[string]any, error) {
	database := repository.scopedQuery(ctx, definition).
		Select(selectProjection(definition)).
		Where(qualifiedColumn("legacy", definition.Resource.PrimaryKey[0])+" = ?", id).
		Limit(1)
	var records []map[string]any
	if err := database.Find(&records).Error; err != nil {
		return nil, classifyDatabaseError(err)
	}
	if len(records) == 0 {
		return nil, ErrRecordNotFound
	}
	return sanitizeRecord(definition, records[0]), nil
}

func (repository *Repository) scopedQuery(ctx context.Context, definition Definition) *gorm.DB {
	return repository.scopedQueryWithDB(repository.db.WithContext(ctx), definition)
}

func (repository *Repository) scopedQueryWithDB(database *gorm.DB, definition Definition) *gorm.DB {
	database = database.Table(qualifiedTable(repository.binding.SharedSchema, definition.Resource.Name) + " AS " + quoteIdentifier("legacy"))
	if definition.SoftDelete {
		database = database.Where(qualifiedColumn("legacy", "deleted_at") + " IS NULL")
	}
	return database
}

func (repository *Repository) applyQuery(database *gorm.DB, definition Definition, query Query) (*gorm.DB, error) {
	columnIndex := definitionColumnIndex(definition)
	if query.Search != "" {
		clauses := make([]string, 0, len(definition.Resource.Columns))
		arguments := make([]any, 0, len(definition.Resource.Columns))
		for _, column := range definition.Resource.Columns {
			if column.Secret || (column.Type != ColumnString && column.Type != ColumnJSON) {
				continue
			}
			clauses = append(clauses, "LOWER(CAST("+qualifiedColumn("legacy", column.Name)+" AS TEXT)) LIKE ? ESCAPE '!'")
			arguments = append(arguments, "%"+escapeLike(strings.ToLower(query.Search))+"%")
		}
		if len(clauses) > 0 {
			database = database.Where("("+strings.Join(clauses, " OR ")+")", arguments...)
		}
	}
	for _, filter := range sortedFilters(query.Exact) {
		column, err := filterableColumn(columnIndex, filter.name)
		if err != nil {
			return nil, err
		}
		database = database.Where(qualifiedColumn("legacy", column.Name)+" = ?", filter.value)
	}
	for _, filter := range sortedFilters(query.Contains) {
		column, err := filterableColumn(columnIndex, filter.name)
		if err != nil {
			return nil, err
		}
		function := "STRPOS"
		if repository.db.Dialector.Name() == "sqlite" {
			function = "INSTR"
		}
		database = database.Where(function+"(CAST("+qualifiedColumn("legacy", column.Name)+" AS TEXT), ?) > 0", filter.value)
	}
	for _, filter := range sortedFilters(query.IContains) {
		column, err := filterableColumn(columnIndex, filter.name)
		if err != nil {
			return nil, err
		}
		database = database.Where(
			"LOWER(CAST("+qualifiedColumn("legacy", column.Name)+" AS TEXT)) LIKE ? ESCAPE '!'",
			"%"+escapeLike(strings.ToLower(filter.value))+"%",
		)
	}
	return database, nil
}

func normalizeQuery(definition Definition, query Query) (Query, error) {
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = defaultPageSize
	}
	if query.Page < 1 {
		return Query{}, invalidField("page", "minimum")
	}
	if query.PageSize < 1 || query.PageSize > maximumPageSize {
		return Query{}, invalidField("pageSize", "range")
	}
	if len(query.Search) > maximumSearch {
		return Query{}, invalidField("q", "maximum")
	}
	if query.SortBy == "" {
		query.SortBy = definition.Resource.PrimaryKey[0]
	}
	columnIndex := definitionColumnIndex(definition)
	sortColumn, ok := columnIndex[query.SortBy]
	if !ok || sortColumn.Secret {
		return Query{}, invalidField("sortBy", "unsupported")
	}
	if query.SortOrder == "" {
		query.SortOrder = "asc"
	}
	query.SortOrder = strings.ToLower(query.SortOrder)
	if query.SortOrder != "asc" && query.SortOrder != "desc" {
		return Query{}, invalidField("sortOrder", "oneof")
	}
	if len(query.Exact)+len(query.Contains)+len(query.IContains) > 100 {
		return Query{}, invalidField("filters", "maximum")
	}
	return query, nil
}

type namedFilter struct {
	name  string
	value string
}

func sortedFilters(filters map[string]string) []namedFilter {
	result := make([]namedFilter, 0, len(filters))
	for name, value := range filters {
		result = append(result, namedFilter{name: name, value: value})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].name < result[right].name })
	return result
}

func filterableColumn(index map[string]Column, name string) (Column, error) {
	column, ok := index[name]
	if !ok || column.Secret || name == "deleted_at" {
		return Column{}, invalidField("filter."+name, "unsupported")
	}
	return column, nil
}

func validateID(id string) error {
	if id == "" || len(id) > maximumIDLength {
		return invalidField("id", "invalid")
	}
	return nil
}

func selectProjection(definition Definition) string {
	selected := make([]string, 0, len(definition.Resource.Columns))
	for _, column := range definition.Resource.Columns {
		if column.Secret {
			continue
		}
		selected = append(selected, qualifiedColumn("legacy", column.Name)+" AS "+quoteIdentifier(column.Name))
	}
	return strings.Join(selected, ", ")
}

func sanitizeRecord(definition Definition, record map[string]any) map[string]any {
	result := make(map[string]any, len(definition.Resource.Columns))
	for _, column := range definition.Resource.Columns {
		if column.Secret {
			result[column.Name] = nil
			continue
		}
		value := normalizeDatabaseValue(column, record[column.Name])
		if column.Type == ColumnJSON {
			value = redactNestedSecrets(value, definition.NestedSecrets)
		}
		result[column.Name] = value
	}
	return result
}

func normalizeDatabaseValue(column Column, value any) any {
	if value == nil {
		return nil
	}
	if column.Type == ColumnJSON {
		var encoded []byte
		switch typed := value.(type) {
		case []byte:
			encoded = typed
		case string:
			encoded = []byte(typed)
		}
		if len(encoded) != 0 {
			var decoded any
			if json.Unmarshal(encoded, &decoded) == nil {
				return decoded
			}
		}
	}
	if encoded, ok := value.([]byte); ok {
		return string(encoded)
	}
	return value
}

func redactNestedSecrets(value any, explicit []string) any {
	explicitSet := make(map[string]struct{}, len(explicit))
	for _, key := range explicit {
		explicitSet[normalizeSecretKey(key)] = struct{}{}
	}
	var redact func(any) any
	redact = func(candidate any) any {
		switch typed := candidate.(type) {
		case map[string]any:
			result := make(map[string]any, len(typed))
			for key, item := range typed {
				normalized := normalizeSecretKey(key)
				_, explicitSecret := explicitSet[normalized]
				if explicitSecret || secretLikeKey(normalized) {
					result[key] = nil
				} else {
					result[key] = redact(item)
				}
			}
			return result
		case []any:
			result := make([]any, len(typed))
			for index := range typed {
				result[index] = redact(typed[index])
			}
			return result
		default:
			return candidate
		}
	}
	return redact(value)
}

func normalizeSecretKey(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(value)
}

func secretLikeKey(normalized string) bool {
	for _, marker := range []string{"secret", "password", "passwd", "token", "privatekey", "credential", "apikey", "accesskey"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func definitionColumnIndex(definition Definition) map[string]Column {
	result := make(map[string]Column, len(definition.Resource.Columns))
	for _, column := range definition.Resource.Columns {
		result[column.Name] = column
	}
	return result
}

func qualifiedTable(schema, table string) string {
	return quoteIdentifier(schema) + "." + quoteIdentifier(table)
}

func qualifiedColumn(alias, column string) string {
	return quoteIdentifier(alias) + "." + quoteIdentifier(column)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func escapeLike(value string) string {
	return strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`).Replace(value)
}

func classifyDatabaseError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "no such table"),
		strings.Contains(message, "does not exist"),
		strings.Contains(message, "undefined table"),
		strings.Contains(message, "undefined column"):
		return fmt.Errorf("%w: shared catalogue relation unavailable", ErrSchemaNotReady)
	case strings.Contains(message, "unique constraint"),
		strings.Contains(message, "duplicate key"),
		strings.Contains(message, "unique failed"):
		return fmt.Errorf("%w: duplicate shared catalogue record", ErrConflict)
	default:
		return fmt.Errorf("%w: execute qualified shared catalogue query", ErrPersistence)
	}
}
