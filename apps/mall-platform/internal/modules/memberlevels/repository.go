package memberlevels

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const legacyRowColumns = `"id", "created_at", "updated_at", "tenant_id", "name", "has_market", "change_courier", "payment_ids", CAST("ratio" AS TEXT) AS "ratio", "init", "status"`

type repository struct {
	database *gorm.DB
	binding  fixedbinding.Binding
	tables   legacyTables
}

type legacyTables struct {
	MemberLevels            string
	Members                 string
	Activities              string
	CouponParents           string
	Goods                   string
	GoodsShippingWarehouses string
}

func newRepository(database *gorm.DB, binding fixedbinding.Binding) (*repository, error) {
	if database == nil || database.Dialector == nil {
		return nil, ErrSchemaNotReady
	}
	if err := binding.Validate(); err != nil {
		return nil, ErrSchemaNotReady
	}
	// payment_ids is deliberately never logged or exposed. It is read only to
	// validate/copy the raw database value and to verify column preservation.
	session := database.Session(&gorm.Session{Logger: logger.Discard})
	return &repository{database: session, binding: binding, tables: qualifiedLegacyTables(binding)}, nil
}

func (repository *repository) List(ctx context.Context, options ListOptions) (MemberLevelPage, error) {
	if err := repository.ready(ctx); err != nil {
		return MemberLevelPage{}, err
	}
	offset, err := paginationOffset(options.Current, options.PageSize)
	if err != nil {
		return MemberLevelPage{}, err
	}
	query := repository.scoped(repository.database.WithContext(ctx).Table(repository.tables.MemberLevels))
	if options.Query != "" {
		pattern := "%" + escapeLike(options.Query) + "%"
		if repository.database.Dialector.Name() == "postgres" {
			query = query.Where(`"name" ILIKE ? ESCAPE '!'`, pattern)
		} else {
			query = query.Where(`LOWER(COALESCE("name", '')) LIKE LOWER(?) ESCAPE '!'`, pattern)
		}
	}
	if options.Status != nil {
		query = query.Where(`"status" = ?`, *options.Status)
	}
	if options.IsDefault != nil {
		query = query.Where(`"init" = ?`, *options.IsDefault)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return MemberLevelPage{}, ErrPersistence
	}
	integrity, err := repository.defaultIntegrity(ctx, repository.database)
	if err != nil {
		return MemberLevelPage{}, err
	}

	orderColumn := map[string]string{
		"name": "\"name\"", "discountPercent": "\"ratio\"", "updatedAt": "\"updated_at\"",
	}[options.SortBy]
	if orderColumn == "" {
		orderColumn = `"updated_at"`
	}
	orderDirection := "DESC"
	if options.SortOrder == "asc" {
		orderDirection = "ASC"
	}
	var rows []legacyMemberLevelRow
	if err := query.Select(legacyRowColumns).
		Order(orderColumn + " " + orderDirection).Order(`"id" ASC`).
		Limit(options.PageSize).Offset(offset).
		Find(&rows).Error; err != nil {
		return MemberLevelPage{}, ErrPersistence
	}
	items := make([]MemberLevel, 0, len(rows))
	for _, row := range rows {
		item, err := row.view()
		if err != nil || item.Revision == "" {
			return MemberLevelPage{}, ErrLegacyData
		}
		items = append(items, item)
	}
	return MemberLevelPage{
		Data: items, Total: total, Current: options.Current, PageSize: options.PageSize,
		Integrity: integrity,
	}, nil
}

func (repository *repository) Get(ctx context.Context, id string) (MemberLevel, error) {
	if err := repository.ready(ctx); err != nil {
		return MemberLevel{}, err
	}
	row, err := repository.findByID(ctx, repository.database, id, false)
	if err != nil {
		return MemberLevel{}, err
	}
	view, err := row.view()
	if err != nil || view.Revision == "" {
		return MemberLevel{}, ErrLegacyData
	}
	return view, nil
}

func (repository *repository) Create(ctx context.Context, values createValues) (MemberLevel, error) {
	if err := repository.ready(ctx); err != nil {
		return MemberLevel{}, err
	}
	var created legacyMemberLevelRow
	err := repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := repository.lockAggregate(tx); err != nil {
			return err
		}
		duplicate, err := repository.activeNameCount(tx, values.Name, "")
		if err != nil {
			return err
		}
		if duplicate != 0 {
			return ErrDuplicateName
		}
		source, err := repository.paymentPolicySource(ctx, tx, values.PaymentPolicySourceLevelID)
		if err != nil {
			return err
		}
		if !source.hasUsablePaymentPolicy() {
			return ErrPaymentPolicySource
		}
		now := time.Now().UTC()
		for attempt := 0; attempt < 8; attempt++ {
			id, err := newLegacyID()
			if err != nil {
				return ErrPersistence
			}
			row := map[string]any{
				"id": id, "created_at": now, "updated_at": now,
				"tenant_id": repository.binding.LegacyTenantID, "name": values.Name,
				"has_market": false, "change_courier": false,
				"payment_ids": source.PaymentIDs.String, "ratio": values.DiscountPercent,
				"init": false, "status": values.Status,
			}
			result := tx.Table(repository.tables.MemberLevels).
				Clauses(clause.OnConflict{DoNothing: true}).Create(row)
			if result.Error != nil {
				return ErrPersistence
			}
			if result.RowsAffected == 0 {
				continue
			}
			created, err = repository.findByID(ctx, tx, id, true)
			if err != nil {
				return err
			}
			if !sameNullableString(created.PaymentIDs, source.PaymentIDs) ||
				!created.HasMarket.Valid || created.HasMarket.Bool ||
				!created.ChangeCourier.Valid || created.ChangeCourier.Bool {
				return ErrPersistence
			}
			return nil
		}
		return ErrPersistence
	})
	if err != nil {
		return MemberLevel{}, repository.domainError(err)
	}
	view, err := created.view()
	if err != nil || view.Revision == "" {
		return MemberLevel{}, ErrLegacyData
	}
	return view, nil
}

func (repository *repository) Update(ctx context.Context, id string, values updateValues) (MemberLevel, error) {
	if err := repository.ready(ctx); err != nil {
		return MemberLevel{}, err
	}
	var updated legacyMemberLevelRow
	err := repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := repository.lockAggregate(tx); err != nil {
			return err
		}
		current, err := repository.findByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if current.revision() != values.Revision {
			return ErrRevisionConflict
		}
		if current.Init.Valid && current.Init.Bool {
			currentEnabled := current.Status.Valid && current.Status.Int64 == legacyEnabledStatus
			switch {
			case currentEnabled && values.Status != legacyEnabledStatus:
				return ErrDefaultRequired
			case !currentEnabled && values.Status == legacyEnabledStatus:
				// An invalid flagged default must be repaired through SetDefault so
				// all competing flags are cleared in the same transaction.
				return ErrDefaultRepairRequired
			}
		}
		duplicate, err := repository.activeNameCount(tx, values.Name, id)
		if err != nil {
			return err
		}
		if duplicate != 0 {
			return ErrDuplicateName
		}
		result := repository.optimisticRow(tx.Table(repository.tables.MemberLevels), current).
			Updates(map[string]any{
				"name": values.Name, "ratio": values.DiscountPercent,
				"status": values.Status, "updated_at": time.Now().UTC(),
			})
		if result.Error != nil {
			return ErrPersistence
		}
		if result.RowsAffected != 1 {
			return ErrRevisionConflict
		}
		updated, err = repository.findByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if !unownedColumnsEqual(current, updated) {
			return ErrRevisionConflict
		}
		return nil
	})
	if err != nil {
		return MemberLevel{}, repository.domainError(err)
	}
	view, err := updated.view()
	if err != nil || view.Revision == "" {
		return MemberLevel{}, ErrLegacyData
	}
	return view, nil
}

func (repository *repository) SetDefault(ctx context.Context, id, revision string) (MemberLevel, error) {
	if err := repository.ready(ctx); err != nil {
		return MemberLevel{}, err
	}
	var updated legacyMemberLevelRow
	err := repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := repository.lockAggregate(tx); err != nil {
			return err
		}
		current, err := repository.findByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if current.revision() != revision {
			return ErrRevisionConflict
		}
		if !current.Status.Valid || current.Status.Int64 != legacyEnabledStatus {
			return ErrDefaultRequired
		}
		if !current.hasUsablePaymentPolicy() {
			return ErrPaymentPolicySource
		}
		var allDefaults int64
		if err := repository.scoped(tx.Table(repository.tables.MemberLevels)).Where(`"init" = ?`, true).Count(&allDefaults).Error; err != nil {
			return ErrPersistence
		}
		if current.Init.Valid && current.Init.Bool && allDefaults == 1 {
			updated = current
			return nil
		}
		now := time.Now().UTC()
		if err := repository.scoped(tx.Table(repository.tables.MemberLevels)).Where(`"init" = ?`, true).
			Updates(map[string]any{"init": false, "updated_at": now}).Error; err != nil {
			return ErrPersistence
		}
		result := tx.Table(repository.tables.MemberLevels).Where(
			`"id" = ? AND "tenant_id" = ? AND "deleted_at" IS NULL`, id, repository.binding.LegacyTenantID,
		).Updates(map[string]any{"init": true, "updated_at": now})
		if result.Error != nil {
			return ErrPersistence
		}
		if result.RowsAffected != 1 {
			return ErrRevisionConflict
		}
		var confirmed int64
		if err := repository.scoped(tx.Table(repository.tables.MemberLevels)).Where(`"init" = ?`, true).Count(&confirmed).Error; err != nil {
			return ErrPersistence
		}
		if confirmed != 1 {
			return ErrConflict
		}
		updated, err = repository.findByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if !updated.Init.Valid || !updated.Init.Bool || !updated.Status.Valid || updated.Status.Int64 != legacyEnabledStatus {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return MemberLevel{}, repository.domainError(err)
	}
	view, err := updated.view()
	if err != nil || view.Revision == "" {
		return MemberLevel{}, ErrLegacyData
	}
	return view, nil
}

func (repository *repository) Delete(ctx context.Context, id, revision string) error {
	if err := repository.ready(ctx); err != nil {
		return err
	}
	err := repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := repository.lockAggregate(tx); err != nil {
			return err
		}
		current, err := repository.findByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if current.revision() != revision {
			return ErrRevisionConflict
		}
		if current.Init.Valid && current.Init.Bool {
			return ErrDefaultRequired
		}
		counts, err := repository.referenceCounts(tx, id)
		if err != nil {
			return err
		}
		if counts.total() != 0 {
			return &ReferenceError{Counts: counts}
		}
		result := repository.optimisticRow(tx.Table(repository.tables.MemberLevels), current).
			Updates(map[string]any{"deleted_at": time.Now().UTC(), "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return ErrPersistence
		}
		if result.RowsAffected != 1 {
			return ErrRevisionConflict
		}
		return nil
	})
	if err != nil {
		return repository.domainError(err)
	}
	return nil
}

func (repository *repository) paymentPolicySource(ctx context.Context, tx *gorm.DB, sourceID string) (legacyMemberLevelRow, error) {
	if sourceID != "" {
		if err := validateLegacyID(sourceID); err != nil {
			return legacyMemberLevelRow{}, ErrPaymentPolicySource
		}
		source, err := repository.findByID(ctx, tx, sourceID, true)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return legacyMemberLevelRow{}, ErrPaymentPolicySource
			}
			return legacyMemberLevelRow{}, err
		}
		if !source.hasUsablePaymentPolicy() {
			return legacyMemberLevelRow{}, ErrPaymentPolicySource
		}
		return source, nil
	}
	var rows []legacyMemberLevelRow
	query := repository.scoped(tx.Table(repository.tables.MemberLevels)).Select(legacyRowColumns).
		Where(`"init" = ? AND "status" = ?`, true, legacyEnabledStatus).Order(`"id"`).Limit(2)
	if tx.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Find(&rows).Error; err != nil {
		return legacyMemberLevelRow{}, ErrPersistence
	}
	if len(rows) != 1 || !rows[0].hasUsablePaymentPolicy() {
		return legacyMemberLevelRow{}, ErrPaymentPolicySource
	}
	return rows[0], nil
}

func (repository *repository) findByID(ctx context.Context, database *gorm.DB, id string, lock bool) (legacyMemberLevelRow, error) {
	query := repository.scoped(database.WithContext(ctx).Table(repository.tables.MemberLevels)).
		Select(legacyRowColumns).Where(`"id" = ?`, id)
	if lock && database.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var row legacyMemberLevelRow
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return legacyMemberLevelRow{}, ErrNotFound
		}
		return legacyMemberLevelRow{}, ErrPersistence
	}
	return row, nil
}

func (repository *repository) activeNameCount(tx *gorm.DB, name, excludedID string) (int64, error) {
	query := repository.scoped(tx.Table(repository.tables.MemberLevels)).Where(`"name" = ?`, name)
	if excludedID != "" {
		query = query.Where(`"id" <> ?`, excludedID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, ErrPersistence
	}
	return count, nil
}

func (repository *repository) defaultIntegrity(ctx context.Context, database *gorm.DB) (Integrity, error) {
	integrity := Integrity{}
	if err := repository.scoped(database.WithContext(ctx).Table(repository.tables.MemberLevels)).
		Where(`"init" = ?`, true).Count(&integrity.FlaggedDefaultCount).Error; err != nil {
		return Integrity{}, ErrPersistence
	}
	if err := repository.scoped(database.WithContext(ctx).Table(repository.tables.MemberLevels)).
		Where(`"init" = ? AND "status" = ?`, true, legacyEnabledStatus).
		Count(&integrity.EnabledDefaultCount).Error; err != nil {
		return Integrity{}, ErrPersistence
	}
	integrity.InvalidDefaultCount = integrity.FlaggedDefaultCount - integrity.EnabledDefaultCount
	if integrity.InvalidDefaultCount < 0 {
		return Integrity{}, ErrLegacyData
	}
	return integrity, nil
}

func (repository *repository) referenceCounts(tx *gorm.DB, id string) (ReferenceCounts, error) {
	counts := ReferenceCounts{}
	if err := tx.Table(repository.tables.Members).Where(
		`"tenant_id" = ? AND "level_id" = ? AND "deleted_at" IS NULL`, repository.binding.LegacyTenantID, id,
	).Count(&counts.Members).Error; err != nil {
		return ReferenceCounts{}, ErrPersistence
	}
	csvQuery, csvArgs, err := repository.csvReferenceExpression(id)
	if err != nil {
		return ReferenceCounts{}, err
	}
	activityArgs := append([]any{repository.binding.LegacyTenantID}, csvArgs...)
	if err := tx.Table(repository.tables.Activities).Where(
		`"tenant_id" = ? AND "deleted_at" IS NULL AND (`+csvQuery+`)`, activityArgs...,
	).Count(&counts.Activities).Error; err != nil {
		return ReferenceCounts{}, ErrPersistence
	}
	couponArgs := append([]any{repository.binding.LegacyTenantID}, csvArgs...)
	if err := tx.Table(repository.tables.CouponParents).Where(
		`"tenant_id" = ? AND "deleted_at" IS NULL AND (`+csvQuery+`)`, couponArgs...,
	).Count(&counts.CouponTemplates).Error; err != nil {
		return ReferenceCounts{}, ErrPersistence
	}
	goodsPrices, err := repository.goodsPriceReferenceCount(tx, id)
	if err != nil {
		return ReferenceCounts{}, err
	}
	counts.GoodsPrices = goodsPrices
	return counts, nil
}

func (repository *repository) csvReferenceExpression(id string) (string, []any, error) {
	if repository == nil || repository.database == nil || repository.database.Dialector == nil {
		return "", nil, ErrSchemaNotReady
	}
	return csvReferenceExpressionForDialect(repository.database.Dialector.Name(), id)
}

func csvReferenceExpressionForDialect(dialect, id string) (string, []any, error) {
	if err := validateLegacyID(id); err != nil {
		return "", nil, ErrPersistence
	}
	switch dialect {
	case "postgres":
		return `EXISTS (SELECT 1 FROM unnest(string_to_array(COALESCE("member_level_ids_data", ''), ',')) AS token WHERE btrim(token) = ?)`, []any{id}, nil
	case "sqlite":
		return `(',' || REPLACE(COALESCE("member_level_ids_data", ''), ' ', '') || ',') LIKE ? ESCAPE '!'`, []any{"%," + escapeLike(id) + ",%"}, nil
	default:
		return "", nil, ErrSchemaNotReady
	}
}

func (repository *repository) goodsPriceReferenceCount(tx *gorm.DB, id string) (int64, error) {
	join := "JOIN " + repository.tables.Goods + ` AS goods ON goods."id" = goods_prices."goods_id"`
	base := func() *gorm.DB {
		return tx.Table(repository.tables.GoodsShippingWarehouses+" AS goods_prices").Joins(join).
			Where(`goods."tenant_id" = ? AND goods."deleted_at" IS NULL`, repository.binding.LegacyTenantID)
	}
	var invalid int64
	switch repository.database.Dialector.Name() {
	case "postgres":
		if err := base().Where(`goods_prices."member_level_price_data" IS NOT NULL AND json_typeof(goods_prices."member_level_price_data") <> 'array'`).Count(&invalid).Error; err != nil {
			return 0, ErrPersistence
		}
		if invalid != 0 {
			return 0, ErrLegacyData
		}
		var count int64
		expression := `EXISTS (SELECT 1 FROM json_array_elements(COALESCE(goods_prices."member_level_price_data", '[]'::json)) AS price WHERE price->>'id' = ?)`
		if err := base().Where(expression, id).Count(&count).Error; err != nil {
			return 0, ErrPersistence
		}
		return count, nil
	case "sqlite":
		if err := base().Where(`goods_prices."member_level_price_data" IS NOT NULL AND (json_valid(CAST(goods_prices."member_level_price_data" AS TEXT)) = 0 OR json_type(CAST(goods_prices."member_level_price_data" AS TEXT)) <> 'array')`).Count(&invalid).Error; err != nil {
			return 0, ErrPersistence
		}
		if invalid != 0 {
			return 0, ErrLegacyData
		}
		var count int64
		expression := `EXISTS (SELECT 1 FROM json_each(COALESCE(CAST(goods_prices."member_level_price_data" AS TEXT), '[]')) AS price WHERE json_extract(price.value, '$.id') = ?)`
		if err := base().Where(expression, id).Count(&count).Error; err != nil {
			return 0, ErrPersistence
		}
		return count, nil
	default:
		return 0, ErrSchemaNotReady
	}
}

func (repository *repository) lockAggregate(tx *gorm.DB) error {
	switch tx.Dialector.Name() {
	case "postgres":
		key := repository.binding.BusinessSchema + ".member_levels/" + repository.binding.LegacyTenantID
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error; err != nil {
			return ErrPersistence
		}
		return nil
	case "sqlite":
		statement := "UPDATE " + repository.tables.MemberLevels + ` SET "updated_at" = "updated_at" WHERE 1 = 0`
		if err := tx.Exec(statement).Error; err != nil {
			return ErrPersistence
		}
		return nil
	default:
		return ErrSchemaNotReady
	}
}

func (repository *repository) scoped(query *gorm.DB) *gorm.DB {
	return query.Where(`"tenant_id" = ? AND "deleted_at" IS NULL`, repository.binding.LegacyTenantID)
}

func (repository *repository) optimisticRow(query *gorm.DB, row legacyMemberLevelRow) *gorm.DB {
	query = query.Where(`"id" = ? AND "tenant_id" = ? AND "deleted_at" IS NULL`, row.ID, repository.binding.LegacyTenantID)
	if row.UpdatedAt.Valid {
		return query.Where(`"updated_at" = ?`, row.UpdatedAt.Time)
	}
	return query.Where(`"updated_at" IS NULL`)
}

func (repository *repository) ready(ctx context.Context) error {
	if repository == nil || repository.database == nil || repository.database.Dialector == nil || ctx == nil {
		return ErrPersistence
	}
	return nil
}

func (repository *repository) domainError(err error) error {
	var referenceError *ReferenceError
	if errors.As(err, &referenceError) {
		return referenceError
	}
	for _, known := range []error{
		ErrNotFound, ErrDuplicateName, ErrConflict, ErrRevisionConflict,
		ErrPaymentPolicySource, ErrDefaultRequired, ErrDefaultRepairRequired,
		ErrInUse, ErrSchemaNotReady,
		ErrLegacyData, ErrPersistence,
	} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrPersistence
}

func qualifiedLegacyTables(binding fixedbinding.Binding) legacyTables {
	// fixedbinding validates the schema identifier and every table name below
	// is compiled. A plain dotted name lets GORM quote both segments correctly;
	// embedding quotes here would make them part of the table identifier.
	qualify := func(table string) string { return binding.BusinessSchema + "." + table }
	return legacyTables{
		MemberLevels: qualify("member_levels"), Members: qualify("members"),
		Activities: qualify("activities"), CouponParents: qualify("coupon_parents"),
		Goods: qualify("goods"), GoodsShippingWarehouses: qualify("goods_shipping_warehouses"),
	}
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)
	return replacer.Replace(value)
}

func paginationOffset(current, pageSize int) (int, error) {
	if current < 1 || pageSize < 1 || pageSize > maximumPageSize {
		return 0, &FieldError{Kind: ErrValidation, Field: "pagination", Rule: "range"}
	}
	maximumInt := int(^uint(0) >> 1)
	if current-1 > maximumInt/pageSize {
		return 0, &FieldError{Kind: ErrValidation, Field: "current", Rule: "range"}
	}
	return (current - 1) * pageSize, nil
}

func newLegacyID() (string, error) {
	first, err := rand.Int(rand.Reader, big.NewInt(9))
	if err != nil {
		return "", err
	}
	identifier := make([]byte, 18)
	identifier[0] = byte('1' + first.Int64())
	for index := 1; index < len(identifier); index++ {
		digit, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		identifier[index] = byte('0' + digit.Int64())
	}
	return string(identifier), nil
}

func sameNullableString(left, right sql.NullString) bool {
	return left.Valid == right.Valid && (!left.Valid || left.String == right.String)
}

func sameNullableBool(left, right sql.NullBool) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Bool == right.Bool)
}

func sameNullableTime(left, right sql.NullTime) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Time.Equal(right.Time))
}

func unownedColumnsEqual(before, after legacyMemberLevelRow) bool {
	return before.ID == after.ID && sameNullableString(before.TenantID, after.TenantID) &&
		sameNullableTime(before.CreatedAt, after.CreatedAt) && sameNullableBool(before.HasMarket, after.HasMarket) &&
		sameNullableBool(before.ChangeCourier, after.ChangeCourier) && sameNullableString(before.PaymentIDs, after.PaymentIDs) &&
		sameNullableBool(before.Init, after.Init)
}
