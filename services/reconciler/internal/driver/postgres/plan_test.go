package postgres

import (
	"strings"
	"testing"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

func testConfig() stage.Config {
	return stage.Config{
		Environment:         stage.Environment,
		Namespace:           stage.Namespace,
		DatabaseDSN:         "postgres://bootstrap:bootstrap-secret@" + stage.DatabaseHost + ":5432/" + stage.DatabaseName + "?sslmode=verify-full&sslrootcert=" + stage.DatabaseCAPath,
		RedisPassword:       []byte("redis-secret-value"),
		TenantID:            stage.TenantID,
		TenantKey:           stage.TenantKey,
		LegacyTenantID:      stage.LegacyTenantID,
		ImportReceiptSHA256: strings.Repeat("a", 64),
	}
}

func testCredentials() Credentials {
	return Credentials{
		TenantMigratorPassword: []byte("tenant-migrator-password"),
		TenantRuntimePassword:  []byte("tenant-runtime-password-1"),
		MallMigratorPassword:   []byte("mall-migrator-password-01"),
		MallRuntimePassword:    []byte("mall-runtime-password-001"),
	}
}

func TestPlanHasExactCompatibilityAllocation(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	if len(mallLegacyViews) != 43 || len(mallSnapshots) != 7 {
		t.Fatalf("view/snapshot counts = %d/%d, want 43/7", len(mallLegacyViews), len(mallSnapshots))
	}
	summary := plan.Summary()
	if summary.Views != 46 || summary.Snapshots != 7 {
		t.Fatalf("summary = %+v, want 43 generic mall views + payment/private/audit views and seven snapshots", summary)
	}

	allSQL := flattenSQL(plan)
	if count := strings.Count(allSQL, "CREATE VIEW \"mss_m_aussibuy_biz\""); count != 45 {
		t.Fatalf("mall view count in SQL = %d, want 45", count)
	}
	if count := strings.Count(allSQL, "CREATE TABLE \"mss_m_aussibuy_biz\""); count != 8 {
		// Seven resource tables plus the snapshot audit table.
		t.Fatalf("mall table count in SQL = %d, want 8", count)
	}
	if !strings.Contains(allSQL, `CREATE VIEW "mss_t_dev_shared"."payments"`) {
		t.Fatal("tenant payment compatibility view is missing")
	}
	for _, moved := range []string{"brands", "categories", "classes", "goods_infos", "couriers", "courier_pack_rules", "courier_links"} {
		if strings.Contains(allSQL, `CREATE VIEW "mss_t_dev_shared"."`+moved+`"`) {
			t.Fatalf("tenant shared schema still exposes moved resource %s", moved)
		}
		if !strings.Contains(allSQL, `CREATE TABLE "mss_m_aussibuy_biz"."`+moved+`"`) {
			t.Fatalf("mall snapshot table %s is missing", moved)
		}
	}
}

func TestOrdersIsOnlyFixedTenantReadOnlyView(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	expectedMarker := `database_marker = 'mss-shop-isolated-dev:legacy-import:v1:` + strings.Repeat("a", 64) + `'`
	if !strings.Contains(allSQL, expectedMarker) ||
		strings.Contains(allSQL, `database_marker LIKE 'mss-shop-isolated-dev:legacy-import:v1:`) {
		t.Fatal("isolated database boundary is not bound to the exact reviewed import receipt")
	}
	for _, schema := range []string{
		"information_schema",
		"pg_catalog",
		"pg_toast",
		"public",
		"mss_t_dev_core",
		"mss_t_dev_shared",
		"mss_m_aussibuy_core",
		"mss_m_aussibuy_biz",
	} {
		if !strings.Contains(allSQL, `'`+schema+`'`) {
			t.Fatalf("isolated database schema inventory omits %s", schema)
		}
	}
	if !strings.Contains(allSQL, "schema_inventory IS DISTINCT FROM") {
		t.Fatal("isolated database schema inventory is not exhaustive")
	}
	if !strings.Contains(allSQL, `IF (SELECT count(*) FROM "public"."orders") <> 0`) ||
		!strings.Contains(allSQL, `OR (SELECT count(*) FROM "public"."order_goods") <> 0`) ||
		!strings.Contains(allSQL, "isolated legacy import must leave public.orders and public.order_goods empty") {
		t.Fatal("orders/order_goods structure-only import gate is missing")
	}
	ordersView := `CREATE VIEW "mss_m_aussibuy_biz"."orders" WITH (security_barrier=true, security_invoker=false) AS SELECT "source"."id" AS "id"`
	if !strings.Contains(allSQL, ordersView) || !strings.Contains(allSQL, `FROM "public"."orders" AS source WHERE "source"."tenant_id" = ''518729051064631297''`) {
		t.Fatal("orders is not a security-barrier view with the immutable tenant predicate")
	}
	for _, forbidden := range []string{
		`INSERT INTO "mss_m_aussibuy_biz"."orders"`,
		`CREATE TABLE "mss_m_aussibuy_biz"."orders"`,
		`DELETE FROM "public"."orders"`,
		`UPDATE "public"."orders"`,
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("orders plan contains forbidden operation %q", forbidden)
		}
	}
}

func TestPlanNeverWritesLegacySchemaAndCredentialsAreRedacted(t *testing.T) {
	t.Parallel()
	credentials := testCredentials()
	plan, err := BuildPlan(testConfig(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	for _, forbidden := range []string{
		`INSERT INTO "public".`,
		`UPDATE "public".`,
		`DELETE FROM "public".`,
		`DROP TABLE "public".`,
		`ALTER TABLE "public".`,
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("plan writes legacy schema through %q", forbidden)
		}
	}
	for _, secret := range [][]byte{
		credentials.TenantMigratorPassword,
		credentials.TenantRuntimePassword,
		credentials.MallMigratorPassword,
		credentials.MallRuntimePassword,
	} {
		if strings.Contains(allSQL, string(secret)) {
			t.Fatal("SQL plan text contains a database credential")
		}
	}
	if got := plan.Summary(); got.Batches == 0 || got.Statements == 0 {
		t.Fatalf("empty redacted summary: %+v", got)
	}
}

func TestPlanUsesLeastPrivilegeRuntimeGrants(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	for _, forbidden := range []string{
		`GRANT ALL`,
		`TO "mss_m_aussibuy_runtime" WITH GRANT OPTION`,
		`GRANT CREATE ON SCHEMA "public"`,
		`GRANT SELECT ON TABLE "public"."orders" TO "mss_m_aussibuy_runtime"`,
		`GRANT SELECT ON TABLE "mss_m_aussibuy_biz"."r1_reconcile_snapshot_audit" TO "mss_m_aussibuy_runtime"`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE "mss_t_dev_migrator" IN SCHEMA "mss_t_dev_shared"`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE "mss_m_aussibuy_migrator" IN SCHEMA "mss_m_aussibuy_biz"`,
		`GRANT EXECUTE ON FUNCTIONS TO "mss_t_dev_runtime"`,
		`GRANT EXECUTE ON FUNCTIONS TO "mss_m_aussibuy_runtime"`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA`,
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("runtime grants contain forbidden privilege %q", forbidden)
		}
	}
	for _, expected := range []string{
		"compatibility owner has unexpected direct or incoming default ACL state",
		"MSS core schema is not empty; a versioned post-migration manifest is required",
		`FOR ROLE "mss_t_dev_migrator" REVOKE ALL ON FUNCTIONS FROM PUBLIC`,
		`FOR ROLE "mss_m_aussibuy_migrator" REVOKE ALL ON FUNCTIONS FROM PUBLIC`,
		"unsafe global function default ACL",
	} {
		if !strings.Contains(allSQL, expected) {
			t.Fatalf("managed default-privilege contract is missing %q", expected)
		}
	}
	if !strings.Contains(allSQL, `GRANT SELECT ON TABLE "mss_m_aussibuy_biz"."brands" TO "mss_m_aussibuy_runtime"`) {
		t.Fatal("mall runtime lacks explicit snapshot read grant")
	}
	for _, coreSchema := range []string{"mss_t_dev_core", "mss_m_aussibuy_core"} {
		if !strings.Contains(allSQL, `IN SCHEMA "`+coreSchema+`" GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES`) {
			t.Fatalf("MSS core owner default privileges are missing for %s", coreSchema)
		}
	}
}

func TestMemberLevelsProjectionAuditIsFixedAggregateOnlyAndLeastPrivilege(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	fragment := requiredStatementSQL(
		t,
		plan,
		"reconcile-managed-view-mss_m_aussibuy_biz-r1_member_levels_projection_audit",
	)
	for _, expected := range []string{
		`WITH (security_barrier=true, security_invoker=false)`,
		`FROM "public"."member_levels" AS source`,
		`source."tenant_id" = ''518729051064631297''`,
		`FROM "mss_m_aussibuy_biz"."member_levels" AS business`,
		`source_minus_business AS`,
		`business_minus_source AS`,
		`public_member_levels_rows`,
		`business_member_levels_rows`,
		`difference_rows`,
		`cross_tenant_rows`,
		`flagged_default_rows`,
		`enabled_default_rows`,
		`invalid_default_rows`,
		`GROUP BY name HAVING count(*) > 1`,
		`duplicate_active_name_groups`,
		`public_orders_rows`,
		`business_orders_rows`,
		`public_order_goods_rows`,
		`business_order_goods_rows`,
		`ALTER VIEW "mss_m_aussibuy_biz"."r1_member_levels_projection_audit" OWNER TO "mss_m_aussibuy_compat_owner"`,
		`REVOKE ALL ON TABLE "mss_m_aussibuy_biz"."r1_member_levels_projection_audit" FROM PUBLIC`,
		`GRANT SELECT ON TABLE "mss_m_aussibuy_biz"."r1_member_levels_projection_audit" TO "mss_m_aussibuy_runtime"`,
	} {
		if !strings.Contains(fragment, expected) {
			t.Fatalf("member-levels projection audit is missing %q", expected)
		}
	}
	// reconcileManagedView compiles the reviewed SELECT twice: once for the
	// target view and once for the pg_temp structural comparison view. Each
	// copy must contain both directions of the EXCEPT ALL proof.
	if strings.Count(fragment, "EXCEPT ALL") != 4 {
		t.Fatalf("projection audit EXCEPT ALL count = %d, want exactly 4", strings.Count(fragment, "EXCEPT ALL"))
	}
	if len(legacySourceColumns["member_levels"]) != 12 {
		t.Fatalf("compiled member-levels projection has %d columns, want 12", len(legacySourceColumns["member_levels"]))
	}
	for _, column := range legacySourceColumns["member_levels"] {
		for _, alias := range []string{"source", "business", "source_rows", "business_rows"} {
			if !strings.Contains(fragment, `"`+alias+`"."`+column+`"`) {
				t.Fatalf("12-column audit projection omits %s.%s", alias, column)
			}
		}
	}
	for _, forbidden := range []string{
		`security_invoker=true`,
		`GRANT SELECT ON TABLE "public"."member_levels" TO "mss_m_aussibuy_runtime"`,
		`GRANT SELECT ON TABLE "public"."orders" TO "mss_m_aussibuy_runtime"`,
		`GRANT SELECT ON TABLE "public"."order_goods" TO "mss_m_aussibuy_runtime"`,
		`GRANT INSERT ON TABLE "mss_m_aussibuy_biz"."r1_member_levels_projection_audit"`,
		`GRANT UPDATE ON TABLE "mss_m_aussibuy_biz"."r1_member_levels_projection_audit"`,
		`GRANT DELETE ON TABLE "mss_m_aussibuy_biz"."r1_member_levels_projection_audit"`,
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("projection audit contains forbidden privilege or view option %q", forbidden)
		}
	}
}

func TestInheritedViewsUseActiveOwningTenantPredicates(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	for _, relation := range []string{"coupon_links", "goods_assembles", "goods_shipping_warehouses", "order_goods"} {
		marker := `CREATE VIEW "mss_m_aussibuy_biz"."` + relation + `"`
		start := strings.Index(allSQL, marker)
		if start < 0 {
			t.Fatalf("inherited view %s is missing", relation)
		}
		end := strings.Index(allSQL[start:], "\n-- ")
		fragment := allSQL[start:]
		if end >= 0 {
			fragment = fragment[:end]
		}
		if !strings.Contains(fragment, "EXISTS (SELECT 1 FROM") || !strings.Contains(fragment, `owner."tenant_id" = ''518729051064631297''`) {
			t.Fatalf("inherited view %s has no fixed parent tenant predicate: %s", relation, fragment)
		}
		if !strings.Contains(fragment, `owner."deleted_at" IS NULL`) {
			t.Fatalf("inherited view %s exposes a child of a soft-deleted owner", relation)
		}
	}
}

func TestSensitiveCompatibilityViewsNullClassifiedColumns(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	classified := map[string][]string{
		"consignees":       {"id_card", "id_card_front", "id_card_back"},
		"courier_installs": {"app_key", "app_secret", "param0", "param1"},
		"gold_withdraws":   {"bank_account", "voucher"},
		"members":          {"password_hash", "salt", "rest_password_hash", "open_id", "union_id"},
		"payment_installs": {"app_key", "app_secret"},
		"payment_orders":   {"token", "callback"},
		"system_configs":   {"metadata"},
	}
	for relation, columns := range classified {
		fragment := requiredStatementSQL(
			t,
			plan,
			"reconcile-managed-view-mss_m_aussibuy_biz-"+relation,
		)
		if strings.Contains(fragment, "SELECT source.*") {
			t.Fatalf("classified view %s exposes source.*: %s", relation, fragment)
		}
		for _, column := range columns {
			expected := `(NULL::"public"."` + relation + `")."` + column + `" AS "` + column + `"`
			if !strings.Contains(fragment, expected) || strings.Contains(fragment, `"source"."`+column+`" AS "`+column+`"`) {
				t.Fatalf("classified view %s does not redact %s", relation, column)
			}
		}
	}
}

func TestLegacySourceColumnsAreLockedValidatedAndExplicitlyProjected(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	if strings.Contains(allSQL, "SELECT source.*") || strings.Contains(allSQL, "SELECT \"source\".*") {
		t.Fatal("legacy compatibility plan contains an open-ended source projection")
	}
	if count := strings.Count(allSQL, "-- validate-legacy-source-columns-"); count != 51 {
		t.Fatalf("legacy ordered-column validations = %d, want 51", count)
	}
	for _, expected := range []string{
		"LOCK TABLE \"public\".\"activities\"",
		"attribute.attname::text ORDER BY attribute.attnum",
		"legacy source relation % has an unexpected ordered column or catalog fingerprint",
		"pg_catalog.sha256(pg_catalog.convert_to(",
		"type_namespace.nspname <> 'pg_catalog'",
		"managed compatibility view dependency binding drifted",
		`CREATE VIEW "mss_t_dev_shared"."payments" WITH (security_barrier=true, security_invoker=false) AS SELECT "source"."id" AS "id"`,
		`INSERT INTO "mss_m_aussibuy_biz"."brands" ("id", "created_at"`,
	} {
		if !strings.Contains(allSQL, expected) {
			t.Fatalf("exact legacy source shape contract is missing %q", expected)
		}
	}
}

func TestManagedRolesAndSchemasFailClosed(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	for _, expected := range []string{
		"COMMENT ON ROLE",
		"shobj_description",
		"pg_auth_members",
		"unsafe attributes",
		"COMMENT ON SCHEMA",
		"obj_description",
		"aclexplode",
		"unexpected ACL grantees",
		"mss-shop-reconciler:mss-shop-dev:role:mss_t_dev_runtime",
		"mss-shop-reconciler:mss-shop-dev:compat-owner:mss_t_dev_compat_owner",
		"mss-shop-reconciler:mss-shop-dev:compat-owner:mss_m_aussibuy_compat_owner",
		"mss-shop-reconciler:mss-shop-dev:compat-schema:mss_m_aussibuy_biz",
	} {
		if !strings.Contains(allSQL, expected) {
			t.Fatalf("managed-object guard is missing %q", expected)
		}
	}
	if strings.Contains(allSQL, `ALTER SCHEMA "mss_m_aussibuy_biz" OWNER`) {
		t.Fatal("plan can take over an existing schema owner")
	}
	for _, role := range []string{"mss_t_dev_compat_owner", "mss_m_aussibuy_compat_owner"} {
		statement := ensureCompatibilityOwner(role)
		for _, expected := range []string{
			"NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS",
			"rolpassword IS NOT NULL",
			"pg_auth_members",
			"pg_db_role_setting",
		} {
			if !strings.Contains(statement.SQL, expected) {
				t.Fatalf("compatibility owner %s guard is missing %q", role, expected)
			}
		}
		if strings.Contains(statement.SQL, "PASSWORD") {
			t.Fatalf("NOLOGIN compatibility owner %s receives a password", role)
		}
	}
}

func TestManagedRolePasswordIsWrittenOnlyOnTransactionalCreation(t *testing.T) {
	t.Parallel()
	statements := ensureLoginRole("mss_t_dev_runtime", []byte("generated-database-password"))
	if len(statements) != 4 {
		t.Fatalf("managed role statements=%d, want 4", len(statements))
	}
	if !strings.Contains(statements[1].SQL, "set_config('r1shop.reconciler_role_action', 'created', true)") {
		t.Fatal("managed role creation does not record its transaction-local creation state")
	}
	if !strings.Contains(statements[2].SQL, "current_setting('r1shop.reconciler_role_action') = 'created'") {
		t.Fatal("managed role password write is not guarded by transaction-local creation state")
	}
	if strings.Count(statements[2].SQL, "ALTER ROLE") != 1 || !statements[2].Sensitive {
		t.Fatal("managed role password statement has an unsafe shape")
	}
}

func TestSnapshotsShareRepeatableReadAndVerifyRetryAudit(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Batches) != 1 || len(plan.Batches[0].Statements) == 0 ||
		plan.Batches[0].Statements[0].SQL != "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ" {
		t.Fatal("the complete PostgreSQL plan is not one repeatable-read transaction batch")
	}
	copies := 0
	for _, statement := range plan.Batches[0].Statements {
		if strings.HasPrefix(statement.Name, "copy-and-audit-snapshot-") {
			copies++
		}
	}
	if copies != 7 {
		t.Fatalf("complete transaction has %d snapshot copies, want 7", copies)
	}
	allSQL := flattenSQL(plan)
	for _, expected := range []string{
		"recorded_source_schema",
		"recorded_source_relation",
		"recorded_plan_version",
		"recorded_source_rows",
		"recorded_target_rows",
		"recorded_source_row_hash",
		"recorded_target_row_hash",
		"source_row_hash <> recorded_source_row_hash",
		"target_row_hash <> recorded_target_row_hash",
		"\"source\"::text",
		"\"target\"::text",
		"octet_length(\"source\"::text)::text || ':' || \"source\"::text",
		"PostgreSQL composite output does not preserve JSON lexical differences",
		"legacy-global-snapshot-v4-explicit-ddl",
		"link_rows <> 325",
		"unmatched_link_subjects <> 5",
		"distinct_mixable_ids <> 22",
		"orphan_mixable_ids <> 1",
		"jsonb_typeof(pack_rule::jsonb) <> 'array'",
	} {
		if !strings.Contains(allSQL, expected) {
			t.Fatalf("snapshot audit/profile is missing %q", expected)
		}
	}
}

func TestCompatibilityObjectsUseNoLoginLeastPrivilegeOwners(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	for _, forbidden := range []string{
		`GRANT USAGE ON SCHEMA "public" TO "mss_t_dev_runtime"`,
		`GRANT USAGE ON SCHEMA "public" TO "mss_m_aussibuy_runtime"`,
		`GRANT SELECT ON TABLE "public"."orders" TO "mss_m_aussibuy_runtime"`,
		`GRANT SELECT ON TABLE "public"."payments" TO "mss_t_dev_runtime"`,
		`REVOKE ALL ON SCHEMA "public"`,
		`REVOKE ALL ON TABLE "public".`,
		`CREATE ROLE "r1_t_dev_compat_owner"`,
		`CREATE ROLE "r1_m_aussibuy_compat_owner"`,
		`GRANT CONNECT ON DATABASE "mss_shop_dev" TO "mss_t_dev_compat_owner"`,
		`GRANT CONNECT ON DATABASE "mss_shop_dev" TO "mss_m_aussibuy_compat_owner"`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE "mss_t_dev_compat_owner"`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE "mss_m_aussibuy_compat_owner"`,
		`WITH GRANT OPTION`,
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("compatibility owner contract contains forbidden privilege path %q", forbidden)
		}
	}
	for _, expected := range []string{
		"mss-shop-reconciler:mss-shop-dev:compat-schema:mss_t_dev_shared",
		"mss-shop-reconciler:mss-shop-dev:compat-schema:mss_m_aussibuy_biz",
		`GRANT USAGE ON SCHEMA "public" TO "mss_t_dev_compat_owner"`,
		`GRANT USAGE ON SCHEMA "public" TO "mss_m_aussibuy_compat_owner"`,
		`GRANT SELECT ON TABLE "public"."payments" TO "mss_t_dev_compat_owner"`,
		`GRANT SELECT ON TABLE "public"."orders" TO "mss_m_aussibuy_compat_owner"`,
		`ALTER VIEW "mss_t_dev_shared"."payments" OWNER TO "mss_t_dev_compat_owner"`,
		`ALTER VIEW "mss_m_aussibuy_biz"."orders" OWNER TO "mss_m_aussibuy_compat_owner"`,
		`ALTER TABLE "mss_m_aussibuy_biz"."brands" OWNER TO "mss_m_aussibuy_compat_owner"`,
		`ALTER TABLE "mss_m_aussibuy_biz"."r1_reconcile_snapshot_audit" OWNER TO "mss_m_aussibuy_compat_owner"`,
		"relowner = owner_oid",
		"security_invoker=false",
		"pg_get_viewdef(target_oid, true)",
		"managed compatibility view has executable catalog drift",
		"validate-legacy-source-public-acls",
		"validate-legacy-source-compatibility-acls",
		"privilege.grantee = 0",
		"has_any_column_privilege",
		"public SECURITY DEFINER routine",
		"read-only schema ownership or runtime boundary is unsafe",
		"compatibility owner crosses its database or schema realm",
		"compatibility owner has a direct ACL outside its exact realm",
		"has_database_privilege('mss_t_dev_compat_owner', database.oid, 'CREATE')",
		"has_database_privilege('mss_m_aussibuy_compat_owner', database.oid, 'TEMP')",
		"pg_catalog.pg_parameter_acl",
	} {
		if !strings.Contains(allSQL, expected) {
			t.Fatalf("least-privilege compatibility owner contract is missing %q", expected)
		}
	}
	if count := strings.Count(allSQL, `ALTER VIEW "mss_`); count != 46 {
		t.Fatalf("compatibility view ownership transfers = %d, want 46", count)
	}
	if count := strings.Count(allSQL, `ALTER TABLE "mss_m_aussibuy_biz"`); count != 8 {
		t.Fatalf("snapshot/audit ownership transfers = %d, want 8", count)
	}
	if got := mallCompatibilitySourceNames(); len(got) != 43 {
		t.Fatalf("mall compatibility owner source dependencies = %d, want exact 43", len(got))
	} else {
		for _, relation := range got {
			grant := `GRANT SELECT ON TABLE "public"."` + relation + `" TO "mss_m_aussibuy_compat_owner"`
			if count := strings.Count(allSQL, grant); count != 1 {
				t.Fatalf("mall compatibility source grant %s count = %d, want 1", relation, count)
			}
		}
	}
	for _, relation := range snapshotNames() {
		grant := `GRANT SELECT ON TABLE "public"."` + relation + `" TO "mss_m_aussibuy_compat_owner"`
		if strings.Contains(allSQL, grant) {
			t.Fatalf("snapshot-only source %s is exposed to the persistent view owner", relation)
		}
	}
}

func TestIsolatedVanillaPostgresHasNoTimescaleCapabilityException(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	for _, expected := range []string{
		"isolated PostgreSQL database boundary drifted",
		"isolated PostgreSQL role inventory or bootstrap membership drifted",
		"compatibility-owned read-only schema object inventory drifted",
		"managed application role can execute a routine outside its core realm",
		"pg_catalog.pg_parameter_acl",
	} {
		if !strings.Contains(allSQL, expected) {
			t.Fatalf("isolated PostgreSQL privilege boundary is missing %q", expected)
		}
	}
	for _, forbidden := range []string{
		"timescaledb",
		"reviewed_timescale",
		"cagg_migrate_update_watermark",
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("isolated PostgreSQL plan retains a Timescale exception %q", forbidden)
		}
	}
}

func TestSnapshotsUseCompiledDDLAndFullSourceFingerprints(t *testing.T) {
	t.Parallel()
	plan, err := BuildPlan(testConfig(), testCredentials())
	if err != nil {
		t.Fatal(err)
	}
	allSQL := flattenSQL(plan)
	for _, forbidden := range []string{
		"LIKE \"public\".",
		"INCLUDING ALL",
		"CREATE TABLE IF NOT EXISTS",
		"ALTER TABLE \"public\".",
	} {
		if strings.Contains(allSQL, forbidden) {
			t.Fatalf("snapshot plan retains unsafe inferred/adoptive DDL %q", forbidden)
		}
	}
	for _, expected := range []string{
		`CREATE TABLE "mss_m_aussibuy_biz"."brands" ("id" character varying(20) NOT NULL`,
		`CREATE TABLE "mss_m_aussibuy_biz"."courier_links" ("id" text NOT NULL, "link_id" text NOT NULL, "left_rule_id" text NOT NULL`,
		`PRIMARY KEY ("id", "link_id", "left_rule_id")`,
		`CREATE INDEX "r1_snapshot_goods_infos_parent_category_id_idx"`,
		"legacy snapshot source % has an unexpected table/owner/storage shape",
		"column fingerprint drifted",
		"index fingerprint drifted",
		"constraint fingerprint drifted",
		"sequence drift",
		"relowner = bootstrap_oid",
		"owner_is_compatibility",
		`ALTER TABLE "mss_m_aussibuy_biz"."courier_links" OWNER TO "mss_m_aussibuy_compat_owner"`,
		"relreplident = 'd'",
		"access_method.amname = 'heap'",
	} {
		if !strings.Contains(allSQL, expected) {
			t.Fatalf("compiled snapshot/source contract is missing %q", expected)
		}
	}
}

func flattenSQL(plan Plan) string {
	var builder strings.Builder
	for _, batch := range plan.Batches {
		for _, statement := range batch.Statements {
			builder.WriteString("-- ")
			builder.WriteString(statement.Name)
			builder.WriteByte('\n')
			builder.WriteString(statement.SQL)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func requiredStatementSQL(t *testing.T, plan Plan, name string) string {
	t.Helper()
	var matches []string
	for _, batch := range plan.Batches {
		for _, statement := range batch.Statements {
			if statement.Name == name {
				matches = append(matches, statement.SQL)
			}
		}
	}
	if len(matches) != 1 {
		t.Fatalf("statement %q count = %d, want exactly 1", name, len(matches))
	}
	return matches[0]
}
