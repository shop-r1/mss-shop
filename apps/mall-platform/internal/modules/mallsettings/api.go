package mallsettings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

const maximumBodyBytes = 64 << 10

type Application interface {
	GetGeneral(context.Context) (GeneralSettings, error)
	PutGeneral(context.Context, UpdateGeneralSettingsInput) (GeneralSettings, error)
}

type HTTPController struct {
	application Application
	authorizer  Authorizer
}

func RegisterRoutes(group *gin.RouterGroup, application Application, authorizer Authorizer) error {
	if group == nil || nilInterface(application) || nilInterface(authorizer) {
		return errors.New("mall settings protected route dependencies are incomplete")
	}
	controller := &HTTPController{application: application, authorizer: authorizer}
	settings := group.Group("/mall-settings")
	settings.GET("/general", controller.authorize(PermissionRead, controller.GetGeneral))
	settings.PUT("/general", controller.authorize(PermissionUpdate, controller.PutGeneral))
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

func (controller *HTTPController) GetGeneral(ctx *gin.Context) {
	settings, err := controller.application.GetGeneral(ctx.Request.Context())
	if err != nil {
		writeMallSettingsError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, settings)
}

func (controller *HTTPController) PutGeneral(ctx *gin.Context) {
	input, err := decodeUpdateInput(ctx)
	if err != nil {
		writeMallSettingsError(ctx, err)
		return
	}
	settings, err := controller.application.PutGeneral(ctx.Request.Context(), input)
	if err != nil {
		writeMallSettingsError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, settings)
}

func decodeUpdateInput(ctx *gin.Context) (UpdateGeneralSettingsInput, error) {
	if ctx == nil || ctx.Request == nil || ctx.Request.Body == nil {
		return UpdateGeneralSettingsInput{}, &FieldError{Kind: ErrInvalidRequest, Field: "body", Rule: "object"}
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maximumBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	var input UpdateGeneralSettingsInput
	if err := decoder.Decode(&input); err != nil {
		return UpdateGeneralSettingsInput{}, &FieldError{Kind: ErrInvalidRequest, Field: "body", Rule: "json"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return UpdateGeneralSettingsInput{}, &FieldError{Kind: ErrInvalidRequest, Field: "body", Rule: "single"}
	}
	if _, err := input.settings(); err != nil {
		return UpdateGeneralSettingsInput{}, err
	}
	return input, nil
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
			MessageKey: "mallSettings.errors.authenticationRequired",
		})
	case errors.Is(err, ErrAuthorizationDenied):
		ctx.AbortWithStatusJSON(http.StatusForbidden, errorEnvelope{
			ErrorCode: "FORBIDDEN", ErrorMessage: "Permission is denied",
			MessageKey: "mallSettings.errors.forbidden",
		})
	default:
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, errorEnvelope{
			ErrorCode: "AUTHORIZATION_UNAVAILABLE", ErrorMessage: "Authorization is temporarily unavailable",
			MessageKey: "mallSettings.errors.authorizationUnavailable",
		})
	}
}

func writeMallSettingsError(ctx *gin.Context, err error) {
	response := errorEnvelope{}
	status := http.StatusInternalServerError
	var fieldError *FieldError
	if errors.As(err, &fieldError) {
		response.Params = map[string]any{"field": fieldError.Field, "rule": fieldError.Rule}
	}
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status, response.ErrorCode = http.StatusBadRequest, "INVALID_REQUEST"
		response.ErrorMessage, response.MessageKey = "Request is invalid", "mallSettings.errors.invalidRequest"
	case errors.Is(err, ErrValidation):
		status, response.ErrorCode = http.StatusUnprocessableEntity, "VALIDATION_FAILED"
		response.ErrorMessage, response.MessageKey = "Input validation failed", "mallSettings.errors.validationFailed"
	case errors.Is(err, ErrConflict):
		status, response.ErrorCode = http.StatusConflict, "CONFLICT"
		response.ErrorMessage, response.MessageKey = "Legacy settings rows are ambiguous", "mallSettings.errors.conflict"
	case errors.Is(err, ErrLegacyMetadata):
		status, response.ErrorCode = http.StatusConflict, "LEGACY_METADATA_INCOMPATIBLE"
		response.ErrorMessage, response.MessageKey = "Legacy settings metadata is incompatible", "mallSettings.errors.legacyMetadataIncompatible"
	case errors.Is(err, ErrSchemaNotReady):
		status, response.ErrorCode = http.StatusServiceUnavailable, "LEGACY_SCHEMA_NOT_READY"
		response.ErrorMessage, response.MessageKey = "Legacy settings schema is not ready", "mallSettings.errors.schemaNotReady"
	case errors.Is(err, ErrMutationDisabled):
		status, response.ErrorCode = http.StatusServiceUnavailable, "MALL_SETTINGS_WRITE_DISABLED"
		response.ErrorMessage, response.MessageKey = "Mall settings updates are disabled until a reviewed writable cutover", "mallSettings.errors.writeDisabled"
	default:
		status, response.ErrorCode = http.StatusInternalServerError, "INTERNAL_ERROR"
		response.ErrorMessage, response.MessageKey = "Mall settings operation failed", "mallSettings.errors.internal"
	}
	ctx.AbortWithStatusJSON(status, response)
}
