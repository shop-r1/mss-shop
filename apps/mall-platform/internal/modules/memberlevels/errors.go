package memberlevels

import (
	"errors"
	"fmt"
)

var (
	ErrAuthenticationRequired   = errors.New("member levels authentication required")
	ErrAuthorizationDenied      = errors.New("member levels authorization denied")
	ErrAuthorizationUnavailable = errors.New("member levels authorization unavailable")
	ErrInvalidRequest           = errors.New("member levels request is invalid")
	ErrValidation               = errors.New("member levels validation failed")
	ErrNotFound                 = errors.New("member level not found")
	ErrDuplicateName            = errors.New("member level name already exists")
	ErrConflict                 = errors.New("member level state conflicts with the request")
	ErrRevisionConflict         = errors.New("member level revision changed")
	ErrPaymentPolicySource      = errors.New("member level payment policy source is unavailable")
	ErrDefaultRequired          = errors.New("default member level must remain enabled and active")
	ErrDefaultRepairRequired    = errors.New("invalid default member level requires the set-default command")
	ErrInUse                    = errors.New("member level is referenced")
	ErrMutationDisabled         = errors.New("member level mutations are disabled until isolated cutover")
	ErrSchemaNotReady           = errors.New("member levels legacy schema is not ready")
	ErrLegacyData               = errors.New("member levels legacy data is incompatible")
	ErrPersistence              = errors.New("member levels persistence failed")
)

type FieldError struct {
	Kind  error
	Field string
	Rule  string
}

func (err *FieldError) Error() string {
	if err == nil {
		return "member levels field error"
	}
	return fmt.Sprintf("%v: field %q failed %q", err.Kind, err.Field, err.Rule)
}

func (err *FieldError) Unwrap() error {
	if err == nil || err.Kind == nil {
		return ErrInvalidRequest
	}
	return err.Kind
}

type ReferenceError struct {
	Counts ReferenceCounts
}

func (err *ReferenceError) Error() string { return ErrInUse.Error() }
func (err *ReferenceError) Unwrap() error { return ErrInUse }
