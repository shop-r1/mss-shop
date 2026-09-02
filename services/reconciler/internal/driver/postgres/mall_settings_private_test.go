package postgres

import (
	"strings"
	"testing"
)

func TestMallSettingsPrivateViewIsFixedTenantReadOnlyAndDoesNotWidenGenericView(t *testing.T) {
	t.Parallel()

	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	fragment := requiredStatementSQL(
		t,
		plan,
		"reconcile-managed-view-mss_m_aussibuy_biz-r1_mall_settings_system_configs",
	)
	for _, expected := range []string{
		`WITH (security_barrier=true, security_invoker=false)`,
		`FROM "public"."system_configs" AS source`,
		`source."tenant_id" = ''518729051064631297''`,
		`source."name" = ''appConfig''`,
		`source."deleted_at" IS NULL`,
		`"source"."metadata" AS "metadata"`,
		`ALTER VIEW "mss_m_aussibuy_biz"."r1_mall_settings_system_configs" OWNER TO "mss_m_aussibuy_compat_owner"`,
		`REVOKE ALL ON TABLE "mss_m_aussibuy_biz"."r1_mall_settings_system_configs" FROM PUBLIC`,
		`GRANT SELECT ON TABLE "mss_m_aussibuy_biz"."r1_mall_settings_system_configs" TO "mss_m_aussibuy_runtime"`,
	} {
		if !strings.Contains(fragment, expected) {
			t.Fatalf("private mall-settings view is missing %q", expected)
		}
	}

	genericFragment := requiredStatementSQL(
		t,
		plan,
		"reconcile-managed-view-mss_m_aussibuy_biz-system_configs",
	)
	if !strings.Contains(genericFragment, `(NULL::"public"."system_configs")."metadata" AS "metadata"`) ||
		strings.Contains(genericFragment, `"source"."metadata" AS "metadata"`) {
		t.Fatal("generic system_configs metadata redaction was widened")
	}

	for _, forbidden := range []string{
		`GRANT INSERT ON TABLE "mss_m_aussibuy_biz"."r1_mall_settings_system_configs"`,
		`GRANT UPDATE ON TABLE "mss_m_aussibuy_biz"."r1_mall_settings_system_configs"`,
		`GRANT DELETE ON TABLE "mss_m_aussibuy_biz"."r1_mall_settings_system_configs"`,
		`GRANT INSERT ON TABLE "mss_m_aussibuy_biz"."system_configs"`,
		`GRANT UPDATE ON TABLE "mss_m_aussibuy_biz"."system_configs"`,
		`GRANT DELETE ON TABLE "mss_m_aussibuy_biz"."system_configs"`,
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("mall-settings projection contains forbidden DML grant %q", forbidden)
		}
	}
}
