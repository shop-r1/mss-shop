package memberlevels

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSQLiteRepositoryListAndDetailStayInsideFixedTenant(t *testing.T) {
	database, repository, binding := newMemberLevelSQLiteRepository(t)
	insertMemberLevelFixture(t, database, "100000000000000001", binding.LegacyTenantID, "Standard", "pay-1,pay-2", "10", true, legacyEnabledStatus, false, false)
	insertMemberLevelFixture(t, database, "100000000000000002", binding.LegacyTenantID, "Wholesale", "pay-1,pay-2", "20", false, legacyDisabledStatus, true, true)
	insertMemberLevelFixture(t, database, "100000000000000003", "foreign-tenant", "Foreign", "foreign-pay", "30", true, legacyEnabledStatus, false, false)

	page, err := repository.List(t.Context(), ListOptions{
		Current: 1, PageSize: 20, Query: "standard", SortBy: "name", SortOrder: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Data) != 1 || page.Data[0].ID != "100000000000000001" {
		t.Fatalf("fixed-tenant page = %#v", page)
	}
	if page.Integrity != (Integrity{FlaggedDefaultCount: 1, EnabledDefaultCount: 1}) {
		t.Fatalf("fixed-tenant integrity = %#v", page.Integrity)
	}
	if page.Data[0].Revision == "" || page.Data[0].DiscountPercent != "10" || !page.Data[0].IsDefault {
		t.Fatalf("public member level = %#v", page.Data[0])
	}

	detail, err := repository.Get(t.Context(), "100000000000000002")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != "Wholesale" || detail.Status != StatusDisabled || detail.Revision == "" {
		t.Fatalf("fixed-tenant detail = %#v", detail)
	}
	if _, err := repository.Get(t.Context(), "100000000000000003"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign detail error = %v", err)
	}
}

func TestSQLiteCreateCopiesRawPolicyAndUpdatePreservesEveryUnownedColumn(t *testing.T) {
	database, repository, binding := newMemberLevelSQLiteRepository(t)
	const sourceID = "100000000000000011"
	const updateID = "100000000000000012"
	const rawPolicy = " pay-1 ,pay-2"
	insertMemberLevelFixture(t, database, sourceID, binding.LegacyTenantID, "Source", rawPolicy, "10", true, legacyEnabledStatus, false, false)
	insertMemberLevelFixture(t, database, updateID, binding.LegacyTenantID, "Existing", "legacy-pay-raw", "20", false, legacyEnabledStatus, true, true)
	insertMemberLevelFixture(t, database, "100000000000000013", "foreign-tenant", "Foreign source", "foreign-pay", "30", false, legacyEnabledStatus, false, false)
	insertMemberLevelFixture(t, database, "100000000000000014", binding.LegacyTenantID, "Disabled source", "disabled-pay", "30", false, legacyDisabledStatus, false, false)
	insertMemberLevelFixture(t, database, "100000000000000015", binding.LegacyTenantID, "Invalid source", "pay-1,,pay-2", "30", false, legacyEnabledStatus, false, false)

	created, err := repository.Create(t.Context(), createValues{
		Name: "Created", DiscountPercent: "12.5", Status: legacyEnabledStatus,
		PaymentPolicySourceLevelID: sourceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var createdHidden struct {
		TenantID      string `gorm:"column:tenant_id"`
		HasMarket     bool   `gorm:"column:has_market"`
		ChangeCourier bool   `gorm:"column:change_courier"`
		PaymentIDs    string `gorm:"column:payment_ids"`
		Init          bool   `gorm:"column:init"`
	}
	if err := database.Raw(`SELECT tenant_id, has_market, change_courier, payment_ids, init FROM "main"."member_levels" WHERE id = ?`, created.ID).Scan(&createdHidden).Error; err != nil {
		t.Fatal(err)
	}
	if createdHidden.TenantID != binding.LegacyTenantID || createdHidden.HasMarket ||
		createdHidden.ChangeCourier || createdHidden.PaymentIDs != rawPolicy || createdHidden.Init {
		t.Fatalf("created hidden fields = %#v", createdHidden)
	}

	for source, want := range map[string]error{
		"100000000000000013": ErrPaymentPolicySource,
		"100000000000000014": ErrPaymentPolicySource,
		"100000000000000015": ErrPaymentPolicySource,
		"100000000000000099": ErrPaymentPolicySource,
	} {
		if _, err := repository.Create(t.Context(), createValues{
			Name: "Rejected " + source, DiscountPercent: "10", Status: legacyEnabledStatus,
			PaymentPolicySourceLevelID: source,
		}); !errors.Is(err, want) {
			t.Errorf("source %s error = %v", source, err)
		}
	}

	before, err := repository.Get(t.Context(), updateID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.Update(t.Context(), updateID, updateValues{
		Name: "Renamed", DiscountPercent: "25", Status: legacyDisabledStatus, Revision: before.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed" || updated.DiscountPercent != "25" || updated.Status != StatusDisabled {
		t.Fatalf("updated public fields = %#v", updated)
	}
	var preserved struct {
		TenantID      string         `gorm:"column:tenant_id"`
		CreatedAt     sql.NullTime   `gorm:"column:created_at"`
		DeletedAt     sql.NullTime   `gorm:"column:deleted_at"`
		HasMarket     bool           `gorm:"column:has_market"`
		ChangeCourier bool           `gorm:"column:change_courier"`
		PaymentIDs    sql.NullString `gorm:"column:payment_ids"`
		Init          bool           `gorm:"column:init"`
	}
	if err := database.Raw(`SELECT tenant_id, created_at, deleted_at, has_market, change_courier, payment_ids, init FROM "main"."member_levels" WHERE id = ?`, updateID).Scan(&preserved).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.TenantID != binding.LegacyTenantID || !preserved.CreatedAt.Valid || preserved.DeletedAt.Valid ||
		!preserved.HasMarket || !preserved.ChangeCourier || !preserved.PaymentIDs.Valid ||
		preserved.PaymentIDs.String != "legacy-pay-raw" || preserved.Init {
		t.Fatalf("preserved hidden fields = %#v", preserved)
	}
}

func TestSQLiteDefaultIntegrityAndTransactionalRepair(t *testing.T) {
	database, repository, binding := newMemberLevelSQLiteRepository(t)
	const invalidID = "100000000000000021"
	const duplicateID = "100000000000000022"
	const targetID = "100000000000000023"
	insertMemberLevelFixture(t, database, invalidID, binding.LegacyTenantID, "Invalid default", "pay-1", "10", true, legacyDisabledStatus, false, false)
	insertMemberLevelFixture(t, database, duplicateID, binding.LegacyTenantID, "Duplicate default", "pay-1", "20", true, legacyEnabledStatus, false, false)
	insertMemberLevelFixture(t, database, targetID, binding.LegacyTenantID, "Target", "pay-1", "30", false, legacyEnabledStatus, false, false)
	insertMemberLevelFixture(t, database, "100000000000000024", "foreign-tenant", "Foreign default", "pay-1", "40", true, legacyEnabledStatus, false, false)

	integrity, err := repository.defaultIntegrity(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if integrity != (Integrity{FlaggedDefaultCount: 2, EnabledDefaultCount: 1, InvalidDefaultCount: 1}) {
		t.Fatalf("pre-repair integrity = %#v", integrity)
	}

	invalid, err := repository.Get(t.Context(), invalidID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(t.Context(), invalidID, updateValues{
		Name: invalid.Name, DiscountPercent: invalid.DiscountPercent,
		Status: legacyEnabledStatus, Revision: invalid.Revision,
	}); !errors.Is(err, ErrDefaultRepairRequired) {
		t.Fatalf("invalid default direct-enable error = %v", err)
	}

	target, err := repository.Get(t.Context(), targetID)
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := repository.SetDefault(t.Context(), targetID, target.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.IsDefault || repaired.Status != StatusEnabled {
		t.Fatalf("repaired target = %#v", repaired)
	}
	integrity, err = repository.defaultIntegrity(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if integrity != (Integrity{FlaggedDefaultCount: 1, EnabledDefaultCount: 1}) {
		t.Fatalf("post-repair integrity = %#v", integrity)
	}
	var targetDefaults, oldDefaults, foreignDefaults int64
	if err := database.Raw(`SELECT COUNT(*) FROM "main"."member_levels" WHERE tenant_id = ? AND init = 1`, binding.LegacyTenantID).Scan(&targetDefaults).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Raw(`SELECT COUNT(*) FROM "main"."member_levels" WHERE tenant_id = ? AND id IN (?, ?) AND init = 1`, binding.LegacyTenantID, invalidID, duplicateID).Scan(&oldDefaults).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Raw(`SELECT COUNT(*) FROM "main"."member_levels" WHERE tenant_id = ? AND init = 1`, "foreign-tenant").Scan(&foreignDefaults).Error; err != nil {
		t.Fatal(err)
	}
	if targetDefaults != 1 || oldDefaults != 0 || foreignDefaults != 1 {
		t.Fatalf("default flags local/old/foreign = %d/%d/%d", targetDefaults, oldDefaults, foreignDefaults)
	}
	if _, err := repository.Update(t.Context(), targetID, updateValues{
		Name: repaired.Name, DiscountPercent: repaired.DiscountPercent,
		Status: legacyDisabledStatus, Revision: repaired.Revision,
	}); !errors.Is(err, ErrDefaultRequired) {
		t.Fatalf("default disable error = %v", err)
	}
}

func TestSQLiteDeleteRejectsTypedActiveReferencesAndPreservesRow(t *testing.T) {
	database, repository, binding := newMemberLevelSQLiteRepository(t)
	const levelID = "100000000000000031"
	const defaultID = "100000000000000032"
	insertMemberLevelFixture(t, database, levelID, binding.LegacyTenantID, "Referenced", "raw-pay", "10", false, legacyEnabledStatus, true, true)
	insertMemberLevelFixture(t, database, defaultID, binding.LegacyTenantID, "Default", "raw-pay", "20", true, legacyEnabledStatus, false, false)
	if err := database.Exec(`INSERT INTO "main"."members" (id, tenant_id, level_id) VALUES (?, ?, ?), (?, ?, ?)`,
		"member-local", binding.LegacyTenantID, levelID, "member-foreign", "foreign-tenant", levelID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO "main"."activities" (id, tenant_id, member_level_ids_data) VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
		"activity-local", binding.LegacyTenantID, "other,"+levelID+",another",
		"activity-near", binding.LegacyTenantID, levelID+"9",
		"activity-foreign", "foreign-tenant", levelID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO "main"."coupon_parents" (id, tenant_id, member_level_ids_data) VALUES (?, ?, ?), (?, ?, ?)`,
		"coupon-local", binding.LegacyTenantID, levelID,
		"coupon-near", binding.LegacyTenantID, "x"+levelID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO "main"."goods" (id, tenant_id) VALUES (?, ?), (?, ?)`,
		"goods-local", binding.LegacyTenantID, "goods-foreign", "foreign-tenant").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO "main"."goods_shipping_warehouses" (id, goods_id, member_level_price_data) VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
		"price-local", "goods-local", `[{"id":"`+levelID+`","price":10}]`,
		"price-near", "goods-local", `[{"id":"`+levelID+`9","price":10}]`,
		"price-foreign", "goods-foreign", `[{"id":"`+levelID+`","price":10}]`).Error; err != nil {
		t.Fatal(err)
	}

	record, err := repository.Get(t.Context(), levelID)
	if err != nil {
		t.Fatal(err)
	}
	err = repository.Delete(t.Context(), levelID, record.Revision)
	var referenceError *ReferenceError
	if !errors.As(err, &referenceError) {
		t.Fatalf("referenced delete error = %v", err)
	}
	if referenceError.Counts != (ReferenceCounts{Members: 1, Activities: 1, CouponTemplates: 1, GoodsPrices: 1}) {
		t.Fatalf("reference counts = %#v", referenceError.Counts)
	}
	var active int64
	if err := database.Raw(`SELECT COUNT(*) FROM "main"."member_levels" WHERE id = ? AND deleted_at IS NULL AND payment_ids = ?`, levelID, "raw-pay").Scan(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatal("failed referenced delete changed the physical legacy row")
	}

	defaultRecord, err := repository.Get(t.Context(), defaultID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Delete(t.Context(), defaultID, defaultRecord.Revision); !errors.Is(err, ErrDefaultRequired) {
		t.Fatalf("default delete error = %v", err)
	}

	for _, statement := range []string{
		`DELETE FROM "main"."members" WHERE tenant_id = '` + binding.LegacyTenantID + `'`,
		`DELETE FROM "main"."activities" WHERE tenant_id = '` + binding.LegacyTenantID + `'`,
		`DELETE FROM "main"."coupon_parents" WHERE tenant_id = '` + binding.LegacyTenantID + `'`,
		`DELETE FROM "main"."goods_shipping_warehouses" WHERE goods_id = 'goods-local'`,
	} {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.Delete(t.Context(), levelID, record.Revision); err != nil {
		t.Fatal(err)
	}
	var deleted struct {
		DeletedAt     sql.NullTime   `gorm:"column:deleted_at"`
		PaymentIDs    sql.NullString `gorm:"column:payment_ids"`
		HasMarket     bool           `gorm:"column:has_market"`
		ChangeCourier bool           `gorm:"column:change_courier"`
	}
	if err := database.Raw(`SELECT deleted_at, payment_ids, has_market, change_courier FROM "main"."member_levels" WHERE id = ?`, levelID).Scan(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if !deleted.DeletedAt.Valid || !deleted.PaymentIDs.Valid || deleted.PaymentIDs.String != "raw-pay" ||
		!deleted.HasMarket || !deleted.ChangeCourier {
		t.Fatalf("soft-deleted physical row = %#v", deleted)
	}
}

func newMemberLevelSQLiteRepository(t *testing.T) (*gorm.DB, *repository, fixedbinding.Binding) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "member-levels.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := fixedbinding.Binding{
		TenantID: "tenant-test", AdminTenantID: fixedbinding.MSS137AdminTenantID,
		LegacyTenantID: "legacy-one", BusinessSchema: "main",
	}
	repository, err := newRepository(database, binding)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE "main"."member_levels" ("id" TEXT PRIMARY KEY, "created_at" DATETIME, "updated_at" DATETIME, "deleted_at" DATETIME, "tenant_id" TEXT, "name" TEXT, "has_market" BOOLEAN, "change_courier" BOOLEAN, "payment_ids" TEXT, "ratio" NUMERIC, "init" BOOLEAN, "status" INTEGER)`,
		`CREATE TABLE "main"."members" ("id" TEXT PRIMARY KEY, "deleted_at" DATETIME, "tenant_id" TEXT, "level_id" TEXT)`,
		`CREATE TABLE "main"."activities" ("id" TEXT PRIMARY KEY, "deleted_at" DATETIME, "tenant_id" TEXT, "member_level_ids_data" TEXT)`,
		`CREATE TABLE "main"."coupon_parents" ("id" TEXT PRIMARY KEY, "deleted_at" DATETIME, "tenant_id" TEXT, "member_level_ids_data" TEXT)`,
		`CREATE TABLE "main"."goods" ("id" TEXT PRIMARY KEY, "deleted_at" DATETIME, "tenant_id" TEXT)`,
		`CREATE TABLE "main"."goods_shipping_warehouses" ("id" TEXT PRIMARY KEY, "goods_id" TEXT, "member_level_price_data" TEXT)`,
	}
	for _, statement := range statements {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatalf("create member-level fixture schema: %v: %s", err, statement)
		}
	}
	return database, repository, binding
}

func insertMemberLevelFixture(
	t *testing.T,
	database *gorm.DB,
	id, tenantID, name string,
	paymentIDs any,
	ratio string,
	init bool,
	status int64,
	hasMarket, changeCourier bool,
) {
	t.Helper()
	timestamp := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := database.Exec(
		`INSERT INTO "main"."member_levels" (id, created_at, updated_at, tenant_id, name, has_market, change_courier, payment_ids, ratio, init, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, timestamp, timestamp, tenantID, name, hasMarket, changeCourier, paymentIDs, ratio, init, status,
	).Error; err != nil {
		t.Fatalf("insert member-level fixture: %v", err)
	}
}
