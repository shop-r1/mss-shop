package legacycompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/legacydb"
)

const maximumBodyBytes = 1 << 20

type LegacyRepository interface {
	List(context.Context, string, legacydb.Query) (legacydb.Page, error)
	Get(context.Context, string, string) (map[string]any, legacydb.Resource, error)
	Create(context.Context, string, map[string]any) (map[string]any, legacydb.Resource, error)
	Update(context.Context, string, string, map[string]any) (map[string]any, legacydb.Resource, error)
	Delete(context.Context, string, string) (legacydb.Resource, error)
}

type RepositoryResolver interface {
	Resolve(context.Context) (LegacyRepository, error)
}

type requestRepositoryResolver struct {
	database business.RequestDatabase
	binding  fixedbinding.Binding
	registry legacydb.Registry
}

func (resolver requestRepositoryResolver) Resolve(ctx context.Context) (LegacyRepository, error) {
	if resolver.database == nil {
		return nil, fmt.Errorf("%w: request database resolver is unavailable", legacydb.ErrSchemaNotReady)
	}
	db, available := resolver.database(ctx)
	if !available || db == nil {
		return nil, fmt.Errorf("%w: request database is unavailable", legacydb.ErrSchemaNotReady)
	}
	repository, err := legacydb.NewRepository(db, resolver.binding, resolver.registry)
	if err != nil {
		return nil, fmt.Errorf("%w: compose request repository", legacydb.ErrSchemaNotReady)
	}
	return repository, nil
}

type HTTPController struct {
	resolver   RepositoryResolver
	authorizer Authorizer
	registry   legacydb.Registry
}

func RegisterRoutes(group *gin.RouterGroup, resolver RepositoryResolver, authorizer Authorizer, registry legacydb.Registry) error {
	if group == nil {
		return errors.New("legacy protected route group is required")
	}
	if nilInterface(resolver) {
		return errors.New("legacy repository resolver is required")
	}
	if nilInterface(authorizer) {
		return errors.New("legacy authorizer is required")
	}
	if len(registry.All()) != legacydb.ExpectedMallResourceCount {
		return errors.New("legacy route registry is incomplete")
	}
	controller := &HTTPController{resolver: resolver, authorizer: authorizer, registry: registry}
	resources := group.Group("/legacy/resources")
	resources.GET("/:resource", controller.authorize(OperationList, controller.List))
	resources.GET("/:resource/:id", controller.authorize(OperationRead, controller.Get))
	resources.POST("/:resource", controller.authorize(OperationCreate, controller.Create))
	resources.PUT("/:resource/:id", controller.authorize(OperationUpdate, controller.Update))
	resources.DELETE("/:resource/:id", controller.authorize(OperationDelete, controller.Delete))
	return nil
}

func (controller *HTTPController) authorize(operation Operation, next gin.HandlerFunc) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		resource := ctx.Param("resource")
		definition, ok := controller.registry.Lookup(resource)
		if !ok || validateResourceName(resource) != nil {
			writeLegacyError(ctx, legacydb.ErrResourceNotFound)
			return
		}
		if !operationAllowed(definition, operation) {
			writeLegacyError(ctx, legacydb.ErrOperationNotSupported)
			return
		}
		if err := controller.authorizer.Authorize(ctx, resource, operation); err != nil {
			writeAuthorizationError(ctx, err)
			return
		}
		next(ctx)
	}
}

func (controller *HTTPController) List(ctx *gin.Context) {
	query, err := parseLegacyQuery(ctx)
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	repository, err := controller.resolver.Resolve(ctx.Request.Context())
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	page, err := repository.List(ctx.Request.Context(), ctx.Param("resource"), query)
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, listResponse{
		Data: page.Data, Total: page.Total, Page: page.Page, PageSize: page.PageSize, Resource: page.Resource,
	})
}

func (controller *HTTPController) Get(ctx *gin.Context) {
	repository, err := controller.resolver.Resolve(ctx.Request.Context())
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	record, resource, err := repository.Get(ctx.Request.Context(), ctx.Param("resource"), ctx.Param("id"))
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, detailResponse{Data: record, Resource: resource})
}

func (controller *HTTPController) Create(ctx *gin.Context) {
	input, err := decodeLegacyBody(ctx)
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	repository, err := controller.resolver.Resolve(ctx.Request.Context())
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	record, resource, err := repository.Create(ctx.Request.Context(), ctx.Param("resource"), input)
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, detailResponse{Data: record, Resource: resource})
}

func (controller *HTTPController) Update(ctx *gin.Context) {
	input, err := decodeLegacyBody(ctx)
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	repository, err := controller.resolver.Resolve(ctx.Request.Context())
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	record, resource, err := repository.Update(ctx.Request.Context(), ctx.Param("resource"), ctx.Param("id"), input)
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, detailResponse{Data: record, Resource: resource})
}

func (controller *HTTPController) Delete(ctx *gin.Context) {
	repository, err := controller.resolver.Resolve(ctx.Request.Context())
	if err != nil {
		writeLegacyError(ctx, err)
		return
	}
	if _, err := repository.Delete(ctx.Request.Context(), ctx.Param("resource"), ctx.Param("id")); err != nil {
		writeLegacyError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

type listResponse struct {
	Data     []map[string]any  `json:"data"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
	Resource legacydb.Resource `json:"resource"`
}

type detailResponse struct {
	Data     map[string]any    `json:"data"`
	Resource legacydb.Resource `json:"resource"`
}

type errorEnvelope struct {
	ErrorCode    string         `json:"errorCode"`
	ErrorMessage string         `json:"errorMessage"`
	MessageKey   string         `json:"messageKey"`
	Params       map[string]any `json:"params,omitempty"`
}

func parseLegacyQuery(ctx *gin.Context) (legacydb.Query, error) {
	values := ctx.Request.URL.Query()
	query := legacydb.Query{
		Search:    values.Get("q"),
		SortBy:    values.Get("sortBy"),
		SortOrder: values.Get("sortOrder"),
		Exact:     make(map[string]string),
		Contains:  make(map[string]string),
		IContains: make(map[string]string),
	}
	var err error
	if raw := values.Get("page"); raw != "" {
		query.Page, err = strconv.Atoi(raw)
		if err != nil {
			return legacydb.Query{}, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "page", Rule: "integer"}
		}
	}
	if raw := values.Get("pageSize"); raw != "" {
		query.PageSize, err = strconv.Atoi(raw)
		if err != nil {
			return legacydb.Query{}, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "pageSize", Rule: "integer"}
		}
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return legacydb.Query{}, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: key, Rule: "single"}
		}
		switch key {
		case "page", "pageSize", "q", "sortBy", "sortOrder":
			continue
		}
		mode, field, ok := bracketFilter(key)
		if !ok {
			return legacydb.Query{}, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: key, Rule: "unsupported"}
		}
		switch mode {
		case "exact":
			query.Exact[field] = entries[0]
		case "contains":
			query.Contains[field] = entries[0]
		case "icontains":
			query.IContains[field] = entries[0]
		}
	}
	return query, nil
}

func bracketFilter(key string) (mode, field string, ok bool) {
	opening := strings.IndexByte(key, '[')
	if opening <= 0 || !strings.HasSuffix(key, "]") || strings.Count(key, "[") != 1 || strings.Count(key, "]") != 1 {
		return "", "", false
	}
	mode, field = key[:opening], key[opening+1:len(key)-1]
	if field == "" || (mode != "exact" && mode != "contains" && mode != "icontains") {
		return "", "", false
	}
	return mode, field, true
}

func decodeLegacyBody(ctx *gin.Context) (map[string]any, error) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.UseNumber()
	var input map[string]any
	if err := decoder.Decode(&input); err != nil {
		return nil, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "body", Rule: "json"}
	}
	if input == nil {
		return nil, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "body", Rule: "object"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "body", Rule: "single"}
	}
	return input, nil
}

func writeAuthorizationError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrAuthenticationRequired):
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorEnvelope{ErrorCode: "UNAUTHENTICATED", ErrorMessage: "Authentication is required", MessageKey: "legacy.errors.authenticationRequired"})
	case errors.Is(err, ErrAuthorizationDenied):
		ctx.AbortWithStatusJSON(http.StatusForbidden, errorEnvelope{ErrorCode: "FORBIDDEN", ErrorMessage: "Permission is denied", MessageKey: "legacy.errors.forbidden"})
	default:
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, errorEnvelope{ErrorCode: "AUTHORIZATION_UNAVAILABLE", ErrorMessage: "Authorization is temporarily unavailable", MessageKey: "legacy.errors.authorizationUnavailable"})
	}
}

func writeLegacyError(ctx *gin.Context, err error) {
	response := errorEnvelope{}
	status := http.StatusInternalServerError
	var fieldError *legacydb.FieldError
	if errors.As(err, &fieldError) {
		response.Params = map[string]any{"field": fieldError.Field, "rule": fieldError.Rule}
	}
	switch {
	case errors.Is(err, legacydb.ErrResourceNotFound):
		status, response.ErrorCode, response.ErrorMessage, response.MessageKey = http.StatusNotFound, "RESOURCE_NOT_FOUND", "Legacy resource was not found", "legacy.errors.resourceNotFound"
	case errors.Is(err, legacydb.ErrRecordNotFound):
		status, response.ErrorCode, response.ErrorMessage, response.MessageKey = http.StatusNotFound, "RECORD_NOT_FOUND", "Legacy record was not found", "legacy.errors.recordNotFound"
	case errors.Is(err, legacydb.ErrConflict):
		status, response.ErrorCode, response.ErrorMessage, response.MessageKey = http.StatusConflict, "CONFLICT", "Legacy record conflicts with existing data", "legacy.errors.conflict"
	case errors.Is(err, legacydb.ErrValidation):
		status, response.ErrorCode, response.ErrorMessage, response.MessageKey = http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Legacy input validation failed", "legacy.errors.validationFailed"
	case errors.Is(err, legacydb.ErrSchemaNotReady):
		status, response.ErrorCode, response.ErrorMessage, response.MessageKey = http.StatusServiceUnavailable, "LEGACY_SCHEMA_NOT_READY", "Legacy schema is not ready", "legacy.errors.schemaNotReady"
	case errors.Is(err, legacydb.ErrOperationNotSupported):
		status, response.ErrorCode, response.ErrorMessage, response.MessageKey = http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED", "Legacy operation is not supported", "legacy.errors.operationNotSupported"
	case errors.Is(err, legacydb.ErrInvalidRequest):
		status, response.ErrorCode, response.ErrorMessage, response.MessageKey = http.StatusBadRequest, "INVALID_REQUEST", "Legacy request is invalid", "legacy.errors.invalidRequest"
	default:
		status, response.ErrorCode, response.ErrorMessage, response.MessageKey = http.StatusInternalServerError, "INTERNAL_ERROR", "Legacy operation failed", "legacy.errors.internal"
	}
	ctx.AbortWithStatusJSON(status, response)
}
