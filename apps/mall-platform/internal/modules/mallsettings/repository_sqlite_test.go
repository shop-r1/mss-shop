package mallsettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSQLiteRepositoryPreservesUnknownAndNestedSecretValueBytes(t *testing.T) {
	t.Parallel()

	database, binding := openMallSettingsSQLite(t)
	metadata := []byte(`{"nestedSecret": { "appSecret" : "s3", "tokens" : [ "a", "b" ] }, "unknown": [ 1, { "keep" : true } ], "mall_name":"Old","ewePrefix":"OLD","default_sender_name":"Old sender","default_sender_phone":"1"}`)
	before, err := decodeMetadataObject(metadata)
	if err != nil {
		t.Fatal(err)
	}
	insertMallSettingsRow(t, database, binding, "100000000000000001", metadata, nil)
	foreignMetadata := []byte(`{"mall_name":"Foreign","nestedSecret": { "appSecret" : "foreign" }}`)
	insertRawMallSettingsRow(t, database, "100000000000000002", "foreign-tenant", legacyConfigName, foreignMetadata, nil)

	repository, err := NewRepository(database, binding)
	if err != nil {
		t.Fatal(err)
	}
	want := GeneralSettings{
		MallName: "New mall", OrderPrefix: "NEW",
		DefaultSenderName: "New sender", DefaultSenderPhone: "10086",
	}
	got, err := repository.PutGeneral(t.Context(), want)
	if err != nil {
		t.Fatal(err)
	}
	if !sameGeneralSettingsValues(got, want) {
		t.Fatalf("persisted settings = %#v, want %#v", got, want)
	}

	rows := readAllMallSettingsRows(t, database, binding)
	if len(rows) != 1 || rows[0].ID != "100000000000000001" {
		t.Fatalf("fixed-tenant rows = %#v", rows)
	}
	after, err := decodeMetadataObject(rows[0].Metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"nestedSecret", "unknown"} {
		if !bytes.Equal(after[key], before[key]) {
			t.Fatalf("metadata value %q bytes changed: before=%q after=%q", key, before[key], after[key])
		}
	}
	assertMetadataString(t, after, legacyGeneralFields.MallName.MetadataKey, want.MallName)
	assertMetadataString(t, after, legacyGeneralFields.OrderPrefix.MetadataKey, want.OrderPrefix)
	assertMetadataString(t, after, legacyGeneralFields.DefaultSenderName.MetadataKey, want.DefaultSenderName)
	assertMetadataString(t, after, legacyGeneralFields.DefaultSenderPhone.MetadataKey, want.DefaultSenderPhone)

	var foreign []systemConfigRow
	if err := database.Table(qualifiedRelation(binding.BusinessSchema, "system_configs")).
		Where("\"tenant_id\" = ?", "foreign-tenant").Find(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	if len(foreign) != 1 || !bytes.Equal(foreign[0].Metadata, foreignMetadata) {
		t.Fatalf("foreign tenant row changed: %#v", foreign)
	}
}

func TestSQLiteRepositoryRejectsDuplicateActiveRowsWithoutMutation(t *testing.T) {
	t.Parallel()

	database, binding := openMallSettingsSQLite(t)
	first := []byte(`{"mall_name":"First","unknown": { "keep" : 1 }}`)
	second := []byte(`{"mall_name":"Second","unknown": { "keep" : 2 }}`)
	insertMallSettingsRow(t, database, binding, "100000000000000011", first, nil)
	insertMallSettingsRow(t, database, binding, "100000000000000012", second, nil)
	repository, err := NewRepository(database, binding)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repository.GetGeneral(t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate read error = %v", err)
	}
	if _, err := repository.PutGeneral(t.Context(), GeneralSettings{MallName: "Replacement"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate update error = %v", err)
	}
	rows := readAllMallSettingsRows(t, database, binding)
	if len(rows) != 2 || !bytes.Equal(rows[0].Metadata, first) || !bytes.Equal(rows[1].Metadata, second) {
		t.Fatalf("duplicate rows changed: %#v", rows)
	}
}

func TestSQLiteRepositoryLeavesSoftDeletedRowAndCreatesFreshActiveRow(t *testing.T) {
	t.Parallel()

	database, binding := openMallSettingsSQLite(t)
	deletedAt := time.Now().UTC().Add(-time.Hour)
	deletedMetadata := []byte(`{"mall_name":"Deleted","nestedSecret": { "keep" : true }}`)
	deletedID := "100000000000000021"
	insertMallSettingsRow(t, database, binding, deletedID, deletedMetadata, deletedAt)
	repository, err := NewRepository(database, binding)
	if err != nil {
		t.Fatal(err)
	}

	empty, err := repository.GetGeneral(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if empty != (GeneralSettings{}) {
		t.Fatalf("soft-deleted row leaked into read: %#v", empty)
	}
	want := GeneralSettings{
		MallName: "Fresh", OrderPrefix: "FR",
		DefaultSenderName: "Sender", DefaultSenderPhone: "2",
	}
	if _, err := repository.PutGeneral(t.Context(), want); err != nil {
		t.Fatal(err)
	}

	rows := readAllMallSettingsRows(t, database, binding)
	if len(rows) != 2 {
		t.Fatalf("all row count = %d, want 2", len(rows))
	}
	var deleted, active *systemConfigRow
	for index := range rows {
		row := &rows[index]
		if row.DeletedAt.Valid {
			deleted = row
		} else {
			active = row
		}
	}
	if deleted == nil || deleted.ID != deletedID || !bytes.Equal(deleted.Metadata, deletedMetadata) {
		t.Fatalf("historical row changed: %#v", deleted)
	}
	if active == nil || active.ID == deletedID || len(active.ID) != 18 {
		t.Fatalf("fresh active row = %#v", active)
	}
	got, err := decodeGeneralSettings(active.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !sameGeneralSettingsValues(got, want) {
		t.Fatalf("fresh settings = %#v, want %#v", got, want)
	}
}

func TestEnabledApplicationGateAppliesOnlyWhenRepositorySupportsUpdates(t *testing.T) {
	t.Parallel()

	database, binding := openMallSettingsSQLite(t)
	application := &requestApplication{
		database: func(context.Context) (*gorm.DB, bool) { return database, true },
		binding:  binding,
		writes:   writeCapabilityForValue("true"),
	}
	mallName, orderPrefix := "Application mall", "APP"
	senderName, senderPhone := "Application sender", "3"
	input := UpdateGeneralSettingsInput{
		MallName: &mallName, OrderPrefix: &orderPrefix,
		DefaultSenderName: &senderName, DefaultSenderPhone: &senderPhone,
	}
	persisted, err := application.PutGeneral(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Operations.Update {
		t.Fatal("enabled application did not advertise the writable SQLite capability")
	}
	loaded, err := application.GetGeneral(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Operations.Update || !sameGeneralSettingsValues(loaded, persisted) {
		t.Fatalf("loaded application settings = %#v, persisted = %#v", loaded, persisted)
	}
}

func openMallSettingsSQLite(t *testing.T) (*gorm.DB, fixedbinding.Binding) {
	t.Helper()
	database, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "mall-settings.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE "main"."system_configs" (
		"id" TEXT PRIMARY KEY,
		"created_at" DATETIME NOT NULL,
		"updated_at" DATETIME NOT NULL,
		"deleted_at" DATETIME,
		"tenant_id" TEXT NOT NULL,
		"name" TEXT NOT NULL,
		"metadata" BLOB NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	binding := mallSettingsTestBinding()
	binding.BusinessSchema = "main"
	return database, binding
}

func insertMallSettingsRow(
	t *testing.T,
	database *gorm.DB,
	binding fixedbinding.Binding,
	id string,
	metadata []byte,
	deletedAt any,
) {
	t.Helper()
	insertRawMallSettingsRow(t, database, id, binding.LegacyTenantID, legacyConfigName, metadata, deletedAt)
}

func insertRawMallSettingsRow(
	t *testing.T,
	database *gorm.DB,
	id, tenantID, name string,
	metadata []byte,
	deletedAt any,
) {
	t.Helper()
	now := time.Now().UTC()
	if err := database.Exec(
		`INSERT INTO "main"."system_configs"
		("id", "created_at", "updated_at", "deleted_at", "tenant_id", "name", "metadata")
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, now, now, deletedAt, tenantID, name, metadata,
	).Error; err != nil {
		t.Fatal(err)
	}
}

func readAllMallSettingsRows(
	t *testing.T,
	database *gorm.DB,
	binding fixedbinding.Binding,
) []systemConfigRow {
	t.Helper()
	var rows []systemConfigRow
	if err := database.Unscoped().Table(qualifiedRelation(binding.BusinessSchema, "system_configs")).
		Where("\"tenant_id\" = ? AND \"name\" = ?", binding.LegacyTenantID, legacyConfigName).
		Order("\"id\"").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertMetadataString(t *testing.T, object map[string]json.RawMessage, key, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(object[key], &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("metadata %q = %q, want %q", key, got, want)
	}
}
