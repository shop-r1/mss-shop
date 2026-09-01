package mallsettings

import "unicode/utf8"

const (
	maximumMallNameBytes           = 256
	maximumOrderPrefixBytes        = 64
	maximumDefaultSenderNameBytes  = 256
	maximumDefaultSenderPhoneBytes = 64
)

// MutationAvailability is server-projected capability state. It is not derived
// solely from MSS permissions because root users bypass policy checks while the
// isolated PostgreSQL projection is intentionally SELECT-only.
type MutationAvailability struct {
	Update bool `json:"update"`
}

// GeneralSettings is deliberately closed over the four reviewed, non-secret
// appConfig values plus one stable capability object. It never carries raw
// metadata or unrelated legacy fields.
type GeneralSettings struct {
	MallName           string               `json:"mallName"`
	OrderPrefix        string               `json:"orderPrefix"`
	DefaultSenderName  string               `json:"defaultSenderName"`
	DefaultSenderPhone string               `json:"defaultSenderPhone"`
	Operations         MutationAvailability `json:"operations"`
}

// UpdateGeneralSettingsInput uses pointers so PUT can require a complete
// representation while still allowing every individual value to be empty.
type UpdateGeneralSettingsInput struct {
	MallName           *string `json:"mallName"`
	OrderPrefix        *string `json:"orderPrefix"`
	DefaultSenderName  *string `json:"defaultSenderName"`
	DefaultSenderPhone *string `json:"defaultSenderPhone"`
}

func (input UpdateGeneralSettingsInput) settings() (GeneralSettings, error) {
	fields := []struct {
		mapping legacyGeneralField
		value   *string
		limit   int
	}{
		{mapping: legacyGeneralFields.MallName, value: input.MallName, limit: maximumMallNameBytes},
		{mapping: legacyGeneralFields.OrderPrefix, value: input.OrderPrefix, limit: maximumOrderPrefixBytes},
		{mapping: legacyGeneralFields.DefaultSenderName, value: input.DefaultSenderName, limit: maximumDefaultSenderNameBytes},
		{mapping: legacyGeneralFields.DefaultSenderPhone, value: input.DefaultSenderPhone, limit: maximumDefaultSenderPhoneBytes},
	}
	for _, field := range fields {
		if field.value == nil {
			return GeneralSettings{}, &FieldError{Kind: ErrValidation, Field: field.mapping.APIName, Rule: "required"}
		}
		if !utf8.ValidString(*field.value) {
			return GeneralSettings{}, &FieldError{Kind: ErrValidation, Field: field.mapping.APIName, Rule: "utf8"}
		}
		if len(*field.value) > field.limit {
			return GeneralSettings{}, &FieldError{Kind: ErrValidation, Field: field.mapping.APIName, Rule: "maxBytes"}
		}
	}
	return GeneralSettings{
		MallName:           *input.MallName,
		OrderPrefix:        *input.OrderPrefix,
		DefaultSenderName:  *input.DefaultSenderName,
		DefaultSenderPhone: *input.DefaultSenderPhone,
	}, nil
}
