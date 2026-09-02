package memberlevels

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

type legacyMemberLevelRow struct {
	ID            string         `gorm:"column:id"`
	CreatedAt     sql.NullTime   `gorm:"column:created_at"`
	UpdatedAt     sql.NullTime   `gorm:"column:updated_at"`
	TenantID      sql.NullString `gorm:"column:tenant_id"`
	Name          sql.NullString `gorm:"column:name"`
	HasMarket     sql.NullBool   `gorm:"column:has_market"`
	ChangeCourier sql.NullBool   `gorm:"column:change_courier"`
	PaymentIDs    sql.NullString `gorm:"column:payment_ids"`
	Ratio         sql.NullString `gorm:"column:ratio"`
	Init          sql.NullBool   `gorm:"column:init"`
	Status        sql.NullInt64  `gorm:"column:status"`
}

func (row legacyMemberLevelRow) view() (MemberLevel, error) {
	if !legacyIDPattern.MatchString(row.ID) || !row.Name.Valid || !validLegacyName(row.Name.String) ||
		!row.Ratio.Valid {
		return MemberLevel{}, ErrLegacyData
	}
	discount, ok := normalizeDiscount(row.Ratio.String)
	if !ok {
		return MemberLevel{}, ErrLegacyData
	}
	return MemberLevel{
		ID:              row.ID,
		Name:            row.Name.String,
		DiscountPercent: discount,
		Status:          row.publicStatus(),
		IsDefault:       row.Init.Valid && row.Init.Bool,
		CreatedAt:       publicTimestamp(row.CreatedAt),
		UpdatedAt:       publicTimestamp(row.UpdatedAt),
		Revision:        row.revision(),
	}, nil
}

func (row legacyMemberLevelRow) publicStatus() Status {
	if !row.Status.Valid {
		return StatusUnknown
	}
	switch row.Status.Int64 {
	case legacyEnabledStatus:
		return StatusEnabled
	case legacyDisabledStatus:
		return StatusDisabled
	default:
		return StatusUnknown
	}
}

func (row legacyMemberLevelRow) revision() string {
	// This token is a concurrency checksum, not an authentication secret. Until
	// the Host has a shared HMAC-key contract, hash only public row state and the
	// database concurrency timestamp; including hidden payment policy would
	// create an offline candidate oracle. Column-level writes still preserve all
	// hidden values, and cutover gating excludes the old writer.
	payload := struct {
		ID        string
		CreatedAt sql.NullTime
		UpdatedAt sql.NullTime
		Name      sql.NullString
		Ratio     sql.NullString
		Init      sql.NullBool
		Status    sql.NullInt64
	}{
		ID: row.ID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Name: row.Name, Ratio: row.Ratio, Init: row.Init, Status: row.Status,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func (row legacyMemberLevelRow) hasUsablePaymentPolicy() bool {
	return row.Status.Valid && row.Status.Int64 == legacyEnabledStatus &&
		validRawPaymentPolicy(row.PaymentIDs)
}

func validRawPaymentPolicy(policy sql.NullString) bool {
	if !policy.Valid || policy.String == "" {
		return false
	}
	for _, token := range strings.Split(policy.String, ",") {
		if strings.TrimSpace(token) == "" {
			return false
		}
	}
	return true
}

func validLegacyName(name string) bool {
	return utf8.ValidString(name) && strings.TrimSpace(name) != "" && len(name) <= maximumNameBytes
}

func publicTimestamp(value sql.NullTime) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}
