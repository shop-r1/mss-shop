package sharedcatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/fixedbinding"
	"github.com/shop-r1/mss-shop/apps/tenant-platform/internal/platform/legacydb"
)

const maximumRequestBody = 1 << 20

type requestDependencies struct {
	database  business.RequestDatabase
	principal business.PrincipalResolver
	binding   fixedbinding.Binding
	registry  legacydb.Registry
}

func RegisterRoutes(
	group *gin.RouterGroup,
	runtime business.Runtime,
	binding fixedbinding.Binding,
	registry legacydb.Registry,
) error {
	if group == nil {
		return errors.New("shared catalogue protected route group is required")
	}
	if runtime.RequestDatabase == nil {
		return errors.New("shared catalogue request database resolver is required")
	}
	if runtime.Principal == nil {
		return errors.New("shared catalogue principal resolver is required")
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("shared catalogue route binding: %w", err)
	}
	if len(registry.All()) != legacydb.ExpectedSharedResourceCount {
		return errors.New("shared catalogue route registry is incomplete")
	}
	controller := &controller{dependencies: requestDependencies{
		database:  runtime.RequestDatabase,
		principal: runtime.Principal,
		binding:   binding,
		registry:  registry,
	}}
	resource := group.Group("/legacy/resources/:resource")
	resource.GET("", controller.list)
	resource.POST("", controller.create)
	resource.GET("/:id", controller.detail)
	resource.PUT("/:id", controller.update)
	resource.DELETE("/:id", controller.delete)
	return nil
}

type controller struct{ dependencies requestDependencies }

type requestService struct {
	repository *legacydb.Repository
	authorizer *AdminAuthorizer
}

func (controller *controller) service(ctx *gin.Context) (*requestService, error) {
	if controller == nil || controller.dependencies.database == nil || ctx == nil || ctx.Request == nil {
		return nil, legacydb.ErrPersistence
	}
	db, ok := controller.dependencies.database(ctx.Request.Context())
	if !ok || db == nil {
		return nil, legacydb.ErrPersistence
	}
	repository, err := legacydb.NewRepository(db, controller.dependencies.binding, controller.dependencies.registry)
	if err != nil {
		return nil, legacydb.ErrPersistence
	}
	authorizer, err := NewAdminAuthorizer(
		db,
		controller.dependencies.binding,
		controller.dependencies.registry,
		controller.dependencies.principal,
	)
	if err != nil {
		return nil, ErrAuthorizationUnavailable
	}
	return &requestService{repository: repository, authorizer: authorizer}, nil
}

func (controller *controller) list(ctx *gin.Context) {
	service, ok := controller.authorizedService(ctx, actionRead)
	if !ok {
		return
	}
	query, err := decodeListQuery(ctx)
	if err != nil {
		writeError(ctx, err)
		return
	}
	page, err := service.repository.List(ctx.Request.Context(), ctx.Param("resource"), query)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, page)
}

func (controller *controller) detail(ctx *gin.Context) {
	service, ok := controller.authorizedService(ctx, actionRead)
	if !ok {
		return
	}
	record, resource, err := service.repository.Get(ctx.Request.Context(), ctx.Param("resource"), ctx.Param("id"))
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": record, "resource": resource})
}

func (controller *controller) create(ctx *gin.Context) {
	service, ok := controller.authorizedService(ctx, actionCreate)
	if !ok {
		return
	}
	payload, err := decodeObject(ctx)
	if err != nil {
		writeError(ctx, err)
		return
	}
	record, resource, err := service.repository.Create(ctx.Request.Context(), ctx.Param("resource"), payload)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{"data": record, "resource": resource})
}

func (controller *controller) update(ctx *gin.Context) {
	service, ok := controller.authorizedService(ctx, actionUpdate)
	if !ok {
		return
	}
	payload, err := decodeObject(ctx)
	if err != nil {
		writeError(ctx, err)
		return
	}
	record, resource, err := service.repository.Update(ctx.Request.Context(), ctx.Param("resource"), ctx.Param("id"), payload)
	if err != nil {
		writeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": record, "resource": resource})
}

func (controller *controller) delete(ctx *gin.Context) {
	service, ok := controller.authorizedService(ctx, actionDelete)
	if !ok {
		return
	}
	if _, err := service.repository.Delete(ctx.Request.Context(), ctx.Param("resource"), ctx.Param("id")); err != nil {
		writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (controller *controller) authorizedService(ctx *gin.Context, operation action) (*requestService, bool) {
	resource, exists := controller.dependencies.registry.Lookup(ctx.Param("resource"))
	if !exists {
		writeError(ctx, legacydb.ErrResourceNotFound)
		return nil, false
	}
	if !supports(resource.Resource.Capabilities, operation) {
		writeError(ctx, legacydb.ErrOperationNotSupported)
		return nil, false
	}
	service, err := controller.service(ctx)
	if err != nil {
		writeError(ctx, err)
		return nil, false
	}
	permission := PermissionCode(resource.Resource.Name, string(operation))
	if err := service.authorizer.Authorize(ctx, permission); err != nil {
		writeAuthorizationError(ctx, err)
		return nil, false
	}
	return service, true
}

func supports(capabilities legacydb.Capabilities, operation action) bool {
	switch operation {
	case actionRead:
		return capabilities.Detail
	case actionCreate, actionUpdate, actionDelete:
		return false
	default:
		return false
	}
}

func decodeListQuery(ctx *gin.Context) (legacydb.Query, error) {
	values := ctx.Request.URL.Query()
	query := legacydb.Query{
		Search:    values.Get("q"),
		SortBy:    values.Get("sortBy"),
		SortOrder: values.Get("sortOrder"),
		Exact:     make(map[string]string),
		Contains:  make(map[string]string),
		IContains: make(map[string]string),
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return legacydb.Query{}, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: key, Rule: "single"}
		}
		switch key {
		case "page", "pageSize", "q", "sortBy", "sortOrder":
		default:
			mode, field, ok := bracketFilter(key)
			if !ok {
				return legacydb.Query{}, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: key, Rule: "unknown"}
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
	}
	var err error
	if value := values.Get("page"); value != "" {
		query.Page, err = strconv.Atoi(value)
		if err != nil {
			return legacydb.Query{}, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "page", Rule: "integer"}
		}
	}
	if value := values.Get("pageSize"); value != "" {
		query.PageSize, err = strconv.Atoi(value)
		if err != nil {
			return legacydb.Query{}, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "pageSize", Rule: "integer"}
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

func decodeObject(ctx *gin.Context) (map[string]any, error) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumRequestBody)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "body", Rule: "invalid"}
	}
	if payload == nil {
		return nil, &legacydb.FieldError{Kind: legacydb.ErrValidation, Field: "body", Rule: "object"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, &legacydb.FieldError{Kind: legacydb.ErrInvalidRequest, Field: "body", Rule: "single"}
	}
	return payload, nil
}

type errorEnvelope struct {
	ErrorCode    string         `json:"errorCode"`
	ErrorMessage string         `json:"errorMessage"`
	MessageKey   string         `json:"messageKey"`
	Params       map[string]any `json:"params,omitempty"`
}

func writeAuthorizationError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrAuthenticationRequired):
		writeEnvelope(ctx, http.StatusUnauthorized, "UNAUTHENTICATED", "legacy.errors.authenticationRequired", nil)
	case errors.Is(err, ErrAuthorizationUnavailable):
		writeEnvelope(ctx, http.StatusServiceUnavailable, "AUTHORIZATION_UNAVAILABLE", "legacy.errors.authorizationUnavailable", nil)
	default:
		writeEnvelope(ctx, http.StatusForbidden, "FORBIDDEN", "legacy.errors.forbidden", nil)
	}
}

func writeError(ctx *gin.Context, err error) {
	params := map[string]any(nil)
	var fieldError *legacydb.FieldError
	if errors.As(err, &fieldError) {
		params = map[string]any{"field": fieldError.Field, "rule": fieldError.Rule}
	}
	switch {
	case errors.Is(err, legacydb.ErrInvalidRequest):
		writeEnvelope(ctx, http.StatusBadRequest, "INVALID_REQUEST", "legacy.errors.invalidRequest", params)
	case errors.Is(err, legacydb.ErrValidation):
		writeEnvelope(ctx, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "legacy.errors.validationFailed", params)
	case errors.Is(err, legacydb.ErrResourceNotFound):
		writeEnvelope(ctx, http.StatusNotFound, "RESOURCE_NOT_FOUND", "legacy.errors.resourceNotFound", nil)
	case errors.Is(err, legacydb.ErrRecordNotFound):
		writeEnvelope(ctx, http.StatusNotFound, "RECORD_NOT_FOUND", "legacy.errors.recordNotFound", nil)
	case errors.Is(err, legacydb.ErrConflict):
		writeEnvelope(ctx, http.StatusConflict, "CONFLICT", "legacy.errors.conflict", nil)
	case errors.Is(err, legacydb.ErrOperationNotSupported):
		writeEnvelope(ctx, http.StatusMethodNotAllowed, "OPERATION_NOT_SUPPORTED", "legacy.errors.operationNotSupported", nil)
	case errors.Is(err, legacydb.ErrSchemaNotReady), errors.Is(err, legacydb.ErrPersistence):
		writeEnvelope(ctx, http.StatusServiceUnavailable, "LEGACY_SCHEMA_NOT_READY", "legacy.errors.schemaNotReady", nil)
	default:
		writeEnvelope(ctx, http.StatusInternalServerError, "INTERNAL_ERROR", "legacy.errors.internal", nil)
	}
}

func writeEnvelope(ctx *gin.Context, status int, code, messageKey string, params map[string]any) {
	ctx.AbortWithStatusJSON(status, errorEnvelope{
		ErrorCode: code, ErrorMessage: fallbackErrorMessage(code), MessageKey: messageKey, Params: params,
	})
}

func fallbackErrorMessage(code string) string {
	switch code {
	case "UNAUTHENTICATED":
		return "Authentication is required."
	case "FORBIDDEN":
		return "You do not have permission to perform this operation."
	case "LEGACY_SCHEMA_NOT_READY":
		return "The legacy data service is not ready."
	case "AUTHORIZATION_UNAVAILABLE":
		return "Authorization is temporarily unavailable."
	case "INVALID_REQUEST":
		return "The request is invalid."
	case "VALIDATION_FAILED":
		return "The request data is invalid."
	case "RESOURCE_NOT_FOUND":
		return "The requested resource was not found."
	case "RECORD_NOT_FOUND":
		return "The requested record was not found."
	case "CONFLICT":
		return "The request conflicts with existing data."
	case "OPERATION_NOT_SUPPORTED":
		return "This operation is not supported for the resource."
	default:
		return "The request could not be completed."
	}
}
