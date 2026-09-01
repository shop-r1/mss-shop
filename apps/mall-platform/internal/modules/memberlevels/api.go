package memberlevels

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const maximumBodyBytes = 64 << 10

type HTTPController struct {
	application Application
	authorizer  Authorizer
}

func RegisterRoutes(group *gin.RouterGroup, application Application, authorizer Authorizer) error {
	if group == nil || nilInterface(application) || nilInterface(authorizer) {
		return errors.New("member levels protected route dependencies are incomplete")
	}
	controller := &HTTPController{application: application, authorizer: authorizer}
	levels := group.Group("/member-levels")
	levels.GET("", controller.authorize(PermissionList, controller.List))
	levels.POST("", controller.authorize(PermissionCreate, controller.Create))
	levels.GET("/:id", controller.authorize(PermissionRead, controller.Get))
	levels.PUT("/:id", controller.authorize(PermissionUpdate, controller.Update))
	levels.PUT("/:id/default", controller.authorize(PermissionSetDefault, controller.SetDefault))
	levels.DELETE("/:id", controller.authorize(PermissionDelete, controller.Delete))
	return nil
}

func (controller *HTTPController) authorize(permission string, next gin.HandlerFunc) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if err := controller.authorizer.Authorize(ctx, permission); err != nil {
			writeAuthorizationError(ctx, err)
			return
		}
		next(ctx)
	}
}

func (controller *HTTPController) List(ctx *gin.Context) {
	options, err := parseListOptions(ctx.Request.URL.Query())
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	page, err := controller.application.List(ctx.Request.Context(), options)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, page)
}

func (controller *HTTPController) Get(ctx *gin.Context) {
	id := ctx.Param("id")
	if err := validateLegacyID(id); err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	level, err := controller.application.Get(ctx.Request.Context(), id)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, level)
}

func (controller *HTTPController) Create(ctx *gin.Context) {
	input, err := decodeBody[CreateMemberLevelInput](ctx)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	level, err := controller.application.Create(ctx.Request.Context(), input)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, level)
}

func (controller *HTTPController) Update(ctx *gin.Context) {
	id := ctx.Param("id")
	if err := validateLegacyID(id); err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	input, err := decodeBody[UpdateMemberLevelInput](ctx)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	level, err := controller.application.Update(ctx.Request.Context(), id, input)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, level)
}

func (controller *HTTPController) SetDefault(ctx *gin.Context) {
	id := ctx.Param("id")
	if err := validateLegacyID(id); err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	input, err := decodeBody[RevisionInput](ctx)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	level, err := controller.application.SetDefault(ctx.Request.Context(), id, input)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, level)
}

func (controller *HTTPController) Delete(ctx *gin.Context) {
	id := ctx.Param("id")
	if err := validateLegacyID(id); err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	input, err := decodeBody[RevisionInput](ctx)
	if err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	if err := controller.application.Delete(ctx.Request.Context(), id, input); err != nil {
		writeMemberLevelError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func decodeBody[T any](ctx *gin.Context) (T, error) {
	var input T
	if ctx == nil || ctx.Request == nil || ctx.Request.Body == nil {
		return input, &FieldError{Kind: ErrInvalidRequest, Field: "body", Rule: "object"}
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, &FieldError{Kind: ErrInvalidRequest, Field: "body", Rule: "json"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return input, &FieldError{Kind: ErrInvalidRequest, Field: "body", Rule: "single"}
	}
	return input, nil
}

func parseListOptions(values url.Values) (ListOptions, error) {
	allowed := map[string]struct{}{
		"current": {}, "pageSize": {}, "q": {}, "status": {}, "isDefault": {},
		"sortBy": {}, "sortOrder": {},
	}
	for key, entries := range values {
		if _, exists := allowed[key]; !exists || len(entries) != 1 {
			return ListOptions{}, &FieldError{Kind: ErrInvalidRequest, Field: key, Rule: "query"}
		}
	}
	options := ListOptions{Current: 1, PageSize: defaultPageSize, SortBy: "updatedAt", SortOrder: "desc"}
	var err error
	if raw := values.Get("current"); raw != "" {
		options.Current, err = strconv.Atoi(raw)
		if err != nil || options.Current < 1 {
			return ListOptions{}, &FieldError{Kind: ErrValidation, Field: "current", Rule: "positiveInteger"}
		}
	}
	if raw := values.Get("pageSize"); raw != "" {
		options.PageSize, err = strconv.Atoi(raw)
		if err != nil || options.PageSize < 1 || options.PageSize > maximumPageSize {
			return ListOptions{}, &FieldError{Kind: ErrValidation, Field: "pageSize", Rule: "range"}
		}
	}
	if raw := values.Get("q"); raw != "" {
		options.Query = strings.TrimSpace(raw)
		if !utf8.ValidString(options.Query) || len(options.Query) > maximumNameBytes {
			return ListOptions{}, &FieldError{Kind: ErrValidation, Field: "q", Rule: "maxBytes"}
		}
	}
	if raw := values.Get("status"); raw != "" {
		status := Status(raw)
		legacy, statusErr := requiredStatus(&status)
		if statusErr != nil {
			return ListOptions{}, statusErr
		}
		options.Status = &legacy
	}
	if raw := values.Get("isDefault"); raw != "" {
		parsed, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return ListOptions{}, &FieldError{Kind: ErrValidation, Field: "isDefault", Rule: "boolean"}
		}
		options.IsDefault = &parsed
	}
	if raw := values.Get("sortBy"); raw != "" {
		switch raw {
		case "name", "discountPercent", "updatedAt":
			options.SortBy = raw
		default:
			return ListOptions{}, &FieldError{Kind: ErrValidation, Field: "sortBy", Rule: "enum"}
		}
	}
	if raw := values.Get("sortOrder"); raw != "" {
		if raw != "asc" && raw != "desc" {
			return ListOptions{}, &FieldError{Kind: ErrValidation, Field: "sortOrder", Rule: "enum"}
		}
		options.SortOrder = raw
	}
	if _, err := paginationOffset(options.Current, options.PageSize); err != nil {
		return ListOptions{}, err
	}
	return options, nil
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
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorEnvelope{
			ErrorCode: "UNAUTHENTICATED", ErrorMessage: "Authentication is required",
			MessageKey: "memberLevels.errors.authenticationRequired",
		})
	case errors.Is(err, ErrAuthorizationDenied):
		ctx.AbortWithStatusJSON(http.StatusForbidden, errorEnvelope{
			ErrorCode: "FORBIDDEN", ErrorMessage: "Permission is denied",
			MessageKey: "memberLevels.errors.forbidden",
		})
	default:
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, errorEnvelope{
			ErrorCode: "AUTHORIZATION_UNAVAILABLE", ErrorMessage: "Authorization is temporarily unavailable",
			MessageKey: "memberLevels.errors.authorizationUnavailable",
		})
	}
}

func writeMemberLevelError(ctx *gin.Context, err error) {
	response := errorEnvelope{}
	status := http.StatusInternalServerError
	var fieldError *FieldError
	if errors.As(err, &fieldError) {
		response.Params = map[string]any{"field": fieldError.Field, "rule": fieldError.Rule}
	}
	var referenceError *ReferenceError
	if errors.As(err, &referenceError) {
		response.Params = map[string]any{
			"count":           referenceError.Counts.total(),
			"members":         referenceError.Counts.Members,
			"activities":      referenceError.Counts.Activities,
			"couponTemplates": referenceError.Counts.CouponTemplates,
			"goodsPrices":     referenceError.Counts.GoodsPrices,
		}
	}
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status, response.ErrorCode = http.StatusBadRequest, "INVALID_REQUEST"
		response.ErrorMessage, response.MessageKey = "Request is invalid", "memberLevels.errors.invalidRequest"
	case errors.Is(err, ErrValidation):
		status, response.ErrorCode = http.StatusUnprocessableEntity, "VALIDATION_FAILED"
		response.ErrorMessage, response.MessageKey = "Input validation failed", "memberLevels.errors.validationFailed"
	case errors.Is(err, ErrNotFound):
		status, response.ErrorCode = http.StatusNotFound, "MEMBER_LEVEL_NOT_FOUND"
		response.ErrorMessage, response.MessageKey = "Member level was not found", "memberLevels.errors.notFound"
	case errors.Is(err, ErrDuplicateName):
		status, response.ErrorCode = http.StatusConflict, "MEMBER_LEVEL_NAME_EXISTS"
		response.ErrorMessage, response.MessageKey = "Member level name already exists", "memberLevels.errors.duplicateName"
	case errors.Is(err, ErrRevisionConflict):
		status, response.ErrorCode = http.StatusConflict, "MEMBER_LEVEL_REVISION_CONFLICT"
		response.ErrorMessage, response.MessageKey = "Member level changed; refresh and retry", "memberLevels.errors.revisionConflict"
	case errors.Is(err, ErrPaymentPolicySource):
		status, response.ErrorCode = http.StatusConflict, "PAYMENT_POLICY_SOURCE_REQUIRED"
		response.ErrorMessage, response.MessageKey = "A safe payment policy source is required", "memberLevels.errors.paymentPolicySource"
	case errors.Is(err, ErrDefaultRequired):
		status, response.ErrorCode = http.StatusConflict, "DEFAULT_MEMBER_LEVEL_REQUIRED"
		response.ErrorMessage, response.MessageKey = "The default member level must remain enabled and active", "memberLevels.errors.defaultRequired"
	case errors.Is(err, ErrDefaultRepairRequired):
		status, response.ErrorCode = http.StatusConflict, "DEFAULT_MEMBER_LEVEL_REPAIR_REQUIRED"
		response.ErrorMessage, response.MessageKey = "Repair an invalid default through the set-default command", "memberLevels.errors.defaultRepairRequired"
	case errors.Is(err, ErrInUse):
		status, response.ErrorCode = http.StatusConflict, "MEMBER_LEVEL_IN_USE"
		response.ErrorMessage, response.MessageKey = "Member level is referenced by active business data", "memberLevels.errors.inUse"
	case errors.Is(err, ErrConflict):
		status, response.ErrorCode = http.StatusConflict, "MEMBER_LEVEL_CONFLICT"
		response.ErrorMessage, response.MessageKey = "Member level state conflicts with this operation", "memberLevels.errors.conflict"
	case errors.Is(err, ErrLegacyData):
		status, response.ErrorCode = http.StatusConflict, "LEGACY_MEMBER_LEVEL_INCOMPATIBLE"
		response.ErrorMessage, response.MessageKey = "Legacy member-level data is incompatible", "memberLevels.errors.legacyDataIncompatible"
	case errors.Is(err, ErrSchemaNotReady):
		status, response.ErrorCode = http.StatusServiceUnavailable, "LEGACY_SCHEMA_NOT_READY"
		response.ErrorMessage, response.MessageKey = "Legacy member-level schema is not ready", "memberLevels.errors.schemaNotReady"
	case errors.Is(err, ErrMutationDisabled):
		status, response.ErrorCode = http.StatusServiceUnavailable, "MEMBER_LEVEL_MUTATION_DISABLED"
		response.ErrorMessage, response.MessageKey = "Member-level mutations are disabled until isolated cutover", "memberLevels.errors.mutationDisabled"
	default:
		status, response.ErrorCode = http.StatusInternalServerError, "INTERNAL_ERROR"
		response.ErrorMessage, response.MessageKey = "Member-level operation failed", "memberLevels.errors.internal"
	}
	ctx.AbortWithStatusJSON(status, response)
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
