package legacydb

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidRequest        = errors.New("invalid legacy request")
	ErrValidation            = errors.New("legacy validation failed")
	ErrResourceNotFound      = errors.New("legacy resource not found")
	ErrRecordNotFound        = errors.New("legacy record not found")
	ErrConflict              = errors.New("legacy record conflict")
	ErrOperationNotSupported = errors.New("legacy operation not supported")
	ErrSchemaNotReady        = errors.New("legacy schema not ready")
	ErrPersistence           = errors.New("legacy persistence failed")
)

type FieldError struct {
	Kind  error
	Field string
	Rule  string
}

func (err *FieldError) Error() string {
	if err == nil {
		return "legacy field error"
	}
	return fmt.Sprintf("%v: field %q failed %q", err.Kind, err.Field, err.Rule)
}

func (err *FieldError) Unwrap() error {
	if err == nil || err.Kind == nil {
		return ErrInvalidRequest
	}
	return err.Kind
}

func invalidField(field, rule string) error {
	return &FieldError{Kind: ErrInvalidRequest, Field: field, Rule: rule}
}

func validationField(field, rule string) error {
	return &FieldError{Kind: ErrValidation, Field: field, Rule: rule}
}
