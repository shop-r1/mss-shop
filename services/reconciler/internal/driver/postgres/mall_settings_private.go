package postgres

import (
	"fmt"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

// buildMallSettingsPrivateView is the narrowly scoped exception to the
// metadata-redacted generic system_configs view. It exposes raw metadata only
// after fixing tenant, row name and active-row state inside a security-barrier
// view. reconcileManagedView grants the runtime SELECT only.
func buildMallSettingsPrivateView(
	legacyTenantID string,
	schemas stage.Schemas,
	roles stage.Roles,
) Statement {
	selectSQL := fmt.Sprintf(
		"SELECT %s FROM %s AS source WHERE source.%s = %s AND source.%s = %s AND source.%s IS NULL",
		explicitProjection("system_configs", nil),
		qualified(stage.LegacySchema, "system_configs"),
		quoteIdentifier("tenant_id"),
		quoteLiteral(legacyTenantID),
		quoteIdentifier("name"),
		quoteLiteral("appConfig"),
		quoteIdentifier("deleted_at"),
	)
	return reconcileManagedView(
		schemas.MallBusiness,
		mallSettingsPrivateView,
		selectSQL,
		roles.MallCompatibilityOwner,
		roles.MallRuntime,
	)
}
