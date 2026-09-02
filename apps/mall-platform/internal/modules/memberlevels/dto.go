package memberlevels

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	defaultPageSize  = 20
	maximumPageSize  = 100
	maximumNameBytes = 100
)

var legacyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,20}$`)

type Status string

const (
	StatusEnabled  Status = "enabled"
	StatusDisabled Status = "disabled"
	StatusUnknown  Status = "unknown"
)

type MemberLevel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DiscountPercent string `json:"discountPercent"`
	Status          Status `json:"status"`
	IsDefault       bool   `json:"isDefault"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	Revision        string `json:"revision"`
}

type Integrity struct {
	FlaggedDefaultCount int64 `json:"flaggedDefaultCount"`
	EnabledDefaultCount int64 `json:"enabledDefaultCount"`
	InvalidDefaultCount int64 `json:"invalidDefaultCount"`
}

// MutationAvailability is the server-side cutover gate projected to the page.
// It is combined with, but never replaces, the caller's MSS permissions.
type MutationAvailability struct {
	Create     bool `json:"create"`
	Update     bool `json:"update"`
	SetDefault bool `json:"setDefault"`
	Delete     bool `json:"delete"`
}

type MemberLevelPage struct {
	Data       []MemberLevel        `json:"data"`
	Total      int64                `json:"total"`
	Current    int                  `json:"current"`
	PageSize   int                  `json:"pageSize"`
	Integrity  Integrity            `json:"integrity"`
	Operations MutationAvailability `json:"operations"`
}

type CreateMemberLevelInput struct {
	Name                       *string `json:"name"`
	DiscountPercent            *string `json:"discountPercent"`
	Status                     *Status `json:"status"`
	PaymentPolicySourceLevelID *string `json:"paymentPolicySourceLevelId,omitempty"`
}

type UpdateMemberLevelInput struct {
	Name            *string `json:"name"`
	DiscountPercent *string `json:"discountPercent"`
	Status          *Status `json:"status"`
	Revision        *string `json:"revision"`
}

type RevisionInput struct {
	Revision *string `json:"revision"`
}

type createValues struct {
	Name                       string
	DiscountPercent            string
	Status                     int64
	PaymentPolicySourceLevelID string
}

type updateValues struct {
	Name            string
	DiscountPercent string
	Status          int64
	Revision        string
}

type ListOptions struct {
	Current   int
	PageSize  int
	Query     string
	Status    *int64
	IsDefault *bool
	SortBy    string
	SortOrder string
}

type ReferenceCounts struct {
	Members         int64 `json:"members"`
	Activities      int64 `json:"activities"`
	CouponTemplates int64 `json:"couponTemplates"`
	GoodsPrices     int64 `json:"goodsPrices"`
}

func (counts ReferenceCounts) total() int64 {
	return counts.Members + counts.Activities + counts.CouponTemplates + counts.GoodsPrices
}

func (input CreateMemberLevelInput) values() (createValues, error) {
	name, err := requiredName(input.Name)
	if err != nil {
		return createValues{}, err
	}
	discount, err := requiredDiscount(input.DiscountPercent)
	if err != nil {
		return createValues{}, err
	}
	status, err := requiredStatus(input.Status)
	if err != nil {
		return createValues{}, err
	}
	sourceID := ""
	if input.PaymentPolicySourceLevelID != nil {
		sourceID = strings.TrimSpace(*input.PaymentPolicySourceLevelID)
		if !legacyIDPattern.MatchString(sourceID) {
			return createValues{}, &FieldError{Kind: ErrValidation, Field: "paymentPolicySourceLevelId", Rule: "legacyId"}
		}
	}
	return createValues{Name: name, DiscountPercent: discount, Status: status, PaymentPolicySourceLevelID: sourceID}, nil
}

func (input UpdateMemberLevelInput) values() (updateValues, error) {
	name, err := requiredName(input.Name)
	if err != nil {
		return updateValues{}, err
	}
	discount, err := requiredDiscount(input.DiscountPercent)
	if err != nil {
		return updateValues{}, err
	}
	status, err := requiredStatus(input.Status)
	if err != nil {
		return updateValues{}, err
	}
	revision, err := requiredRevision(input.Revision)
	if err != nil {
		return updateValues{}, err
	}
	return updateValues{Name: name, DiscountPercent: discount, Status: status, Revision: revision}, nil
}

func (input RevisionInput) value() (string, error) { return requiredRevision(input.Revision) }

func requiredName(value *string) (string, error) {
	if value == nil {
		return "", &FieldError{Kind: ErrValidation, Field: "name", Rule: "required"}
	}
	name := strings.TrimSpace(*value)
	if name == "" {
		return "", &FieldError{Kind: ErrValidation, Field: "name", Rule: "required"}
	}
	if !utf8.ValidString(name) {
		return "", &FieldError{Kind: ErrValidation, Field: "name", Rule: "utf8"}
	}
	if len(name) > maximumNameBytes {
		return "", &FieldError{Kind: ErrValidation, Field: "name", Rule: "maxBytes"}
	}
	return name, nil
}

func requiredDiscount(value *string) (string, error) {
	if value == nil {
		return "", &FieldError{Kind: ErrValidation, Field: "discountPercent", Rule: "required"}
	}
	discount, ok := normalizeDiscount(*value)
	if !ok {
		return "", &FieldError{Kind: ErrValidation, Field: "discountPercent", Rule: "decimalRange"}
	}
	return discount, nil
}

func normalizeDiscount(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return "", false
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 3 {
		return "", false
	}
	for _, char := range parts[0] {
		if char < '0' || char > '9' {
			return "", false
		}
	}
	integer, err := strconv.Atoi(parts[0])
	if err != nil || integer > 100 {
		return "", false
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 0 || len(fraction) > 2 {
			return "", false
		}
		for _, char := range fraction {
			if char < '0' || char > '9' {
				return "", false
			}
		}
		if integer == 100 && strings.Trim(fraction, "0") != "" {
			return "", false
		}
		fraction = strings.TrimRight(fraction, "0")
	}
	canonical := strconv.Itoa(integer)
	if fraction != "" {
		canonical += "." + fraction
	}
	return canonical, true
}

func requiredStatus(value *Status) (int64, error) {
	if value == nil {
		return 0, &FieldError{Kind: ErrValidation, Field: "status", Rule: "required"}
	}
	switch *value {
	case StatusEnabled:
		return legacyEnabledStatus, nil
	case StatusDisabled:
		return legacyDisabledStatus, nil
	default:
		return 0, &FieldError{Kind: ErrValidation, Field: "status", Rule: "enum"}
	}
}

func requiredRevision(value *string) (string, error) {
	if value == nil {
		return "", &FieldError{Kind: ErrValidation, Field: "revision", Rule: "required"}
	}
	revision := strings.TrimSpace(*value)
	if len(revision) != 64 {
		return "", &FieldError{Kind: ErrValidation, Field: "revision", Rule: "opaque"}
	}
	for _, char := range revision {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return "", &FieldError{Kind: ErrValidation, Field: "revision", Rule: "opaque"}
		}
	}
	return revision, nil
}

func validateLegacyID(id string) error {
	if !legacyIDPattern.MatchString(id) {
		return &FieldError{Kind: ErrInvalidRequest, Field: "id", Rule: "legacyId"}
	}
	return nil
}
