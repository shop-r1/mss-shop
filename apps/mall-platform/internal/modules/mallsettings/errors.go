package mallsettings

import (
	"errors"
	"fmt"
)

var (
	ErrAuthenticationRequired   = errors.New("mall settings authentication required")
	ErrAuthorizationDenied      = errors.New("mall settings authorization denied")
	ErrAuthorizationUnavailable = errors.New("mall settings authorization unavailable")
	ErrInvalidRequest           = errors.New("mall settings request is invalid")
	ErrValidation               = errors.New("mall settings validation failed")
	ErrConflict                 = errors.New("mall settings legacy row is ambiguous")
	ErrMutationDisabled         = errors.New("mall settings updates are disabled until a reviewed writable cutover")
	ErrSchemaNotReady           = errors.New("mall settings legacy schema is not ready")
	ErrPersistence              = errors.New("mall settings persistence failed")
	ErrLegacyMetadata           = errors.New("mall settings legacy metadata is incompatible")
)

type FieldError struct {
	Kind  error
	Field string
	Rule  string
}

func (err *FieldError) Error() string {
	if err == nil {
		return "mall settings field error"
	}
	return fmt.Sprintf("%v: field %q failed %q", err.Kind, err.Field, err.Rule)
}

func (err *FieldError) Unwrap() error {
	if err == nil || err.Kind == nil {
		return ErrInvalidRequest
	}
	return err.Kind
}
