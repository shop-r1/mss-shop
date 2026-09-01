package mallsettings

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type systemConfigRow struct {
	ID        string         `gorm:"column:id;primaryKey"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at"`
	TenantID  string         `gorm:"column:tenant_id"`
	Name      string         `gorm:"column:name"`
	Metadata  []byte         `gorm:"column:metadata"`
}

type Repository struct {
	database *gorm.DB
	binding  fixedbinding.Binding
	table    string
	writable bool
}

func NewRepository(database *gorm.DB, binding fixedbinding.Binding) (*Repository, error) {
	if database == nil {
		return nil, ErrSchemaNotReady
	}
	if err := binding.Validate(); err != nil {
		return nil, ErrSchemaNotReady
	}
	if database.Dialector == nil {
		return nil, ErrSchemaNotReady
	}
	// Metadata can contain historical credentials. Suppress SQL/value logging
	// for every operation that reads or rewrites the legacy JSON object.
	session := database.Session(&gorm.Session{Logger: logger.Discard})
	table, writable, err := systemConfigsRelation(binding, database.Dialector.Name())
	if err != nil {
		return nil, err
	}
	return &Repository{
		database: session,
		binding:  binding,
		table:    table,
		writable: writable,
	}, nil
}

// GetGeneral returns an empty typed projection when appConfig has never been
// created. Multiple active rows fail closed instead of choosing one by order.
func (repository *Repository) GetGeneral(ctx context.Context) (GeneralSettings, error) {
	if repository == nil || repository.database == nil || ctx == nil {
		return GeneralSettings{}, ErrPersistence
	}
	rows, err := repository.findRows(ctx, repository.database, false)
	if err != nil {
		return GeneralSettings{}, err
	}
	if len(rows) > 1 {
		return GeneralSettings{}, ErrConflict
	}
	if len(rows) == 0 {
		return GeneralSettings{}, nil
	}
	return decodeGeneralSettings(rows[0].Metadata)
}

// PutGeneral performs the complete legacy upsert in one transaction. An
// existing active row is updated; when none exists a fresh row is created and
// every historical soft-deleted row remains untouched.
func (repository *Repository) PutGeneral(ctx context.Context, settings GeneralSettings) (GeneralSettings, error) {
	if repository == nil || !repository.supportsUpdate() {
		return GeneralSettings{}, ErrMutationDisabled
	}
	if repository == nil || repository.database == nil || ctx == nil {
		return GeneralSettings{}, ErrPersistence
	}
	var persisted GeneralSettings
	err := repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := repository.lockConfiguration(tx); err != nil {
			return err
		}
		active, err := repository.findRows(ctx, tx, true)
		if err != nil {
			return err
		}
		if len(active) > 1 {
			return ErrConflict
		}

		now := time.Now()
		switch len(active) {
		case 1:
			metadata, mergeErr := mergeGeneralSettings(active[0].Metadata, settings)
			if mergeErr != nil {
				return mergeErr
			}
			result := tx.Table(repository.table).Where(
				"\"id\" = ? AND \"tenant_id\" = ? AND \"name\" = ? AND \"deleted_at\" IS NULL",
				active[0].ID, repository.binding.LegacyTenantID, legacyConfigName,
			).Updates(map[string]any{"metadata": metadata, "updated_at": now})
			if result.Error != nil {
				return ErrPersistence
			}
			if result.RowsAffected != 1 {
				return ErrConflict
			}
		default:
			// A deleted appConfig is historical evidence, not an update target.
			// Always create a new active row and leave every tombstone untouched.
			metadata, mergeErr := mergeGeneralSettings(nil, settings)
			if mergeErr != nil {
				return mergeErr
			}
			id, idErr := newLegacyID()
			if idErr != nil {
				return ErrPersistence
			}
			row := systemConfigRow{
				ID: id, CreatedAt: now, UpdatedAt: now,
				TenantID: repository.binding.LegacyTenantID,
				Name:     legacyConfigName, Metadata: metadata,
			}
			if err := tx.Table(repository.table).Create(&row).Error; err != nil {
				return ErrPersistence
			}
		}

		confirmed, err := repository.findRows(ctx, tx, true)
		if err != nil {
			return err
		}
		if len(confirmed) != 1 {
			return ErrConflict
		}
		persisted, err = decodeGeneralSettings(confirmed[0].Metadata)
		if err != nil {
			return err
		}
		if !sameGeneralSettingsValues(persisted, settings) {
			return ErrPersistence
		}
		return nil
	})
	if err != nil {
		if isRepositoryError(err) {
			return GeneralSettings{}, err
		}
		return GeneralSettings{}, ErrPersistence
	}
	return persisted, nil
}

func sameGeneralSettingsValues(left, right GeneralSettings) bool {
	return left.MallName == right.MallName &&
		left.OrderPrefix == right.OrderPrefix &&
		left.DefaultSenderName == right.DefaultSenderName &&
		left.DefaultSenderPhone == right.DefaultSenderPhone
}

func (repository *Repository) findRows(ctx context.Context, database *gorm.DB, lock bool) ([]systemConfigRow, error) {
	query := database.WithContext(ctx).Table(repository.table).
		Select("id", "created_at", "updated_at", "deleted_at", "tenant_id", "name", "metadata").
		Where("\"tenant_id\" = ? AND \"name\" = ? AND \"deleted_at\" IS NULL", repository.binding.LegacyTenantID, legacyConfigName).
		Order("\"id\"").
		Limit(2)
	if lock && database.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var rows []systemConfigRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, ErrPersistence
	}
	return rows, nil
}

func (repository *Repository) lockConfiguration(tx *gorm.DB) error {
	switch tx.Dialector.Name() {
	case "postgres":
		lockKey := repository.binding.BusinessSchema + ".system_configs/" + repository.binding.LegacyTenantID + "/" + legacyConfigName
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
			return ErrPersistence
		}
		return nil
	case "sqlite":
		// SQLite starts deferred transactions. A zero-row UPDATE upgrades this
		// transaction to a writer before the absence check, preventing two
		// concurrent first creates from both observing zero active rows.
		statement := "UPDATE " + repository.table + " SET \"updated_at\" = \"updated_at\" WHERE 1 = 0"
		if err := tx.Exec(statement).Error; err != nil {
			return ErrPersistence
		}
		return nil
	default:
		return ErrSchemaNotReady
	}
}

func (repository *Repository) supportsUpdate() bool {
	return repository != nil && repository.writable
}

func systemConfigsRelation(binding fixedbinding.Binding, dialect string) (string, bool, error) {
	switch dialect {
	case "postgres":
		// The reconciler-owned private view is tenant/name filtered and grants
		// runtime SELECT only. A future writable projection requires its own
		// reviewed cutover before this flag can become true.
		return qualifiedRelation(binding.BusinessSchema, postgresPrivateRelation), false, nil
	case "sqlite":
		// The repository-owned demo database retains the historical table so
		// local compatibility tests can explicitly exercise merge semantics.
		return qualifiedRelation(binding.BusinessSchema, "system_configs"), true, nil
	default:
		return "", false, ErrSchemaNotReady
	}
}

func qualifiedRelation(schema, relation string) string {
	// fixedbinding validates the schema identifier and relation is compiled.
	// Pass the dotted name without pre-quoting so GORM quotes each identifier
	// segment. Supplying embedded quotes makes GORM quote those quote bytes and
	// addresses a non-existent table named `"schema"."relation"`.
	return schema + "." + relation
}

func decodeGeneralSettings(metadata []byte) (GeneralSettings, error) {
	object, err := decodeMetadataObject(metadata)
	if err != nil {
		return GeneralSettings{}, err
	}
	mallName, err := decodeMetadataString(object, legacyGeneralFields.MallName)
	if err != nil {
		return GeneralSettings{}, err
	}
	orderPrefix, err := decodeMetadataString(object, legacyGeneralFields.OrderPrefix)
	if err != nil {
		return GeneralSettings{}, err
	}
	defaultSenderName, err := decodeMetadataString(object, legacyGeneralFields.DefaultSenderName)
	if err != nil {
		return GeneralSettings{}, err
	}
	defaultSenderPhone, err := decodeMetadataString(object, legacyGeneralFields.DefaultSenderPhone)
	if err != nil {
		return GeneralSettings{}, err
	}
	return GeneralSettings{
		MallName: mallName, OrderPrefix: orderPrefix,
		DefaultSenderName: defaultSenderName, DefaultSenderPhone: defaultSenderPhone,
	}, nil
}

func mergeGeneralSettings(metadata []byte, settings GeneralSettings) ([]byte, error) {
	object, err := decodeMetadataObject(metadata)
	if err != nil {
		return nil, err
	}
	values := []struct {
		mapping legacyGeneralField
		value   string
	}{
		{mapping: legacyGeneralFields.MallName, value: settings.MallName},
		{mapping: legacyGeneralFields.OrderPrefix, value: settings.OrderPrefix},
		{mapping: legacyGeneralFields.DefaultSenderName, value: settings.DefaultSenderName},
		{mapping: legacyGeneralFields.DefaultSenderPhone, value: settings.DefaultSenderPhone},
	}
	for _, value := range values {
		encoded, err := json.Marshal(value.value)
		if err != nil {
			return nil, ErrPersistence
		}
		object[value.mapping.MetadataKey] = json.RawMessage(encoded)
	}
	return encodeMetadataObject(object)
}

// encodeMetadataObject deliberately appends each already-validated RawMessage
// without asking encoding/json to compact or otherwise rewrite unknown nested
// values. Key order is deterministic, while unapproved value bytes—including
// secret-bearing objects—remain exactly as decoded from the legacy document.
func encodeMetadataObject(object map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	buffer := bytes.NewBuffer(make([]byte, 0, len(keys)*32))
	buffer.WriteByte('{')
	for index, key := range keys {
		raw := object[key]
		if len(raw) == 0 || !json.Valid(raw) {
			return nil, ErrLegacyMetadata
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, ErrLegacyMetadata
		}
		if index != 0 {
			buffer.WriteByte(',')
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		buffer.Write(raw)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func decodeMetadataObject(metadata []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(metadata)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return make(map[string]json.RawMessage), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, ErrLegacyMetadata
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, ErrLegacyMetadata
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, ErrLegacyMetadata
		}
		if _, duplicate := object[key]; duplicate {
			return nil, ErrLegacyMetadata
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, ErrLegacyMetadata
		}
		object[key] = append(json.RawMessage(nil), value...)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, ErrLegacyMetadata
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrLegacyMetadata
	}
	return object, nil
}

func decodeMetadataString(object map[string]json.RawMessage, mapping legacyGeneralField) (string, error) {
	raw, exists := object[mapping.MetadataKey]
	if !exists || strings.TrimSpace(string(raw)) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", ErrLegacyMetadata
	}
	return value, nil
}

func newLegacyID() (string, error) {
	// Old system_config IDs are 18 decimal characters. Randomizing inside the
	// same representation avoids the timestamp truncation collisions of the old
	// generator while remaining compatible with its varchar/consumer contract.
	span := new(big.Int).Exp(big.NewInt(10), big.NewInt(17), nil)
	span.Mul(span, big.NewInt(9))
	offset, err := rand.Int(rand.Reader, span)
	if err != nil {
		return "", err
	}
	offset.Add(offset, new(big.Int).Exp(big.NewInt(10), big.NewInt(17), nil))
	return offset.String(), nil
}

func isRepositoryError(err error) bool {
	return errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrMutationDisabled) ||
		errors.Is(err, ErrSchemaNotReady) ||
		errors.Is(err, ErrPersistence) ||
		errors.Is(err, ErrLegacyMetadata)
}
