package mallsettings

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"math/big"
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
	return &Repository{
		database: session,
		binding:  binding,
		table:    qualifiedSystemConfigsTable(binding),
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
		if persisted != settings {
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

func qualifiedSystemConfigsTable(binding fixedbinding.Binding) string {
	return "\"" + binding.BusinessSchema + "\".\"system_configs\""
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
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, ErrLegacyMetadata
	}
	return encoded, nil
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
		errors.Is(err, ErrSchemaNotReady) ||
		errors.Is(err, ErrPersistence) ||
		errors.Is(err, ErrLegacyMetadata)
}
