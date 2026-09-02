// Package postgres builds and applies the fixed PostgreSQL reconciliation plan
// for the isolated mss-shop-dev tenant. Plans contain no caller-selected schema and
// never write the legacy public relations.
package postgres

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

var ErrInvalidCredentials = errors.New("invalid reconciler-generated database credentials")

type Credentials struct {
	TenantMigratorPassword []byte
	TenantRuntimePassword  []byte
	MallMigratorPassword   []byte
	MallRuntimePassword    []byte
}

func (c Credentials) Validate() error {
	for _, value := range [][]byte{
		c.TenantMigratorPassword,
		c.TenantRuntimePassword,
		c.MallMigratorPassword,
		c.MallRuntimePassword,
	} {
		if len(value) < 24 {
			return ErrInvalidCredentials
		}
	}
	return nil
}

// Statement deliberately has no String method. Arguments may contain a role
// password and must never be included in plan diagnostics.
type Statement struct {
	Name      string
	SQL       string
	Arguments []any
	Sensitive bool
}

type Batch struct {
	Name       string
	Statements []Statement
}

type Plan struct {
	Batches []Batch
}

type Summary struct {
	Batches    int
	Statements int
	Views      int
	Snapshots  int
}

func (p Plan) Summary() Summary {
	summary := Summary{
		Batches:   len(p.Batches),
		Views:     expectedMallViewCount + 3,
		Snapshots: expectedMallSnapshotCount,
	}
	for _, batch := range p.Batches {
		summary.Statements += len(batch.Statements)
	}
	return summary
}

func BuildPlan(config stage.Config, credentials Credentials) (Plan, error) {
	if err := config.Validate(); err != nil {
		return Plan{}, err
	}
	if err := credentials.Validate(); err != nil {
		return Plan{}, err
	}
	if len(mallLegacyViews) != expectedMallViewCount || len(mallSnapshots) != expectedMallSnapshotCount {
		return Plan{}, errors.New("compiled legacy reconciliation inventory is inconsistent")
	}
	if len(legacySourceColumns["member_levels"]) != expectedMemberLevelsProjectionColumnCount {
		return Plan{}, errors.New("compiled member levels projection inventory is inconsistent")
	}
	if err := validateCompiledSourceColumns(); err != nil {
		return Plan{}, err
	}

	schemas := config.Schemas()
	roles := config.Roles()
	batches := []Batch{
		buildFoundationBatch(schemas, roles, credentials),
		buildLegacySourceBoundaryBatch(roles),
		buildTenantSharedBatch(schemas, roles),
		buildMallViewsBatch(config.LegacyTenantID, schemas, roles),
		buildMemberLevelsProjectionAuditBatch(config.LegacyTenantID, schemas, roles),
		buildSnapshotAuditBatch(schemas, roles),
	}
	snapshotBatch := Batch{Name: "tenant-global-snapshots"}
	for _, resource := range mallSnapshots {
		snapshotBatch.Statements = append(
			snapshotBatch.Statements,
			buildSnapshotBatch(resource, schemas, roles.MallCompatibilityOwner, roles.MallRuntime).Statements...,
		)
	}
	batches = append(batches,
		snapshotBatch,
		buildSnapshotRelationsBatch(schemas),
		buildRuntimeGrantsBatch(schemas, roles),
	)
	statements := []Statement{
		plain("complete-plan-repeatable-read", "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"),
		validateBootstrapRole(),
		validateIsolatedRoleInventory(roles),
		plain("fix-session-replication-role", "SET LOCAL session_replication_role = 'origin'"),
		validateEventTriggerBoundary(config.ImportReceiptSHA256, schemas),
		plain("fix-bootstrap-search-path", "SET LOCAL search_path = pg_catalog"),
		plain("fix-bootstrap-table-access-method", "SET LOCAL default_table_access_method = 'heap'"),
		plain("fix-bootstrap-tablespace", "SET LOCAL default_tablespace = ''"),
		plain("fix-bootstrap-time-zone", "SET LOCAL TimeZone = 'UTC'"),
		plain("fix-bootstrap-date-style", "SET LOCAL DateStyle = 'ISO, YMD'"),
		plain("fix-bootstrap-interval-style", "SET LOCAL IntervalStyle = 'postgres'"),
		plain("fix-bootstrap-bytea-output", "SET LOCAL bytea_output = 'hex'"),
		plain("fix-bootstrap-float-output", "SET LOCAL extra_float_digits = 3"),
		plain("fix-bootstrap-password-encryption", "SET LOCAL password_encryption = 'scram-sha-256'"),
		plain("lock-legacy-source-shapes", "LOCK TABLE "+joinQualifiedAllLegacyResources()+" IN ACCESS SHARE MODE"),
		plain("verify-composite-json-lexical-output", `DO $r1_json_lexical$
BEGIN
  IF ROW('{"a":1, "b":2}'::json)::text = ROW('{"a":1,"b":2}'::json)::text
     OR ROW('{"a":1,"b":2}'::json)::text = ROW('{"b":2,"a":1}'::json)::text THEN
    RAISE EXCEPTION 'PostgreSQL composite output does not preserve JSON lexical differences';
  END IF;
END
$r1_json_lexical$`),
	}
	statements = append(statements, validateLegacySourceColumnStatements()...)
	statements = append(statements, verifyImportedOrderTablesEmpty())
	for _, batch := range batches {
		statements = append(statements, batch.Statements...)
	}
	return Plan{Batches: []Batch{{
		Name:       "complete-mss-shop-dev-reconciliation",
		Statements: statements,
	}}}, nil
}

func buildFoundationBatch(schemas stage.Schemas, roles stage.Roles, credentials Credentials) Batch {
	statements := make([]Statement, 0, 48)
	for _, role := range []struct {
		name     string
		password []byte
	}{
		{name: roles.TenantMigrator, password: credentials.TenantMigratorPassword},
		{name: roles.TenantRuntime, password: credentials.TenantRuntimePassword},
		{name: roles.MallMigrator, password: credentials.MallMigratorPassword},
		{name: roles.MallRuntime, password: credentials.MallRuntimePassword},
	} {
		statements = append(statements, ensureLoginRole(role.name, role.password)...)
	}
	for _, owner := range []string{roles.TenantCompatibilityOwner, roles.MallCompatibilityOwner} {
		statements = append(statements, ensureCompatibilityOwner(owner))
	}
	coreSchemas := []struct {
		name    string
		owner   string
		runtime string
	}{
		{name: schemas.TenantCore, owner: roles.TenantMigrator, runtime: roles.TenantRuntime},
		{name: schemas.MallCore, owner: roles.MallMigrator, runtime: roles.MallRuntime},
	}
	for _, schema := range coreSchemas {
		statements = append(statements, ensureManagedSchema(schema.name, schema.owner)...)
	}
	readOnlySchemas := []struct {
		name     string
		owner    string
		runtime  string
		migrator string
	}{
		{name: schemas.TenantShared, owner: roles.TenantCompatibilityOwner, runtime: roles.TenantRuntime, migrator: roles.TenantMigrator},
		{name: schemas.MallBusiness, owner: roles.MallCompatibilityOwner, runtime: roles.MallRuntime, migrator: roles.MallMigrator},
	}
	for _, schema := range readOnlySchemas {
		statements = append(statements, ensureCompatibilityOwnedSchema(schema.name, schema.owner)...)
	}

	for _, role := range []string{roles.TenantMigrator, roles.TenantRuntime, roles.MallMigrator, roles.MallRuntime} {
		statements = append(statements, plain("connect-"+role, fmt.Sprintf(
			"GRANT CONNECT ON DATABASE %s TO %s",
			quoteIdentifier(stage.DatabaseName), quoteIdentifier(role),
		)))
	}
	statements = append(statements,
		plain("tenant-runtime-core-usage", grantSchemaUsage(schemas.TenantCore, roles.TenantRuntime)),
		plain("tenant-runtime-shared-usage", grantSchemaUsage(schemas.TenantShared, roles.TenantRuntime)),
		plain("mall-runtime-core-usage", grantSchemaUsage(schemas.MallCore, roles.MallRuntime)),
		plain("mall-runtime-business-usage", grantSchemaUsage(schemas.MallBusiness, roles.MallRuntime)),
		plain("tenant-default-table-privileges", defaultTablePrivileges(roles.TenantMigrator, schemas.TenantCore, roles.TenantRuntime)),
		plain("tenant-default-sequence-privileges", defaultSequencePrivileges(roles.TenantMigrator, schemas.TenantCore, roles.TenantRuntime)),
		plain("tenant-default-public-functions-readonly", revokeDefaultPublicFunctionPrivileges(roles.TenantMigrator)),
		plain("mall-default-table-privileges", defaultTablePrivileges(roles.MallMigrator, schemas.MallCore, roles.MallRuntime)),
		plain("mall-default-sequence-privileges", defaultSequencePrivileges(roles.MallMigrator, schemas.MallCore, roles.MallRuntime)),
		plain("mall-default-public-functions-readonly", revokeDefaultPublicFunctionPrivileges(roles.MallMigrator)),
	)
	for _, schema := range coreSchemas {
		statements = append(statements, validateManagedSchemaACL(schema.name, schema.owner, schema.runtime))
		statements = append(statements, validateManagedDefaultACL(schema.owner, schema.name, schema.runtime))
	}
	for _, schema := range readOnlySchemas {
		statements = append(statements, validateCompatibilityOwnedSchemaACL(schema.name, schema.owner, schema.migrator, schema.runtime))
		statements = append(statements, validateCompatibilityOwnerDefaultACL(schema.name, schema.owner))
	}
	return Batch{Name: "roles-and-schemas", Statements: statements}
}

func validateBootstrapRole() Statement {
	return plain("validate-trusted-bootstrap-role", `DO $r1_bootstrap_role$
DECLARE
  bootstrap_oid oid;
BEGIN
  SELECT oid INTO bootstrap_oid
    FROM pg_catalog.pg_roles
    WHERE rolname = current_user
      AND rolsuper
      AND rolvaliduntil IS NULL;
  IF bootstrap_oid IS NULL OR session_user <> current_user THEN
    RAISE EXCEPTION 'database reconciliation requires one non-expiring superuser session identity';
  END IF;
  IF EXISTS (
    SELECT 1 FROM pg_catalog.pg_db_role_setting WHERE setrole = bootstrap_oid
  ) THEN
    RAISE EXCEPTION 'database reconciliation bootstrap role has database-specific settings';
  END IF;
END
$r1_bootstrap_role$`)
}

func validateIsolatedRoleInventory(roles stage.Roles) Statement {
	expectedManagedRoles := []string{
		roles.TenantMigrator,
		roles.TenantRuntime,
		roles.TenantCompatibilityOwner,
		roles.MallMigrator,
		roles.MallRuntime,
		roles.MallCompatibilityOwner,
	}
	sort.Strings(expectedManagedRoles)
	return plain("validate-isolated-role-inventory", fmt.Sprintf(`DO $mss_role_inventory$
DECLARE
  bootstrap_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user);
  expected_managed_roles text[] := %s;
BEGIN
  IF EXISTS (
       SELECT 1
       FROM pg_catalog.pg_roles AS role_record
       WHERE role_record.rolname <> current_user
         AND role_record.rolname !~ '^pg_'
         AND role_record.rolname <> ALL (expected_managed_roles)
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_auth_members AS membership
       WHERE membership.member = bootstrap_oid OR membership.roleid = bootstrap_oid
     ) THEN
    RAISE EXCEPTION 'isolated PostgreSQL role inventory or bootstrap membership drifted';
  END IF;
END
$mss_role_inventory$`, sqlTextArray(expectedManagedRoles)))
}

func validateEventTriggerBoundary(importReceiptSHA256 string, schemas stage.Schemas) Statement {
	expectedImportMarker := "mss-shop-isolated-dev:legacy-import:v1:" + importReceiptSHA256
	baseSchemas := []string{"information_schema", "pg_catalog", "pg_toast", stage.LegacySchema}
	managedSchemas := append([]string(nil), baseSchemas...)
	managedSchemas = append(
		managedSchemas,
		schemas.TenantCore,
		schemas.TenantShared,
		schemas.MallCore,
		schemas.MallBusiness,
	)
	sort.Strings(baseSchemas)
	sort.Strings(managedSchemas)
	return plain("validate-isolated-database-boundary", fmt.Sprintf(`DO $mss_isolated_database$
DECLARE
  database_marker text;
  schema_inventory text[];
  bootstrap_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user);
BEGIN
  SELECT pg_catalog.shobj_description(database.oid, 'pg_database')
    INTO database_marker
    FROM pg_catalog.pg_database AS database
    WHERE database.datname = current_database();

  SELECT array_agg(namespace.nspname::text ORDER BY namespace.nspname)
    INTO schema_inventory
    FROM pg_catalog.pg_namespace AS namespace;

  IF session_user <> current_user
     OR current_setting('event_triggers') <> 'off'
     OR current_setting('session_replication_role') <> 'origin'
     OR NOT EXISTS (
       SELECT 1 FROM pg_catalog.pg_database
       WHERE datname = current_database() AND datdba = bootstrap_oid
     )
     OR NOT (
       (current_database() LIKE 'r1shop_reconciler_contract_%%'
        AND database_marker LIKE 'mss-shop-disposable-contract:%%')
       OR (current_database() = %s AND database_marker = %s)
     )
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_event_trigger)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_publication)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_subscription)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_db_role_setting)
     OR pg_catalog.has_database_privilege('public', current_database(), 'CONNECT')
     OR pg_catalog.has_database_privilege('public', current_database(), 'CREATE')
     OR pg_catalog.has_database_privilege('public', current_database(), 'TEMP')
     OR (SELECT array_agg(
                  extension.extname || ':' || extension.extversion || ':' || namespace.nspname || ':'
                  || (extension.extowner = bootstrap_oid)::text
                  ORDER BY extension.extname
                )
           FROM pg_catalog.pg_extension AS extension
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = extension.extnamespace)
          IS DISTINCT FROM ARRAY['plpgsql:1.0:pg_catalog:true']::text[]
     OR (
       schema_inventory IS DISTINCT FROM %s
       AND schema_inventory IS DISTINCT FROM %s
     ) THEN
    RAISE EXCEPTION 'isolated PostgreSQL database boundary drifted';
  END IF;
END
$mss_isolated_database$`,
		quoteLiteral(stage.DatabaseName),
		quoteLiteral(expectedImportMarker),
		sqlTextArray(baseSchemas),
		sqlTextArray(managedSchemas),
	))
}

func verifyImportedOrderTablesEmpty() Statement {
	return plain("verify-imported-order-tables-empty", `DO $mss_orders_empty$
BEGIN
  IF (SELECT count(*) FROM "public"."orders") <> 0
     OR (SELECT count(*) FROM "public"."order_goods") <> 0 THEN
    RAISE EXCEPTION 'isolated legacy import must leave public.orders and public.order_goods empty';
  END IF;
END
$mss_orders_empty$`)
}

func ensureLoginRole(role string, password []byte) []Statement {
	setting := "r1shop.reconciler_role_password"
	actionSetting := "r1shop.reconciler_role_action"
	roleLiteral := quoteLiteral(role)
	markerLiteral := quoteLiteral("mss-shop-reconciler:" + stage.Environment + ":role:" + role)
	return []Statement{
		{
			Name:      "load-password-" + role,
			SQL:       "SELECT set_config('" + setting + "', $1, true)",
			Arguments: []any{string(password)},
			Sensitive: true,
		},
		plain("ensure-managed-role-"+role, fmt.Sprintf(`DO $r1_role$
DECLARE
  managed_role_oid oid;
BEGIN
  PERFORM set_config('%s', 'existing', true);
  SELECT oid INTO managed_role_oid FROM pg_catalog.pg_roles WHERE rolname = %s;
  IF managed_role_oid IS NULL THEN
    EXECUTE format('CREATE ROLE %%I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS', %s);
    EXECUTE format('COMMENT ON ROLE %%I IS %%L', %s, %s);
    PERFORM set_config('%s', 'created', true);
  ELSE
    IF pg_catalog.shobj_description(managed_role_oid, 'pg_authid') IS DISTINCT FROM %s THEN
      RAISE EXCEPTION 'refusing to adopt unmanaged role %%', %s;
    END IF;
    IF EXISTS (
      SELECT 1 FROM pg_catalog.pg_roles
      WHERE oid = managed_role_oid
        AND (NOT rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolinherit
             OR rolreplication OR rolbypassrls OR rolconnlimit <> -1 OR rolconfig IS NOT NULL
             OR rolvaliduntil IS NOT NULL)
    ) THEN
      RAISE EXCEPTION 'managed role %% has unsafe attributes', %s;
    END IF;
    IF EXISTS (
      SELECT 1 FROM pg_catalog.pg_auth_members
      WHERE member = managed_role_oid OR roleid = managed_role_oid
    ) THEN
      RAISE EXCEPTION 'managed role %% has unexpected memberships', %s;
    END IF;
    IF EXISTS (
      SELECT 1 FROM pg_catalog.pg_db_role_setting WHERE setrole = managed_role_oid
    ) THEN
      RAISE EXCEPTION 'managed role %% has database-specific settings', %s;
    END IF;
  END IF;
END
$r1_role$`, actionSetting, roleLiteral, roleLiteral, roleLiteral, markerLiteral, actionSetting, markerLiteral, roleLiteral, roleLiteral, roleLiteral, roleLiteral)),
		{
			Name: "set-password-" + role,
			SQL: fmt.Sprintf(`DO $r1_password$
BEGIN
  IF current_setting('%s') = 'created' THEN
    EXECUTE format(
      'ALTER ROLE %%I WITH LOGIN PASSWORD %%L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS',
      %s,
      current_setting('%s')
    );
  END IF;
END
$r1_password$`, actionSetting, roleLiteral, setting),
			Sensitive: true,
		},
		plain("validate-managed-role-"+role, fmt.Sprintf(`DO $r1_role_final$
DECLARE
  managed_role_oid oid;
BEGIN
  SELECT oid INTO managed_role_oid FROM pg_catalog.pg_roles WHERE rolname = %s;
  IF managed_role_oid IS NULL
     OR pg_catalog.shobj_description(managed_role_oid, 'pg_authid') IS DISTINCT FROM %s
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_roles
       WHERE oid = managed_role_oid
         AND (NOT rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolinherit
              OR rolreplication OR rolbypassrls OR rolconnlimit <> -1 OR rolconfig IS NOT NULL
              OR rolvaliduntil IS NOT NULL)
     )
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_auth_members
       WHERE member = managed_role_oid OR roleid = managed_role_oid
     )
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_db_role_setting WHERE setrole = managed_role_oid
     ) THEN
    RAISE EXCEPTION 'managed role failed its post-change security validation';
  END IF;
END
$r1_role_final$`, roleLiteral, markerLiteral)),
	}
}

func ensureCompatibilityOwner(role string) Statement {
	roleLiteral := quoteLiteral(role)
	markerLiteral := quoteLiteral("mss-shop-reconciler:" + stage.Environment + ":compat-owner:" + role)
	return plain("ensure-compatibility-owner-"+role, fmt.Sprintf(`DO $mss_compat_owner$
DECLARE
  owner_oid oid;
BEGIN
  SELECT oid INTO owner_oid FROM pg_catalog.pg_roles WHERE rolname = %s;
  IF owner_oid IS NULL THEN
    EXECUTE format(
      'CREATE ROLE %%I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1',
      %s
    );
    EXECUTE format('COMMENT ON ROLE %%I IS %%L', %s, %s);
    SELECT oid INTO owner_oid FROM pg_catalog.pg_roles WHERE rolname = %s;
  END IF;

  IF owner_oid IS NULL
     OR pg_catalog.shobj_description(owner_oid, 'pg_authid') IS DISTINCT FROM %s
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_roles
       WHERE oid = owner_oid
         AND (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolinherit
              OR rolreplication OR rolbypassrls OR rolconnlimit <> -1 OR rolconfig IS NOT NULL
              OR rolvaliduntil IS NOT NULL)
     )
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_authid WHERE oid = owner_oid AND rolpassword IS NOT NULL)
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_auth_members
       WHERE member = owner_oid OR roleid = owner_oid
     )
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_db_role_setting WHERE setrole = owner_oid) THEN
    RAISE EXCEPTION 'compatibility owner role has an unsafe identity, credential, attribute, membership, or setting';
  END IF;
END
$mss_compat_owner$`,
		roleLiteral,
		roleLiteral,
		roleLiteral,
		markerLiteral,
		roleLiteral,
		markerLiteral,
	))
}

func ensureManagedSchema(schema, owner string) []Statement {
	schemaLiteral := quoteLiteral(schema)
	ownerLiteral := quoteLiteral(owner)
	markerLiteral := quoteLiteral("mss-shop-reconciler:" + stage.Environment + ":schema:" + schema)
	return []Statement{
		plain("ensure-managed-schema-"+schema, fmt.Sprintf(`DO $r1_schema$
DECLARE
  managed_schema_oid oid;
  managed_schema_owner oid;
BEGIN
  SELECT oid, nspowner INTO managed_schema_oid, managed_schema_owner
    FROM pg_catalog.pg_namespace WHERE nspname = %s;
  IF managed_schema_oid IS NULL THEN
    EXECUTE format('CREATE SCHEMA %%I AUTHORIZATION %%I', %s, %s);
    EXECUTE format('COMMENT ON SCHEMA %%I IS %%L', %s, %s);
  ELSE
    IF managed_schema_owner <> (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s)
       OR pg_catalog.obj_description(managed_schema_oid, 'pg_namespace') IS DISTINCT FROM %s THEN
      RAISE EXCEPTION 'refusing to adopt unmanaged or foreign-owned schema %%', %s;
    END IF;
  END IF;
END
$r1_schema$`, schemaLiteral, schemaLiteral, ownerLiteral, schemaLiteral, markerLiteral, ownerLiteral, markerLiteral, schemaLiteral)),
		plain("protect-schema-"+schema, fmt.Sprintf(
			"REVOKE ALL ON SCHEMA %s FROM PUBLIC",
			quoteIdentifier(schema),
		)),
	}
}

func ensureCompatibilityOwnedSchema(schema, owner string) []Statement {
	schemaLiteral := quoteLiteral(schema)
	ownerLiteral := quoteLiteral(owner)
	markerLiteral := quoteLiteral("mss-shop-reconciler:" + stage.Environment + ":compat-schema:" + schema)
	return []Statement{
		plain("ensure-compatibility-owned-schema-"+schema, fmt.Sprintf(`DO $r1_readonly_schema$
DECLARE
  managed_schema_oid oid;
  managed_schema_owner oid;
BEGIN
	  SELECT oid, nspowner INTO managed_schema_oid, managed_schema_owner
	    FROM pg_catalog.pg_namespace WHERE nspname = %s;
	  IF managed_schema_oid IS NULL THEN
	    EXECUTE format('CREATE SCHEMA %%I AUTHORIZATION %%I', %s, %s);
	    EXECUTE format('COMMENT ON SCHEMA %%I IS %%L', %s, %s);
	  ELSE
	    IF managed_schema_owner <> (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s)
	       OR pg_catalog.obj_description(managed_schema_oid, 'pg_namespace') IS DISTINCT FROM %s THEN
	      RAISE EXCEPTION 'refusing to adopt unmanaged or foreign-owned compatibility schema %%', %s;
	    END IF;
	  END IF;
END
$r1_readonly_schema$`, schemaLiteral, schemaLiteral, ownerLiteral, schemaLiteral, markerLiteral, ownerLiteral, markerLiteral, schemaLiteral)),
		plain("protect-compatibility-owned-schema-"+schema, fmt.Sprintf(
			"REVOKE ALL ON SCHEMA %s FROM PUBLIC",
			quoteIdentifier(schema),
		)),
	}
}

func validateManagedSchemaACL(schema, owner, runtime string) Statement {
	return plain("validate-schema-acl-"+schema, fmt.Sprintf(`DO $r1_schema_acl$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_namespace AS namespace
    CROSS JOIN LATERAL pg_catalog.aclexplode(
      COALESCE(namespace.nspacl, pg_catalog.acldefault('n', namespace.nspowner))
    ) AS privilege
    WHERE namespace.nspname = %s
      AND (
        privilege.grantee NOT IN (
          namespace.nspowner,
          (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s)
        )
        OR (
          privilege.grantee = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s)
          AND (privilege.privilege_type <> 'USAGE' OR privilege.is_grantable)
        )
      )
  ) THEN
    RAISE EXCEPTION 'managed schema %% has unexpected ACL grantees', %s;
  END IF;
  IF NOT pg_catalog.has_schema_privilege(%s, %s, 'USAGE') THEN
    RAISE EXCEPTION 'managed runtime role lacks schema usage';
  END IF;
END
$r1_schema_acl$`,
		quoteLiteral(schema),
		quoteLiteral(runtime),
		quoteLiteral(runtime),
		quoteLiteral(schema),
		quoteLiteral(runtime),
		quoteLiteral(schema),
	))
}

func validateCompatibilityOwnedSchemaACL(schema, owner, migrator, runtime string) Statement {
	return plain("validate-compatibility-owned-schema-acl-"+schema, fmt.Sprintf(`DO $r1_readonly_schema_acl$
DECLARE
	  owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s);
	  runtime_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s);
BEGIN
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_namespace AS namespace
    CROSS JOIN LATERAL pg_catalog.aclexplode(
      COALESCE(namespace.nspacl, pg_catalog.acldefault('n', namespace.nspowner))
    ) AS privilege
    WHERE namespace.nspname = %s
      AND (
	        privilege.grantee NOT IN (owner_oid, runtime_oid)
	        OR privilege.grantor <> owner_oid
	        OR privilege.is_grantable
	        OR (
	          privilege.grantee = runtime_oid
	          AND privilege.privilege_type <> 'USAGE'
	        )
	      )
	  ) THEN
	    RAISE EXCEPTION 'compatibility-owned read-only schema has an unexpected ACL';
	  END IF;
	  IF NOT EXISTS (
	       SELECT 1 FROM pg_catalog.pg_namespace
	       WHERE nspname = %s AND nspowner = owner_oid
	     )
	     OR pg_catalog.has_schema_privilege(%s, %s, 'CREATE')
	     OR pg_catalog.has_schema_privilege(%s, %s, 'CREATE')
	     OR NOT pg_catalog.has_schema_privilege(%s, %s, 'USAGE') THEN
	    RAISE EXCEPTION 'read-only schema ownership or runtime boundary is unsafe';
  END IF;
END
	$r1_readonly_schema_acl$`,
		quoteLiteral(owner),
		quoteLiteral(runtime),
		quoteLiteral(schema),
		quoteLiteral(schema),
		quoteLiteral(migrator), quoteLiteral(schema),
		quoteLiteral(runtime), quoteLiteral(schema),
		quoteLiteral(runtime), quoteLiteral(schema),
	))
}

func validateCompatibilityOwnerDefaultACL(schema, owner string) Statement {
	return plain("validate-compatibility-owner-default-acl-"+schema, fmt.Sprintf(`DO $r1_readonly_defaults$
DECLARE
	  owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s);
BEGIN
	  -- The NOLOGIN owner never creates objects directly. Every compatibility
	  -- object is created by the trusted transaction, transferred explicitly and
	  -- receives an exact ACL, so any owner default ACL is unreviewed state.
	  IF EXISTS (
	    SELECT 1
	    FROM pg_catalog.pg_default_acl AS defaults
	    WHERE defaults.defaclrole = owner_oid
	  ) OR EXISTS (
	    SELECT 1
	    FROM pg_catalog.pg_default_acl AS defaults
	    CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
	    WHERE privilege.grantee = owner_oid
	  ) THEN
	    RAISE EXCEPTION 'compatibility owner has unexpected direct or incoming default ACL state';
	  END IF;
END
$r1_readonly_defaults$`, quoteLiteral(owner)))
}

func validateManagedDefaultACL(owner, schema, runtime string) Statement {
	return plain("validate-managed-default-acl-"+schema, fmt.Sprintf(`DO $r1_core_defaults$
DECLARE
  owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s);
  runtime_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s);
  core_schema_oid oid := (SELECT oid FROM pg_catalog.pg_namespace WHERE nspname = %s);
BEGIN
  IF (
    SELECT count(*) FROM pg_catalog.pg_default_acl
    WHERE defaclrole = owner_oid AND defaclnamespace = 0
      AND defaclobjtype = 'f'
  ) <> 1 OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_default_acl
    WHERE defaclrole = owner_oid AND defaclnamespace = 0
      AND defaclobjtype <> 'f'
  ) OR (
    SELECT count(*) FROM pg_catalog.pg_default_acl
    WHERE defaclrole = owner_oid AND defaclnamespace = core_schema_oid
  ) <> 2 OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_default_acl
    WHERE defaclrole = owner_oid AND defaclnamespace = core_schema_oid
      AND defaclobjtype NOT IN ('r', 'S', 'f')
  ) THEN
    RAISE EXCEPTION 'managed core owner has an unexpected default ACL inventory';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_default_acl AS defaults
    CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
    WHERE defaults.defaclrole = owner_oid
      AND defaults.defaclnamespace = 0
      AND (
        defaults.defaclobjtype <> 'f'
        OR privilege.grantee <> owner_oid
        OR privilege.grantor <> owner_oid
        OR privilege.privilege_type <> 'EXECUTE'
        OR privilege.is_grantable
      )
  ) OR (SELECT count(*)
          FROM pg_catalog.pg_default_acl AS defaults
          CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
         WHERE defaults.defaclrole = owner_oid AND defaults.defaclnamespace = 0) <> 1 THEN
    RAISE EXCEPTION 'managed core owner has an unsafe global function default ACL';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_default_acl AS defaults
    CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
    WHERE defaults.defaclrole = owner_oid
      AND defaults.defaclnamespace = core_schema_oid
      AND (
        privilege.grantee NOT IN (owner_oid, runtime_oid)
        OR privilege.grantor <> owner_oid
        OR (privilege.grantee = runtime_oid AND privilege.is_grantable)
        OR (
          privilege.grantee = runtime_oid AND (
            (defaults.defaclobjtype = 'r' AND privilege.privilege_type NOT IN ('SELECT','INSERT','UPDATE','DELETE'))
            OR (defaults.defaclobjtype = 'S' AND privilege.privilege_type NOT IN ('USAGE','SELECT','UPDATE'))
            OR defaults.defaclobjtype = 'f'
          )
        )
      )
  ) THEN
    RAISE EXCEPTION 'managed core owner has an unsafe default ACL';
  END IF;

  IF (SELECT array_agg(privilege.privilege_type ORDER BY privilege.privilege_type)
        FROM pg_catalog.pg_default_acl AS defaults
        CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
       WHERE defaults.defaclrole = owner_oid AND defaults.defaclnamespace = core_schema_oid
         AND defaults.defaclobjtype = 'r' AND privilege.grantee = runtime_oid)
       IS DISTINCT FROM ARRAY['DELETE','INSERT','SELECT','UPDATE']::text[]
     OR (SELECT array_agg(privilege.privilege_type ORDER BY privilege.privilege_type)
           FROM pg_catalog.pg_default_acl AS defaults
           CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
          WHERE defaults.defaclrole = owner_oid AND defaults.defaclnamespace = core_schema_oid
            AND defaults.defaclobjtype = 'S' AND privilege.grantee = runtime_oid)
       IS DISTINCT FROM ARRAY['SELECT','UPDATE','USAGE']::text[]
     OR (SELECT array_agg(privilege.privilege_type ORDER BY privilege.privilege_type)
           FROM pg_catalog.pg_default_acl AS defaults
           CROSS JOIN LATERAL pg_catalog.aclexplode(defaults.defaclacl) AS privilege
          WHERE defaults.defaclrole = owner_oid AND defaults.defaclnamespace = core_schema_oid
            AND defaults.defaclobjtype = 'f' AND privilege.grantee = runtime_oid)
       IS NOT NULL THEN
    RAISE EXCEPTION 'managed core runtime default privileges are incomplete';
  END IF;
END
$r1_core_defaults$`, quoteLiteral(owner), quoteLiteral(runtime), quoteLiteral(schema)))
}

func buildLegacySourceBoundaryBatch(roles stage.Roles) Batch {
	statements := []Statement{
		validateLegacySourcePublicACLs(),
		plain("grant-tenant-compatibility-owner-public-usage", fmt.Sprintf(
			"GRANT USAGE ON SCHEMA %s TO %s",
			quoteIdentifier(stage.LegacySchema), quoteIdentifier(roles.TenantCompatibilityOwner),
		)),
		plain("grant-mall-compatibility-owner-public-usage", fmt.Sprintf(
			"GRANT USAGE ON SCHEMA %s TO %s",
			quoteIdentifier(stage.LegacySchema), quoteIdentifier(roles.MallCompatibilityOwner),
		)),
	}
	for _, relation := range tenantCompatibilitySourceNames() {
		statements = append(statements, plain("grant-tenant-compatibility-source-"+relation, fmt.Sprintf(
			"GRANT SELECT ON TABLE %s TO %s",
			qualified(stage.LegacySchema, relation), quoteIdentifier(roles.TenantCompatibilityOwner),
		)))
	}
	for _, relation := range mallCompatibilitySourceNames() {
		statements = append(statements, plain("grant-mall-compatibility-source-"+relation, fmt.Sprintf(
			"GRANT SELECT ON TABLE %s TO %s",
			qualified(stage.LegacySchema, relation), quoteIdentifier(roles.MallCompatibilityOwner),
		)))
	}
	statements = append(statements,
		validateLegacySourceCompatibilityACLs(roles),
		validateLegacySourceEffectivePrivileges(roles),
	)
	return Batch{Name: "legacy-source-boundary", Statements: statements}
}

func tenantCompatibilitySourceNames() []string {
	return []string{tenantSharedResource}
}

func mallCompatibilitySourceNames() []string {
	names := make(map[string]struct{}, len(mallLegacyViews))
	for _, resource := range mallLegacyViews {
		names[resource.Name] = struct{}{}
		if resource.Inherited != nil {
			names[resource.Inherited.ParentTable] = struct{}{}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func validateLegacySourceEffectivePrivileges(roles stage.Roles) Statement {
	roleNames := []string{roles.TenantMigrator, roles.TenantRuntime, roles.MallMigrator, roles.MallRuntime}
	quotedRoles := make([]string, 0, len(roleNames))
	for _, role := range roleNames {
		quotedRoles = append(quotedRoles, quoteLiteral(role))
	}
	return plain("validate-app-roles-have-no-effective-legacy-source-access", fmt.Sprintf(`DO $r1_effective_source_acl$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM unnest(ARRAY[%s]::text[]) AS app_role(role_name)
    JOIN pg_catalog.pg_roles AS role_record ON role_record.rolname = app_role.role_name
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.nspname = %s
    JOIN pg_catalog.pg_class AS relation
      ON relation.relnamespace = namespace.oid AND relation.relkind IN ('r','p','v','m','f')
    WHERE relation.relowner = role_record.oid
       OR pg_catalog.has_table_privilege(app_role.role_name, relation.oid, 'SELECT')
       OR pg_catalog.has_table_privilege(app_role.role_name, relation.oid, 'INSERT')
       OR pg_catalog.has_table_privilege(app_role.role_name, relation.oid, 'UPDATE')
       OR pg_catalog.has_table_privilege(app_role.role_name, relation.oid, 'DELETE')
       OR pg_catalog.has_table_privilege(app_role.role_name, relation.oid, 'TRUNCATE')
       OR pg_catalog.has_table_privilege(app_role.role_name, relation.oid, 'REFERENCES')
       OR pg_catalog.has_table_privilege(app_role.role_name, relation.oid, 'TRIGGER')
       OR pg_catalog.has_any_column_privilege(app_role.role_name, relation.oid, 'SELECT')
       OR pg_catalog.has_any_column_privilege(app_role.role_name, relation.oid, 'INSERT')
       OR pg_catalog.has_any_column_privilege(app_role.role_name, relation.oid, 'UPDATE')
       OR pg_catalog.has_any_column_privilege(app_role.role_name, relation.oid, 'REFERENCES')
  ) THEN
    RAISE EXCEPTION 'an application LOGIN role has effective access to a public relation';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM unnest(ARRAY[%s]::text[]) AS app_role(role_name)
    JOIN pg_catalog.pg_roles AS role_record ON role_record.rolname = app_role.role_name
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.nspname = %s
    JOIN pg_catalog.pg_class AS sequence_relation
      ON sequence_relation.relnamespace = namespace.oid AND sequence_relation.relkind = 'S'
    WHERE sequence_relation.relowner = role_record.oid
       OR pg_catalog.has_sequence_privilege(app_role.role_name, sequence_relation.oid, 'SELECT')
       OR pg_catalog.has_sequence_privilege(app_role.role_name, sequence_relation.oid, 'UPDATE')
       OR pg_catalog.has_sequence_privilege(app_role.role_name, sequence_relation.oid, 'USAGE')
  ) THEN
    RAISE EXCEPTION 'an application LOGIN role has effective access to a public sequence';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM unnest(ARRAY[%s]::text[]) AS app_role(role_name)
    JOIN pg_catalog.pg_roles AS role_record ON role_record.rolname = app_role.role_name
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.nspname = %s
    JOIN pg_catalog.pg_proc AS routine ON routine.pronamespace = namespace.oid
    WHERE routine.prokind IN ('f','p') AND routine.prosecdef
      AND (
        routine.proowner = role_record.oid
        OR pg_catalog.has_function_privilege(app_role.role_name, routine.oid, 'EXECUTE')
      )
  ) THEN
    RAISE EXCEPTION 'an application LOGIN role can execute a public SECURITY DEFINER routine';
  END IF;
  IF EXISTS (
    SELECT 1 FROM unnest(ARRAY[%s]::text[]) AS app_role(role_name)
    WHERE pg_catalog.has_schema_privilege(app_role.role_name, %s, 'CREATE')
  ) THEN
    RAISE EXCEPTION 'an application LOGIN role can create objects in the legacy public schema';
  END IF;
END
$r1_effective_source_acl$`,
		strings.Join(quotedRoles, ", "),
		quoteLiteral(stage.LegacySchema),
		strings.Join(quotedRoles, ", "),
		quoteLiteral(stage.LegacySchema),
		strings.Join(quotedRoles, ", "),
		quoteLiteral(stage.LegacySchema),
		strings.Join(quotedRoles, ", "),
		quoteLiteral(stage.LegacySchema),
	))
}

func buildTenantSharedBatch(schemas stage.Schemas, roles stage.Roles) Batch {
	selectSQL := fmt.Sprintf(
		"SELECT %s FROM %s AS source",
		explicitProjection(tenantSharedResource, nil),
		qualified(stage.LegacySchema, tenantSharedResource),
	)
	return Batch{Name: "tenant-shared-payment", Statements: []Statement{
		reconcileManagedView(
			schemas.TenantShared,
			tenantSharedResource,
			selectSQL,
			roles.TenantCompatibilityOwner,
			roles.TenantRuntime,
		),
	}}
}

func buildMallViewsBatch(legacyTenantID string, schemas stage.Schemas, roles stage.Roles) Batch {
	statements := make([]Statement, 0, len(mallLegacyViews)+1)
	for _, resource := range mallLegacyViews {
		projection := legacyViewProjection(resource)
		predicate := quoteIdentifier("source") + "." + quoteIdentifier("tenant_id") + " = " + quoteLiteral(legacyTenantID)
		if resource.Inherited != nil {
			parent := resource.Inherited
			predicate = fmt.Sprintf(
				"EXISTS (SELECT 1 FROM %s AS owner WHERE owner.%s = source.%s AND owner.%s = %s%s)",
				qualified(stage.LegacySchema, parent.ParentTable),
				quoteIdentifier(parent.ParentColumn),
				quoteIdentifier(parent.LocalColumn),
				quoteIdentifier("tenant_id"),
				quoteLiteral(legacyTenantID),
				softDeletePredicate(parent.ParentSoftDelete),
			)
		}
		selectSQL := fmt.Sprintf(
			"SELECT %s FROM %s AS source WHERE %s",
			projection, qualified(stage.LegacySchema, resource.Name), predicate,
		)
		statements = append(statements, reconcileManagedView(
			schemas.MallBusiness,
			resource.Name,
			selectSQL,
			roles.MallCompatibilityOwner,
			roles.MallRuntime,
		))
	}
	statements = append(statements, buildMallSettingsPrivateView(legacyTenantID, schemas, roles))
	return Batch{Name: "mall-fixed-tenant-views", Statements: statements}
}

// buildMemberLevelsProjectionAuditBatch creates a fixed-name, single-row view
// that the mall runtime can query without receiving any privilege on the
// legacy public tables. Its compatibility owner evaluates source aggregates
// through the already-reviewed SELECT-only source grants. All compared columns
// remain explicit so the 12-column projection is part of the compiled contract.
func buildMemberLevelsProjectionAuditBatch(legacyTenantID string, schemas stage.Schemas, roles stage.Roles) Batch {
	columns := legacySourceColumns["member_levels"]
	selectSQL := fmt.Sprintf(`WITH source_rows AS (
  SELECT %s
  FROM %s AS source
  WHERE source.%s = %s
), business_rows AS (
  SELECT %s
  FROM %s AS business
), source_minus_business AS (
  SELECT %s FROM source_rows
  EXCEPT ALL
  SELECT %s FROM business_rows
), business_minus_source AS (
  SELECT %s FROM business_rows
  EXCEPT ALL
  SELECT %s FROM source_rows
)
SELECT
  (SELECT count(*) FROM source_rows)::bigint AS public_member_levels_rows,
  (SELECT count(*) FROM business_rows)::bigint AS business_member_levels_rows,
  ((SELECT count(*) FROM source_minus_business) +
   (SELECT count(*) FROM business_minus_source))::bigint AS difference_rows,
  (SELECT count(*) FROM business_rows WHERE tenant_id IS DISTINCT FROM %s)::bigint AS cross_tenant_rows,
  (SELECT count(*) FROM business_rows WHERE init IS TRUE)::bigint AS flagged_default_rows,
  (SELECT count(*) FROM business_rows
    WHERE init IS TRUE AND deleted_at IS NULL AND status = 1)::bigint AS enabled_default_rows,
  (SELECT count(*) FROM business_rows
    WHERE init IS TRUE AND (deleted_at IS NOT NULL OR status IS DISTINCT FROM 1))::bigint AS invalid_default_rows,
  (SELECT count(*) FROM (
     SELECT name FROM source_rows
     WHERE deleted_at IS NULL
     GROUP BY name HAVING count(*) > 1
   ) AS duplicate_active_names)::bigint AS duplicate_active_name_groups,
  (SELECT count(*) FROM %s)::bigint AS public_orders_rows,
  (SELECT count(*) FROM %s)::bigint AS business_orders_rows,
  (SELECT count(*) FROM %s)::bigint AS public_order_goods_rows,
  (SELECT count(*) FROM %s)::bigint AS business_order_goods_rows`,
		projectionForAlias("source", columns),
		qualified(stage.LegacySchema, "member_levels"),
		quoteIdentifier("tenant_id"),
		quoteLiteral(legacyTenantID),
		projectionForAlias("business", columns),
		qualified(schemas.MallBusiness, "member_levels"),
		columnReferences("source_rows", columns),
		columnReferences("business_rows", columns),
		columnReferences("business_rows", columns),
		columnReferences("source_rows", columns),
		quoteLiteral(legacyTenantID),
		qualified(stage.LegacySchema, "orders"),
		qualified(schemas.MallBusiness, "orders"),
		qualified(stage.LegacySchema, "order_goods"),
		qualified(schemas.MallBusiness, "order_goods"),
	)
	return Batch{Name: "member-levels-projection-audit", Statements: []Statement{
		reconcileManagedView(
			schemas.MallBusiness,
			memberLevelsProjectionAuditView,
			selectSQL,
			roles.MallCompatibilityOwner,
			roles.MallRuntime,
		),
	}}
}

func projectionForAlias(alias string, columns []string) string {
	projection := make([]string, 0, len(columns))
	for _, column := range columns {
		projection = append(projection, fmt.Sprintf(
			"%s.%s AS %s",
			quoteIdentifier(alias),
			quoteIdentifier(column),
			quoteIdentifier(column),
		))
	}
	return strings.Join(projection, ", ")
}

func columnReferences(alias string, columns []string) string {
	references := make([]string, 0, len(columns))
	for _, column := range columns {
		references = append(references, quoteIdentifier(alias)+"."+quoteIdentifier(column))
	}
	return strings.Join(references, ", ")
}

func reconcileManagedView(schema, relation, selectSQL, owner, runtime string) Statement {
	target := qualified(schema, relation)
	targetLiteral := quoteLiteral(target)
	marker := "mss-shop-reconciler:" + stage.Environment + ":view:" + schema + "." + relation
	temporaryName := "r1_expected_" + strings.ReplaceAll(relation, "-", "_")
	createSQL := "CREATE VIEW " + target + " WITH (security_barrier=true, security_invoker=false) AS " + selectSQL
	temporarySQL := "CREATE VIEW pg_temp." + quoteIdentifier(temporaryName) + " WITH (security_barrier=true, security_invoker=false) AS " + selectSQL
	return plain("reconcile-managed-view-"+schema+"-"+relation, fmt.Sprintf(`DO $r1_managed_view$
DECLARE
  target_oid oid;
  expected_oid oid;
	  owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s);
	  runtime_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s);
  target_dependencies text[];
  expected_dependencies text[];
BEGIN
  SELECT relation_oid.oid INTO target_oid
    FROM pg_catalog.pg_class AS relation_oid
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation_oid.relnamespace
    WHERE namespace.nspname = %s AND relation_oid.relname = %s;

  IF target_oid IS NULL THEN
	    EXECUTE %s;
	    EXECUTE %s;
	    EXECUTE %s;
	    EXECUTE %s;
	    EXECUTE %s;
    SELECT relation_oid.oid INTO target_oid
      FROM pg_catalog.pg_class AS relation_oid
      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation_oid.relnamespace
      WHERE namespace.nspname = %s AND relation_oid.relname = %s;
  ELSE
    -- ACCESS SHARE prevents target/source DDL while allowing legacy DML.
    -- Existing managed views are verified and left untouched on retry.
    EXECUTE 'LOCK TABLE ' || %s || ' IN ACCESS SHARE MODE';
    SELECT relation_oid.oid INTO target_oid
      FROM pg_catalog.pg_class AS relation_oid
      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation_oid.relnamespace
      WHERE namespace.nspname = %s AND relation_oid.relname = %s;
  END IF;
  IF target_oid IS NULL THEN
    RAISE EXCEPTION 'managed view disappeared while locked';
  END IF;
  IF NOT EXISTS (
       SELECT 1 FROM pg_catalog.pg_class
       WHERE oid = target_oid
         AND relkind = 'v'
	         AND relowner = owner_oid
         AND NOT relrowsecurity
         AND NOT relforcerowsecurity
         AND COALESCE(reloptions, ARRAY[]::text[]) @> ARRAY['security_barrier=true', 'security_invoker=false']::text[]
         AND cardinality(COALESCE(reloptions, ARRAY[]::text[])) = 2
     )
     OR pg_catalog.obj_description(target_oid, 'pg_class') IS DISTINCT FROM %s THEN
	    RAISE EXCEPTION 'refusing to replace unmanaged or foreign-owned compatibility view %%', %s;
  END IF;
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_attribute AS attribute
    CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
    WHERE attribute.attrelid = target_oid AND attribute.attnum > 0 AND NOT attribute.attisdropped
  ) THEN
    RAISE EXCEPTION 'managed compatibility view has column privileges';
  END IF;
  IF EXISTS (
    SELECT 1 FROM pg_catalog.aclexplode(
	      COALESCE((SELECT relacl FROM pg_catalog.pg_class WHERE oid = target_oid), pg_catalog.acldefault('r', owner_oid))
	    ) AS privilege
	    WHERE (privilege.grantee = runtime_oid AND (
	             privilege.privilege_type <> 'SELECT' OR privilege.is_grantable OR privilege.grantor <> owner_oid
	          ))
	       OR privilege.grantee NOT IN (owner_oid, runtime_oid)
  ) OR NOT pg_catalog.has_table_privilege(%s, target_oid, 'SELECT') THEN
    RAISE EXCEPTION 'managed compatibility view has an unexpected ACL';
  END IF;
  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid = target_oid AND NOT tgisinternal
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_rewrite
       WHERE ev_class = target_oid AND rulename <> '_RETURN'
     ) OR (SELECT count(*) FROM pg_catalog.pg_rewrite WHERE ev_class = target_oid AND rulename = '_RETURN') <> 1
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_policy WHERE polrelid = target_oid
     ) THEN
    RAISE EXCEPTION 'managed compatibility view has executable catalog drift';
  END IF;

  EXECUTE %s;
  SELECT relation_oid.oid INTO expected_oid
    FROM pg_catalog.pg_class AS relation_oid
    WHERE relation_oid.relnamespace = pg_catalog.pg_my_temp_schema()
      AND relation_oid.relname = %s;
  IF pg_catalog.pg_get_viewdef(target_oid, true) IS DISTINCT FROM pg_catalog.pg_get_viewdef(expected_oid, true) THEN
    RAISE EXCEPTION 'managed compatibility view definition drifted';
  END IF;
  SELECT array_agg(
           dependency.refclassid::regclass::text || ':'
           || CASE WHEN dependency.refclassid = 'pg_catalog.pg_class'::regclass
                        AND dependency.refobjid = target_oid
                   THEN 'SELF' ELSE dependency.refobjid::text END
           || ':' || dependency.refobjsubid::text || ':' || dependency.deptype::text
           ORDER BY dependency.refclassid, dependency.refobjid = target_oid,
                    dependency.refobjid, dependency.refobjsubid, dependency.deptype
         )
    INTO target_dependencies
    FROM pg_catalog.pg_rewrite AS rewrite
    JOIN pg_catalog.pg_depend AS dependency
      ON dependency.classid = 'pg_catalog.pg_rewrite'::regclass
     AND dependency.objid = rewrite.oid
    WHERE rewrite.ev_class = target_oid AND rewrite.rulename = '_RETURN';
  SELECT array_agg(
           dependency.refclassid::regclass::text || ':'
           || CASE WHEN dependency.refclassid = 'pg_catalog.pg_class'::regclass
                        AND dependency.refobjid = expected_oid
                   THEN 'SELF' ELSE dependency.refobjid::text END
           || ':' || dependency.refobjsubid::text || ':' || dependency.deptype::text
           ORDER BY dependency.refclassid, dependency.refobjid = expected_oid,
                    dependency.refobjid, dependency.refobjsubid, dependency.deptype
         )
    INTO expected_dependencies
    FROM pg_catalog.pg_rewrite AS rewrite
    JOIN pg_catalog.pg_depend AS dependency
      ON dependency.classid = 'pg_catalog.pg_rewrite'::regclass
     AND dependency.objid = rewrite.oid
    WHERE rewrite.ev_class = expected_oid AND rewrite.rulename = '_RETURN';
  IF target_dependencies IS DISTINCT FROM expected_dependencies THEN
    RAISE EXCEPTION 'managed compatibility view dependency binding drifted';
  END IF;
  EXECUTE %s;
END
$r1_managed_view$`,
		quoteLiteral(owner),
		quoteLiteral(runtime),
		quoteLiteral(schema), quoteLiteral(relation),
		quoteLiteral(createSQL),
		quoteLiteral("COMMENT ON VIEW "+target+" IS "+quoteLiteral(marker)),
		quoteLiteral("ALTER VIEW "+target+" OWNER TO "+quoteIdentifier(owner)),
		quoteLiteral("REVOKE ALL ON TABLE "+target+" FROM PUBLIC"),
		quoteLiteral("GRANT SELECT ON TABLE "+target+" TO "+quoteIdentifier(runtime)),
		quoteLiteral(schema), quoteLiteral(relation),
		targetLiteral,
		quoteLiteral(schema), quoteLiteral(relation),
		quoteLiteral(marker), targetLiteral,
		quoteLiteral(runtime),
		quoteLiteral(temporarySQL), quoteLiteral(temporaryName),
		quoteLiteral("DROP VIEW pg_temp."+quoteIdentifier(temporaryName)),
	))
}

func legacyViewProjection(resource legacyView) string {
	return explicitProjection(resource.Name, resource.RedactedColumns)
}

func explicitProjection(relation string, redactedColumns []string) string {
	redacted := make(map[string]struct{}, len(redactedColumns))
	for _, column := range redactedColumns {
		redacted[column] = struct{}{}
	}
	columns := legacySourceColumns[relation]
	projection := make([]string, 0, len(columns))
	for _, column := range columns {
		if _, sensitive := redacted[column]; sensitive {
			projection = append(projection, fmt.Sprintf(
				"(NULL::%s).%s AS %s",
				qualified(stage.LegacySchema, relation),
				quoteIdentifier(column),
				quoteIdentifier(column),
			))
			continue
		}
		projection = append(projection, fmt.Sprintf(
			"%s.%s AS %s",
			quoteIdentifier("source"),
			quoteIdentifier(column),
			quoteIdentifier(column),
		))
	}
	return strings.Join(projection, ", ")
}

func buildSnapshotAuditBatch(schemas stage.Schemas, roles stage.Roles) Batch {
	return Batch{Name: "snapshot-audit", Statements: []Statement{
		validateSnapshotPublicationBoundary(schemas.MallBusiness),
		reconcileSnapshotAuditTable(schemas.MallBusiness, roles.MallCompatibilityOwner),
	}}
}

func validateSnapshotPublicationBoundary(schema string) Statement {
	relations := append(snapshotNames(), snapshotAuditTable)
	quotedRelations := make([]string, 0, len(relations))
	for _, relation := range relations {
		quotedRelations = append(quotedRelations, quoteLiteral(relation))
	}
	return plain("validate-snapshot-publication-boundary", fmt.Sprintf(`DO $r1_snapshot_publication$
BEGIN
  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_publication WHERE puballtables
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_publication_namespace AS publication_namespace
       JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = publication_namespace.pnnspid
       WHERE namespace.nspname = %s
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_publication_rel AS publication_relation
       JOIN pg_catalog.pg_class AS relation ON relation.oid = publication_relation.prrelid
       JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
       WHERE namespace.nspname = %s
         AND relation.relname = ANY (ARRAY[%s]::text[])
     ) THEN
    RAISE EXCEPTION 'snapshot boundary is covered by a logical publication';
  END IF;
END
$r1_snapshot_publication$`,
		quoteLiteral(schema),
		quoteLiteral(schema),
		strings.Join(quotedRelations, ", "),
	))
}

func buildSnapshotBatch(resource snapshotResource, schemas stage.Schemas, owner, runtime string) Batch {
	source := qualified(stage.LegacySchema, resource.Name)
	target := qualified(schemas.MallBusiness, resource.Name)
	audit := qualified(schemas.MallBusiness, snapshotAuditTable)
	sourceHash := keyHashExpression("source", resource.PrimaryKey)
	targetHash := keyHashExpression("target", resource.PrimaryKey)
	sourceRowHash := rowHashExpression("source", resource.PrimaryKey)
	targetRowHash := rowHashExpression("target", resource.PrimaryKey)
	quotedColumns := make([]string, 0, len(legacySourceColumns[resource.Name]))
	sourceColumns := make([]string, 0, len(legacySourceColumns[resource.Name]))
	for _, column := range legacySourceColumns[resource.Name] {
		quotedColumns = append(quotedColumns, quoteIdentifier(column))
		sourceColumns = append(sourceColumns, quoteIdentifier("source")+"."+quoteIdentifier(column))
	}
	tag := "$snapshot_" + resource.Name + "$"

	copyAndAudit := fmt.Sprintf(`DO %[1]s
DECLARE
  recorded_source_schema text;
  recorded_source_relation text;
  recorded_plan_version text;
  recorded_source_rows bigint;
  recorded_target_rows bigint;
  recorded_source_hash text;
  recorded_target_hash text;
  recorded_source_row_hash text;
  recorded_target_row_hash text;
  source_rows bigint;
  target_rows bigint;
  source_hash text;
  target_hash text;
  source_row_hash text;
  target_row_hash text;
BEGIN
  SELECT checkpoint.source_schema, checkpoint.source_relation, checkpoint.plan_version,
         checkpoint.source_row_count, checkpoint.target_row_count,
         checkpoint.source_key_hash, checkpoint.target_key_hash,
         checkpoint.source_row_hash, checkpoint.target_row_hash
    INTO recorded_source_schema, recorded_source_relation, recorded_plan_version,
         recorded_source_rows, recorded_target_rows, recorded_source_hash, recorded_target_hash,
         recorded_source_row_hash, recorded_target_row_hash
    FROM %[2]s AS checkpoint
    WHERE checkpoint.resource_name = %[3]s;

  IF FOUND THEN
    SELECT count(*), %[4]s, %[10]s INTO source_rows, source_hash, source_row_hash FROM %[5]s AS source;
    SELECT count(*), %[6]s, %[11]s INTO target_rows, target_hash, target_row_hash FROM %[7]s AS target;
    IF recorded_source_schema <> %[8]s
       OR recorded_source_relation <> %[3]s
       OR recorded_plan_version <> %[9]s
       OR source_rows <> recorded_source_rows
       OR source_hash <> recorded_source_hash
       OR source_row_hash <> recorded_source_row_hash
       OR target_rows <> recorded_target_rows
       OR target_hash <> recorded_target_hash
       OR target_row_hash <> recorded_target_row_hash
       OR source_rows <> target_rows
       OR source_hash <> target_hash
       OR source_row_hash <> target_row_hash THEN
      RAISE EXCEPTION 'audited tenant snapshot %% has drifted or its plan/source binding changed', %[3]s;
    END IF;
    RETURN;
  END IF;

  IF EXISTS (SELECT 1 FROM %[7]s LIMIT 1) THEN
    RAISE EXCEPTION 'tenant snapshot %% is populated without an audit checkpoint', %[3]s;
  END IF;

  SELECT count(*), %[4]s, %[10]s INTO source_rows, source_hash, source_row_hash FROM %[5]s AS source;
  INSERT INTO %[7]s (%[12]s) SELECT %[13]s FROM %[5]s AS source;
  SELECT count(*), %[6]s, %[11]s INTO target_rows, target_hash, target_row_hash FROM %[7]s AS target;

  IF source_rows <> target_rows OR source_hash <> target_hash OR source_row_hash <> target_row_hash THEN
    RAISE EXCEPTION 'tenant snapshot %% failed count/key/full-row reconciliation', %[3]s;
  END IF;

  INSERT INTO %[2]s (
    resource_name, source_schema, source_relation, plan_version,
    source_row_count, target_row_count, source_key_hash, target_key_hash,
    source_row_hash, target_row_hash
  ) VALUES (
    %[3]s, %[8]s, %[3]s, %[9]s, source_rows, target_rows, source_hash, target_hash,
    source_row_hash, target_row_hash
  );
END

%[1]s`,
		tag,
		audit,
		quoteLiteral(resource.Name),
		sourceHash,
		source,
		targetHash,
		target,
		quoteLiteral(stage.LegacySchema),
		quoteLiteral(snapshotPlanVersion),
		sourceRowHash,
		targetRowHash,
		strings.Join(quotedColumns, ", "),
		strings.Join(sourceColumns, ", "),
	)

	return Batch{Name: "snapshot-" + resource.Name, Statements: []Statement{
		validateSnapshotSource(resource),
		reconcileSnapshotTable(resource, schemas.MallBusiness, owner, runtime),
		plain("copy-and-audit-snapshot-"+resource.Name, copyAndAudit),
	}}
}

func reconcileSnapshotAuditTable(schema, owner string) Statement {
	target := qualified(schema, snapshotAuditTable)
	marker := "mss-shop-reconciler:" + stage.Environment + ":audit-table:" + schema + "." + snapshotAuditTable + ":" + snapshotPlanVersion
	expectedColumns := sqlTextArray(auditColumnFingerprints())
	createSQL := fmt.Sprintf(`CREATE TABLE %s (
  resource_name text PRIMARY KEY,
  source_schema text NOT NULL,
  source_relation text NOT NULL,
  plan_version text NOT NULL,
  source_row_count bigint NOT NULL CHECK (source_row_count >= 0),
  target_row_count bigint NOT NULL CHECK (target_row_count >= 0),
  source_key_hash text NOT NULL,
  target_key_hash text NOT NULL,
  source_row_hash text NOT NULL,
  target_row_hash text NOT NULL,
  completed_at timestamptz NOT NULL DEFAULT clock_timestamp()
) USING heap TABLESPACE pg_default`, target)
	return plain("reconcile-snapshot-audit-table", fmt.Sprintf(`DO $r1_audit_table$
DECLARE
  target_oid oid;
	  owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %[9]s);
  actual_columns text[];
BEGIN
  SELECT relation.oid INTO target_oid
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = %[1]s AND relation.relname = %[2]s;
	  IF target_oid IS NULL THEN
	    EXECUTE %[3]s;
	    EXECUTE %[4]s;
	    EXECUTE %[10]s;
	    EXECUTE %[5]s;
    SELECT relation.oid INTO target_oid
      FROM pg_catalog.pg_class AS relation
      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
      WHERE namespace.nspname = %[1]s AND relation.relname = %[2]s;
  ELSE
    EXECUTE 'LOCK TABLE ' || %[6]s || ' IN ACCESS SHARE MODE';
    SELECT relation.oid INTO target_oid
      FROM pg_catalog.pg_class AS relation
      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
      WHERE namespace.nspname = %[1]s AND relation.relname = %[2]s;
  END IF;
  IF NOT EXISTS (
       SELECT 1 FROM pg_catalog.pg_class
       WHERE oid = target_oid AND relkind = 'r' AND relpersistence = 'p'
	         AND relowner = owner_oid AND NOT relrowsecurity AND NOT relforcerowsecurity
         AND NOT relispartition AND NOT relhasrules AND NOT relhastriggers
         AND reltablespace = 0 AND relreplident = 'd' AND reloptions IS NULL
         AND (SELECT access_method.amname FROM pg_catalog.pg_am AS access_method WHERE access_method.oid = relam) = 'heap'
     )
     OR pg_catalog.obj_description(target_oid, 'pg_class') IS DISTINCT FROM %[7]s THEN
	    RAISE EXCEPTION 'refusing to adopt unmanaged or foreign-owned snapshot audit table';
  END IF;

  SELECT array_agg(
           attribute.attname || '|' || pg_catalog.format_type(attribute.atttypid, attribute.atttypmod)
           || '|' || attribute.attnotnull::text || '|' || attribute.attidentity::text
           || '|' || attribute.attgenerated::text || '|' || COALESCE(
             collation_namespace.nspname || '.' || collation_record.collname || ':'
             || collation_record.collprovider::text || ':' || collation_record.collisdeterministic::text
             || ':' || collation_record.collencoding::text,
             ''
           )
           || '|' || COALESCE(pg_catalog.pg_get_expr(default_value.adbin, attribute.attrelid), '')
           || '|' || attribute.attstorage::text || '|' || attribute.attcompression::text
           ORDER BY attribute.attnum
         )
    INTO actual_columns
    FROM pg_catalog.pg_attribute AS attribute
    LEFT JOIN pg_catalog.pg_attrdef AS default_value
      ON default_value.adrelid = attribute.attrelid AND default_value.adnum = attribute.attnum
    LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = attribute.attcollation
    LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace
    WHERE attribute.attrelid = target_oid AND attribute.attnum > 0 AND NOT attribute.attisdropped;
  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_attribute
       WHERE attrelid = target_oid AND attnum > 0 AND attisdropped
     ) OR actual_columns IS DISTINCT FROM %[8]s THEN
    RAISE EXCEPTION 'snapshot audit table column shape drifted';
  END IF;
  IF (SELECT count(*) FROM pg_catalog.pg_attrdef WHERE adrelid = target_oid) <> 1
     OR (SELECT pg_catalog.pg_get_expr(default_value.adbin, target_oid)
           FROM pg_catalog.pg_attrdef AS default_value
           JOIN pg_catalog.pg_attribute AS attribute
             ON attribute.attrelid = default_value.adrelid AND attribute.attnum = default_value.adnum
          WHERE default_value.adrelid = target_oid AND attribute.attname = 'completed_at')
        IS DISTINCT FROM 'clock_timestamp()' THEN
    RAISE EXCEPTION 'snapshot audit table default expression drifted';
  END IF;
  IF (SELECT count(*) FROM pg_catalog.pg_constraint WHERE conrelid = target_oid AND contype = 'p') <> 1
     OR NOT EXISTS (
       SELECT 1 FROM pg_catalog.pg_constraint AS constraint_record
       WHERE constraint_record.conrelid = target_oid AND constraint_record.contype = 'p'
         AND constraint_record.conkey = ARRAY[
           (SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = target_oid AND attname = 'resource_name')
         ]::smallint[]
     )
     OR (SELECT array_agg(pg_catalog.pg_get_constraintdef(oid, true) ORDER BY pg_catalog.pg_get_constraintdef(oid, true))
           FROM pg_catalog.pg_constraint WHERE conrelid = target_oid AND contype = 'c')
        IS DISTINCT FROM ARRAY['CHECK (source_row_count >= 0)', 'CHECK (target_row_count >= 0)']::text[]
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_constraint
       WHERE conrelid = target_oid AND contype NOT IN ('p','c')
     ) THEN
    RAISE EXCEPTION 'snapshot audit table constraints drifted';
  END IF;
  IF (SELECT count(*) FROM pg_catalog.pg_index WHERE indrelid = target_oid) <> 1
     OR NOT EXISTS (
       SELECT 1
       FROM pg_catalog.pg_index AS index_record
       JOIN pg_catalog.pg_class AS index_relation ON index_relation.oid = index_record.indexrelid
       JOIN pg_catalog.pg_am AS access_method ON access_method.oid = index_relation.relam
       WHERE index_record.indrelid = target_oid
         AND index_record.indisprimary AND index_record.indisunique
         AND index_record.indisvalid AND index_record.indisready AND index_record.indislive
         AND NOT index_record.indisclustered AND NOT index_record.indcheckxmin
         AND NOT index_record.indisreplident AND NOT index_record.indisexclusion
         AND NOT index_record.indnullsnotdistinct AND index_record.indoption::text = '0'
         AND index_record.indimmediate AND index_record.indexprs IS NULL AND index_record.indpred IS NULL
         AND index_record.indnkeyatts = 1 AND index_record.indnatts = 1 AND access_method.amname = 'btree'
         AND index_relation.relkind = 'i'
	         AND index_relation.relowner = owner_oid AND index_relation.relpersistence = 'p'
         AND index_relation.reltablespace = 0 AND index_relation.reloptions IS NULL
         AND (SELECT string_agg(operator_namespace.nspname || '.' || operator_class.opcname, ',' ORDER BY indexed_class.ordinality)
                FROM unnest(index_record.indclass) WITH ORDINALITY AS indexed_class(opclass_oid, ordinality)
                JOIN pg_catalog.pg_opclass AS operator_class ON operator_class.oid = indexed_class.opclass_oid
                JOIN pg_catalog.pg_namespace AS operator_namespace ON operator_namespace.oid = operator_class.opcnamespace) = 'pg_catalog.text_ops'
         AND (SELECT string_agg(
                       CASE WHEN indexed_collation.collation_oid = 0 THEN '' ELSE
                         collation_namespace.nspname || '.' || collation_record.collname || ':'
                         || collation_record.collprovider::text || ':' || collation_record.collisdeterministic::text
                         || ':' || collation_record.collencoding::text
                       END,
                       ',' ORDER BY indexed_collation.ordinality
                     )
                FROM unnest(index_record.indcollation) WITH ORDINALITY AS indexed_collation(collation_oid, ordinality)
                LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = indexed_collation.collation_oid
                LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace)
             = 'pg_catalog.default:d:true:-1'
         AND (SELECT array_agg(attribute.attname::text ORDER BY key_column.ordinality)
                FROM unnest(index_record.indkey) WITH ORDINALITY AS key_column(attnum, ordinality)
                JOIN pg_catalog.pg_attribute AS attribute
                  ON attribute.attrelid = target_oid AND attribute.attnum = key_column.attnum)
             = ARRAY['resource_name']::text[]
     ) THEN
    RAISE EXCEPTION 'snapshot audit table index fingerprint drifted';
  END IF;
  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid = target_oid AND NOT tgisinternal
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_rewrite WHERE ev_class = target_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_policy WHERE polrelid = target_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_inherits WHERE inhrelid = target_oid OR inhparent = target_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_attribute
       WHERE attrelid = target_oid AND attnum > 0 AND attacl IS NOT NULL
     ) THEN
    RAISE EXCEPTION 'snapshot audit table has executable or column-ACL drift';
  END IF;
	  IF EXISTS (
	    SELECT 1 FROM pg_catalog.aclexplode(
	      COALESCE((SELECT relacl FROM pg_catalog.pg_class WHERE oid = target_oid), pg_catalog.acldefault('r', owner_oid))
	    ) AS privilege
	    WHERE privilege.grantee <> owner_oid OR privilege.grantor <> owner_oid
	  ) THEN
    RAISE EXCEPTION 'snapshot audit table ACL drifted';
  END IF;
END
$r1_audit_table$`,
		quoteLiteral(schema),
		quoteLiteral(snapshotAuditTable),
		quoteLiteral(createSQL),
		quoteLiteral("COMMENT ON TABLE "+target+" IS "+quoteLiteral(marker)),
		quoteLiteral("REVOKE ALL ON TABLE "+target+" FROM PUBLIC"),
		quoteLiteral(target),
		quoteLiteral(marker),
		expectedColumns,
		quoteLiteral(owner),
		quoteLiteral("ALTER TABLE "+target+" OWNER TO "+quoteIdentifier(owner)),
	))
}

func reconcileSnapshotTable(resource snapshotResource, schema, owner, runtime string) Statement {
	target := qualified(schema, resource.Name)
	source := qualified(stage.LegacySchema, resource.Name)
	marker := "mss-shop-reconciler:" + stage.Environment + ":snapshot-table:" + schema + "." + resource.Name + ":" + snapshotPlanVersion
	createSQL := snapshotCreateTableSQL(resource, target)
	createIndexesSQL := snapshotCreateIndexesBlock(resource, target)
	expectedColumns := sqlTextArray(snapshotColumnFingerprints(resource))
	expectedIndexes := sqlTextArray(snapshotIndexFingerprints(resource))
	expectedPrimaryKey := sqlTextArray(resource.PrimaryKey)
	return plain("reconcile-snapshot-table-"+resource.Name, fmt.Sprintf(`DO $r1_snapshot_table$
DECLARE
  target_oid oid;
  source_binding text := %[1]s;
	  owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %[15]s);
	  runtime_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %[2]s);
  target_columns text[];
  target_indexes text[];
BEGIN
  SELECT relation.oid INTO target_oid
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = %[3]s AND relation.relname = %[4]s;
  IF target_oid IS NULL THEN
	    EXECUTE %[5]s;
%[11]s
	    EXECUTE %[6]s;
	    EXECUTE %[16]s;
	    EXECUTE %[7]s;
	    EXECUTE %[8]s;
    SELECT relation.oid INTO target_oid
      FROM pg_catalog.pg_class AS relation
      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
      WHERE namespace.nspname = %[3]s AND relation.relname = %[4]s;
  ELSE
    EXECUTE 'LOCK TABLE ' || %[9]s || ' IN ACCESS SHARE MODE';
    SELECT relation.oid INTO target_oid
      FROM pg_catalog.pg_class AS relation
      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
      WHERE namespace.nspname = %[3]s AND relation.relname = %[4]s;
  END IF;
  IF NOT EXISTS (
       SELECT 1 FROM pg_catalog.pg_class
       WHERE oid = target_oid AND relkind = 'r' AND relpersistence = 'p'
	         AND relowner = owner_oid AND NOT relrowsecurity AND NOT relforcerowsecurity
         AND NOT relispartition AND NOT relhasrules AND NOT relhastriggers
         AND reltablespace = 0 AND relreplident = 'd' AND reloptions IS NULL
         AND (SELECT access_method.amname FROM pg_catalog.pg_am AS access_method WHERE access_method.oid = relam) = 'heap'
     )
     OR pg_catalog.obj_description(target_oid, 'pg_class') IS DISTINCT FROM %[10]s THEN
	    RAISE EXCEPTION 'refusing to adopt unmanaged or foreign-owned snapshot table %%', %[4]s;
  END IF;

  SELECT array_agg(
           attribute.attname || '|' || pg_catalog.format_type(attribute.atttypid, attribute.atttypmod)
           || '|' || attribute.attnotnull::text || '|' || attribute.attidentity::text
           || '|' || attribute.attgenerated::text || '|' || COALESCE(
             collation_namespace.nspname || '.' || collation_record.collname || ':'
             || collation_record.collprovider::text || ':' || collation_record.collisdeterministic::text
             || ':' || collation_record.collencoding::text,
             ''
           )
           || '|' || COALESCE(pg_catalog.pg_get_expr(default_value.adbin, attribute.attrelid), '')
           || '|' || attribute.attstorage::text || '|' || attribute.attcompression::text
           ORDER BY attribute.attnum
         ) INTO target_columns
    FROM pg_catalog.pg_attribute AS attribute
    LEFT JOIN pg_catalog.pg_attrdef AS default_value
      ON default_value.adrelid = attribute.attrelid AND default_value.adnum = attribute.attnum
    LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = attribute.attcollation
    LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace
    WHERE attribute.attrelid = target_oid AND attribute.attnum > 0 AND NOT attribute.attisdropped;
  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_attribute
       WHERE attrelid = target_oid AND attnum > 0 AND attisdropped
     ) OR target_columns IS DISTINCT FROM %[12]s THEN
    RAISE EXCEPTION 'snapshot table %% column fingerprint drifted', %[4]s;
  END IF;

  SELECT array_agg(fingerprint ORDER BY fingerprint) INTO target_indexes
    FROM (
      SELECT indexFingerprint.indisprimary::text || '|'
             || indexFingerprint.indisunique::text || '|'
             || indexFingerprint.amname || '|'
             || indexFingerprint.index_relkind::text || '|'
             || indexFingerprint.column_names || '|'
             || indexFingerprint.opclass_names || '|'
             || indexFingerprint.collation_names || '|'
             || indexFingerprint.expressions_absent::text || '|'
             || indexFingerprint.predicate_absent::text || '|'
             || indexFingerprint.indisvalid::text || '|'
             || indexFingerprint.indisready::text || '|'
             || indexFingerprint.indislive::text || '|'
             || indexFingerprint.indisclustered::text || '|'
             || indexFingerprint.indcheckxmin::text || '|'
             || indexFingerprint.indisreplident::text || '|'
             || indexFingerprint.indisexclusion::text || '|'
             || indexFingerprint.indimmediate::text || '|'
             || indexFingerprint.indoption::text || '|'
             || indexFingerprint.indnullsnotdistinct::text || '|'
	             || indexFingerprint.owner_is_compatibility::text || '|'
             || indexFingerprint.index_persistence::text || '|'
             || indexFingerprint.index_tablespace::text || '|'
             || indexFingerprint.index_options || '|'
             || indexFingerprint.indnkeyatts::text || '|'
             || indexFingerprint.indnatts::text AS fingerprint
      FROM (
        SELECT index_record.indisprimary, index_record.indisunique, access_method.amname,
               index_relation.relkind AS index_relkind,
               index_record.indexprs IS NULL AS expressions_absent,
               index_record.indpred IS NULL AS predicate_absent,
               index_record.indisvalid, index_record.indisready, index_record.indislive,
               index_record.indisclustered, index_record.indcheckxmin,
               index_record.indisreplident, index_record.indisexclusion, index_record.indimmediate,
               index_record.indoption, index_record.indnullsnotdistinct,
	               index_relation.relowner = owner_oid AS owner_is_compatibility,
               index_relation.relpersistence AS index_persistence,
               index_relation.reltablespace AS index_tablespace,
               COALESCE(array_to_string(index_relation.reloptions, ','), '') AS index_options,
               index_record.indnkeyatts, index_record.indnatts,
               (SELECT string_agg(attribute.attname, ',' ORDER BY key_column.ordinality)
                  FROM unnest(index_record.indkey) WITH ORDINALITY AS key_column(attnum, ordinality)
                  JOIN pg_catalog.pg_attribute AS attribute
                    ON attribute.attrelid = index_record.indrelid AND attribute.attnum = key_column.attnum) AS column_names,
               (SELECT string_agg(operator_namespace.nspname || '.' || operator_class.opcname, ',' ORDER BY indexed_class.ordinality)
                  FROM unnest(index_record.indclass) WITH ORDINALITY AS indexed_class(opclass_oid, ordinality)
                  JOIN pg_catalog.pg_opclass AS operator_class ON operator_class.oid = indexed_class.opclass_oid
                  JOIN pg_catalog.pg_namespace AS operator_namespace ON operator_namespace.oid = operator_class.opcnamespace) AS opclass_names,
               (SELECT string_agg(
                         CASE WHEN indexed_collation.collation_oid = 0 THEN '' ELSE
                           collation_namespace.nspname || '.' || collation_record.collname || ':'
                           || collation_record.collprovider::text || ':' || collation_record.collisdeterministic::text
                           || ':' || collation_record.collencoding::text
                         END,
                         ',' ORDER BY indexed_collation.ordinality
                       )
                  FROM unnest(index_record.indcollation) WITH ORDINALITY AS indexed_collation(collation_oid, ordinality)
                  LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = indexed_collation.collation_oid
                  LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace) AS collation_names
          FROM pg_catalog.pg_index AS index_record
          JOIN pg_catalog.pg_class AS index_relation ON index_relation.oid = index_record.indexrelid
          JOIN pg_catalog.pg_am AS access_method ON access_method.oid = index_relation.relam
         WHERE index_record.indrelid = target_oid
      ) AS indexFingerprint
    ) AS fingerprints;
  IF target_indexes IS DISTINCT FROM %[13]s THEN
    RAISE EXCEPTION 'snapshot table %% index fingerprint drifted', %[4]s;
  END IF;
  IF (SELECT count(*) FROM pg_catalog.pg_constraint WHERE conrelid = target_oid) <> 1
     OR NOT EXISTS (
       SELECT 1
       FROM pg_catalog.pg_constraint AS primary_constraint
       WHERE primary_constraint.conrelid = target_oid AND primary_constraint.contype = 'p'
         AND (SELECT array_agg(attribute.attname::text ORDER BY key_column.ordinality)
                FROM unnest(primary_constraint.conkey) WITH ORDINALITY AS key_column(attnum, ordinality)
                JOIN pg_catalog.pg_attribute AS attribute
                  ON attribute.attrelid = target_oid AND attribute.attnum = key_column.attnum)
             = %[14]s
     ) THEN
    RAISE EXCEPTION 'snapshot table %% constraint fingerprint drifted', %[4]s;
  END IF;
  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid = target_oid AND NOT tgisinternal
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_rewrite WHERE ev_class = target_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_policy WHERE polrelid = target_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_inherits WHERE inhrelid = target_oid OR inhparent = target_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_attribute
       WHERE attrelid = target_oid AND attnum > 0 AND attacl IS NOT NULL
     ) THEN
    RAISE EXCEPTION 'snapshot table %% has executable or column-ACL drift', %[4]s;
  END IF;
	  IF EXISTS (
	    SELECT 1 FROM pg_catalog.aclexplode(
	      COALESCE((SELECT relacl FROM pg_catalog.pg_class WHERE oid = target_oid), pg_catalog.acldefault('r', owner_oid))
	    ) AS privilege
	    WHERE (privilege.grantee = runtime_oid AND (
	             privilege.privilege_type <> 'SELECT' OR privilege.is_grantable OR privilege.grantor <> owner_oid
	          ))
	       OR privilege.grantee NOT IN (owner_oid, runtime_oid)
  ) OR NOT pg_catalog.has_table_privilege(%[2]s, target_oid, 'SELECT') THEN
    RAISE EXCEPTION 'snapshot table %% ACL drifted', %[4]s;
  END IF;
END
$r1_snapshot_table$`,
		quoteLiteral(source),
		quoteLiteral(runtime),
		quoteLiteral(schema),
		quoteLiteral(resource.Name),
		quoteLiteral(createSQL),
		quoteLiteral("COMMENT ON TABLE "+target+" IS "+quoteLiteral(marker)),
		quoteLiteral("REVOKE ALL ON TABLE "+target+" FROM PUBLIC"),
		quoteLiteral("GRANT SELECT ON TABLE "+target+" TO "+quoteIdentifier(runtime)),
		quoteLiteral(target),
		quoteLiteral(marker),
		createIndexesSQL,
		expectedColumns,
		expectedIndexes,
		expectedPrimaryKey,
		quoteLiteral(owner),
		quoteLiteral("ALTER TABLE "+target+" OWNER TO "+quoteIdentifier(owner)),
	))
}

func validateSnapshotSource(resource snapshotResource) Statement {
	source := qualified(stage.LegacySchema, resource.Name)
	expectedColumns := sqlTextArray(snapshotColumnFingerprints(resource))
	expectedIndexes := sqlTextArray(snapshotIndexFingerprints(resource))
	expectedPrimaryKey := sqlTextArray(resource.PrimaryKey)
	return plain("validate-snapshot-source-"+resource.Name, fmt.Sprintf(`DO $r1_snapshot_source$
DECLARE
  source_oid oid := %[1]s::regclass;
  bootstrap_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user);
  actual_columns text[];
  actual_indexes text[];
BEGIN
  IF NOT EXISTS (
       SELECT 1
       FROM pg_catalog.pg_class AS relation
       JOIN pg_catalog.pg_am AS access_method ON access_method.oid = relation.relam
       WHERE relation.oid = source_oid AND relation.relkind = 'r' AND relation.relpersistence = 'p'
         AND relation.relowner = bootstrap_oid AND access_method.amname = 'heap'
         AND relation.reltablespace = 0 AND relation.relreplident = 'd'
         AND NOT relation.relispartition AND NOT relation.relrowsecurity AND NOT relation.relforcerowsecurity
         AND NOT relation.relhasrules AND NOT relation.relhastriggers AND relation.reloptions IS NULL
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_inherits
       WHERE inhrelid = source_oid OR inhparent = source_oid
     ) THEN
    RAISE EXCEPTION 'legacy snapshot source %% has an unexpected table/owner/storage shape', %[2]s;
  END IF;

  SELECT array_agg(
           attribute.attname || '|' || pg_catalog.format_type(attribute.atttypid, attribute.atttypmod)
           || '|' || attribute.attnotnull::text || '|' || attribute.attidentity::text
           || '|' || attribute.attgenerated::text || '|' || COALESCE(
             collation_namespace.nspname || '.' || collation_record.collname || ':'
             || collation_record.collprovider::text || ':' || collation_record.collisdeterministic::text
             || ':' || collation_record.collencoding::text,
             ''
           )
           || '|' || COALESCE(pg_catalog.pg_get_expr(default_value.adbin, attribute.attrelid), '')
           || '|' || attribute.attstorage::text || '|' || attribute.attcompression::text
           ORDER BY attribute.attnum
         ) INTO actual_columns
    FROM pg_catalog.pg_attribute AS attribute
    LEFT JOIN pg_catalog.pg_attrdef AS default_value
      ON default_value.adrelid = attribute.attrelid AND default_value.adnum = attribute.attnum
    LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = attribute.attcollation
    LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace
    WHERE attribute.attrelid = source_oid AND attribute.attnum > 0 AND NOT attribute.attisdropped;
  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_attribute
       WHERE attrelid = source_oid AND attnum > 0 AND attisdropped
     ) OR actual_columns IS DISTINCT FROM %[3]s THEN
    RAISE EXCEPTION 'legacy snapshot source %% column fingerprint drifted', %[2]s;
  END IF;

  SELECT array_agg(fingerprint ORDER BY fingerprint) INTO actual_indexes
    FROM (
      SELECT indexFingerprint.indisprimary::text || '|'
             || indexFingerprint.indisunique::text || '|'
             || indexFingerprint.amname || '|'
             || indexFingerprint.index_relkind::text || '|'
             || indexFingerprint.column_names || '|'
             || indexFingerprint.opclass_names || '|'
             || indexFingerprint.collation_names || '|'
             || indexFingerprint.expressions_absent::text || '|'
             || indexFingerprint.predicate_absent::text || '|'
             || indexFingerprint.indisvalid::text || '|'
             || indexFingerprint.indisready::text || '|'
             || indexFingerprint.indislive::text || '|'
             || indexFingerprint.indisclustered::text || '|'
             || indexFingerprint.indcheckxmin::text || '|'
             || indexFingerprint.indisreplident::text || '|'
             || indexFingerprint.indisexclusion::text || '|'
             || indexFingerprint.indimmediate::text || '|'
             || indexFingerprint.indoption::text || '|'
             || indexFingerprint.indnullsnotdistinct::text || '|'
             || indexFingerprint.owner_is_bootstrap::text || '|'
             || indexFingerprint.index_persistence::text || '|'
             || indexFingerprint.index_tablespace::text || '|'
             || indexFingerprint.index_options || '|'
             || indexFingerprint.indnkeyatts::text || '|'
             || indexFingerprint.indnatts::text AS fingerprint
      FROM (
        SELECT index_record.indisprimary, index_record.indisunique, access_method.amname,
               index_relation.relkind AS index_relkind,
               index_record.indexprs IS NULL AS expressions_absent,
               index_record.indpred IS NULL AS predicate_absent,
               index_record.indisvalid, index_record.indisready, index_record.indislive,
               index_record.indisclustered, index_record.indcheckxmin,
               index_record.indisreplident, index_record.indisexclusion, index_record.indimmediate,
               index_record.indoption, index_record.indnullsnotdistinct,
               index_relation.relowner = bootstrap_oid AS owner_is_bootstrap,
               index_relation.relpersistence AS index_persistence,
               index_relation.reltablespace AS index_tablespace,
               COALESCE(array_to_string(index_relation.reloptions, ','), '') AS index_options,
               index_record.indnkeyatts, index_record.indnatts,
               (SELECT string_agg(attribute.attname, ',' ORDER BY key_column.ordinality)
                  FROM unnest(index_record.indkey) WITH ORDINALITY AS key_column(attnum, ordinality)
                  JOIN pg_catalog.pg_attribute AS attribute
                    ON attribute.attrelid = index_record.indrelid AND attribute.attnum = key_column.attnum) AS column_names,
               (SELECT string_agg(operator_namespace.nspname || '.' || operator_class.opcname, ',' ORDER BY indexed_class.ordinality)
                  FROM unnest(index_record.indclass) WITH ORDINALITY AS indexed_class(opclass_oid, ordinality)
                  JOIN pg_catalog.pg_opclass AS operator_class ON operator_class.oid = indexed_class.opclass_oid
                  JOIN pg_catalog.pg_namespace AS operator_namespace ON operator_namespace.oid = operator_class.opcnamespace) AS opclass_names,
               (SELECT string_agg(
                         CASE WHEN indexed_collation.collation_oid = 0 THEN '' ELSE
                           collation_namespace.nspname || '.' || collation_record.collname || ':'
                           || collation_record.collprovider::text || ':' || collation_record.collisdeterministic::text
                           || ':' || collation_record.collencoding::text
                         END,
                         ',' ORDER BY indexed_collation.ordinality
                       )
                  FROM unnest(index_record.indcollation) WITH ORDINALITY AS indexed_collation(collation_oid, ordinality)
                  LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = indexed_collation.collation_oid
                  LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace) AS collation_names
          FROM pg_catalog.pg_index AS index_record
          JOIN pg_catalog.pg_class AS index_relation ON index_relation.oid = index_record.indexrelid
          JOIN pg_catalog.pg_am AS access_method ON access_method.oid = index_relation.relam
         WHERE index_record.indrelid = source_oid
      ) AS indexFingerprint
    ) AS fingerprints;
  IF actual_indexes IS DISTINCT FROM %[4]s THEN
    RAISE EXCEPTION 'legacy snapshot source %% index fingerprint drifted', %[2]s;
  END IF;
  IF (SELECT count(*) FROM pg_catalog.pg_constraint WHERE conrelid = source_oid) <> 1
     OR NOT EXISTS (
       SELECT 1
       FROM pg_catalog.pg_constraint AS primary_constraint
       WHERE primary_constraint.conrelid = source_oid AND primary_constraint.contype = 'p'
         AND (SELECT array_agg(attribute.attname::text ORDER BY key_column.ordinality)
                FROM unnest(primary_constraint.conkey) WITH ORDINALITY AS key_column(attnum, ordinality)
                JOIN pg_catalog.pg_attribute AS attribute
                  ON attribute.attrelid = source_oid AND attribute.attnum = key_column.attnum)
             = %[5]s
     ) THEN
    RAISE EXCEPTION 'legacy snapshot source %% constraint fingerprint drifted', %[2]s;
  END IF;
  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid = source_oid AND NOT tgisinternal
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_rewrite WHERE ev_class = source_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_policy WHERE polrelid = source_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_attribute
       WHERE attrelid = source_oid AND attnum > 0 AND attacl IS NOT NULL
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_depend AS dependency
       JOIN pg_catalog.pg_class AS sequence_relation ON sequence_relation.oid = dependency.objid
       WHERE dependency.refobjid = source_oid AND sequence_relation.relkind = 'S'
         AND dependency.deptype IN ('a','i')
     ) THEN
    RAISE EXCEPTION 'legacy snapshot source %% has executable, ACL, or sequence drift', %[2]s;
  END IF;
END
$r1_snapshot_source$`,
		quoteLiteral(source),
		quoteLiteral(resource.Name),
		expectedColumns,
		expectedIndexes,
		expectedPrimaryKey,
	))
}

func snapshotCreateTableSQL(resource snapshotResource, target string) string {
	definitions := make([]string, 0, len(resource.Columns)+1)
	for _, column := range resource.Columns {
		definition := quoteIdentifier(column.Name) + " " + column.Type
		if column.NotNull {
			definition += " NOT NULL"
		}
		definitions = append(definitions, definition)
	}
	primary := make([]string, 0, len(resource.PrimaryKey))
	for _, column := range resource.PrimaryKey {
		primary = append(primary, quoteIdentifier(column))
	}
	definitions = append(definitions, "PRIMARY KEY ("+strings.Join(primary, ", ")+")")
	return "CREATE TABLE " + target + " (" + strings.Join(definitions, ", ") + ") USING heap TABLESPACE pg_default"
}

func snapshotCreateIndexesBlock(resource snapshotResource, target string) string {
	var builder strings.Builder
	for _, indexColumns := range resource.Indexes {
		quotedColumns := make([]string, 0, len(indexColumns))
		for _, column := range indexColumns {
			quotedColumns = append(quotedColumns, quoteIdentifier(column))
		}
		indexName := "r1_snapshot_" + resource.Name + "_" + strings.Join(indexColumns, "_") + "_idx"
		command := "CREATE INDEX " + quoteIdentifier(indexName) + " ON " + target + " USING btree (" + strings.Join(quotedColumns, ", ") + ") TABLESPACE pg_default"
		builder.WriteString("    EXECUTE ")
		builder.WriteString(quoteLiteral(command))
		builder.WriteString(";\n")
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func snapshotColumnFingerprints(resource snapshotResource) []string {
	result := make([]string, 0, len(resource.Columns))
	for _, column := range resource.Columns {
		result = append(result, strings.Join([]string{
			column.Name,
			column.Type,
			fmt.Sprint(column.NotNull),
			"", // identity
			"", // generated
			snapshotCollationFingerprint(column.Collation),
			"", // default
			snapshotStorage(column.Type),
			"", // compression
		}, "|"))
	}
	return result
}

func auditColumnFingerprints() []string {
	columns := []struct {
		name       string
		dataType   string
		collation  string
		defaultSQL string
		storage    string
	}{
		{name: "resource_name", dataType: "text", collation: "default", storage: "x"},
		{name: "source_schema", dataType: "text", collation: "default", storage: "x"},
		{name: "source_relation", dataType: "text", collation: "default", storage: "x"},
		{name: "plan_version", dataType: "text", collation: "default", storage: "x"},
		{name: "source_row_count", dataType: "bigint", storage: "p"},
		{name: "target_row_count", dataType: "bigint", storage: "p"},
		{name: "source_key_hash", dataType: "text", collation: "default", storage: "x"},
		{name: "target_key_hash", dataType: "text", collation: "default", storage: "x"},
		{name: "source_row_hash", dataType: "text", collation: "default", storage: "x"},
		{name: "target_row_hash", dataType: "text", collation: "default", storage: "x"},
		{name: "completed_at", dataType: "timestamp with time zone", defaultSQL: "clock_timestamp()", storage: "p"},
	}
	result := make([]string, 0, len(columns))
	for _, column := range columns {
		result = append(result, strings.Join([]string{
			column.name,
			column.dataType,
			"true", // not null
			"",     // identity
			"",     // generated
			snapshotCollationFingerprint(column.collation),
			column.defaultSQL,
			column.storage,
			"", // compression
		}, "|"))
	}
	return result
}

func snapshotIndexFingerprints(resource snapshotResource) []string {
	indexes := make([][]string, 0, len(resource.Indexes)+1)
	indexes = append(indexes, resource.PrimaryKey)
	indexes = append(indexes, resource.Indexes...)
	result := make([]string, 0, len(indexes))
	for index, columns := range indexes {
		opclasses := make([]string, 0, len(columns))
		collations := make([]string, 0, len(columns))
		for _, name := range columns {
			column := snapshotColumnByName(resource, name)
			opclasses = append(opclasses, snapshotIndexOpclass(column.Type))
			collations = append(collations, snapshotCollationFingerprint(column.Collation))
		}
		primary := index == 0
		result = append(result, strings.Join([]string{
			fmt.Sprint(primary), fmt.Sprint(primary), "btree", "i",
			strings.Join(columns, ","), strings.Join(opclasses, ","), strings.Join(collations, ","),
			"true", "true", "true", "true", "true", "false", "false", "false", "false", "true",
			strings.TrimSpace(strings.Repeat("0 ", len(columns))), "false", "true", "p", "0", "",
			fmt.Sprint(len(columns)), fmt.Sprint(len(columns)),
		}, "|"))
	}
	sort.Strings(result)
	return result
}

func snapshotColumnByName(resource snapshotResource, name string) snapshotColumn {
	for _, column := range resource.Columns {
		if column.Name == name {
			return column
		}
	}
	panic("snapshot index references an unknown compiled column")
}

func snapshotStorage(dataType string) string {
	switch {
	case dataType == "numeric(10,2)":
		return "m"
	case dataType == "text", dataType == "json", dataType == "bytea", strings.HasPrefix(dataType, "character varying"):
		return "x"
	default:
		return "p"
	}
}

func snapshotIndexOpclass(dataType string) string {
	if dataType == "timestamp with time zone" {
		return "pg_catalog.timestamptz_ops"
	}
	return "pg_catalog.text_ops"
}

func snapshotCollationFingerprint(collation string) string {
	if collation == "" {
		return ""
	}
	if collation != "default" {
		panic("snapshot column references an unsupported compiled collation")
	}
	return "pg_catalog.default:d:true:-1"
}

func sqlTextArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quoteLiteral(value))
	}
	return "ARRAY[" + strings.Join(quoted, ", ") + "]::text[]"
}

func buildSnapshotRelationsBatch(schemas stage.Schemas) Batch {
	biz := schemas.MallBusiness
	checks := []string{
		orphanCheck(biz, "categories", "parent_id", "categories", "id"),
		orphanCheck(biz, "classes", "category_id", "categories", "id"),
		orphanCheck(biz, "goods_infos", "category_id", "categories", "id"),
		orphanCheck(biz, "goods_infos", "parent_category_id", "categories", "id"),
		orphanCheck(biz, "goods_infos", "brand_id", "brands", "id"),
		orphanCheck(biz, "courier_pack_rules", "courier_id", "couriers", "id"),
		orphanCheck(biz, "courier_links", "left_rule_id", "courier_pack_rules", "id"),
	}
	return Batch{Name: "snapshot-relations", Statements: []Statement{
		plain("verify-snapshot-relations", fmt.Sprintf(`DO $snapshot_relations$
BEGIN
  IF %s THEN
    RAISE EXCEPTION 'tenant catalog/logistics snapshot contains orphaned relationships';
  END IF;
END
$snapshot_relations$`, strings.Join(checks, " OR "))),
		plain("verify-snapshot-known-graph-profile", snapshotGraphProfileCheck(biz)),
	}}
}

func snapshotGraphProfileCheck(schema string) string {
	categories := qualified(schema, "categories")
	goodsInfos := qualified(schema, "goods_infos")
	links := qualified(schema, "courier_links")
	return fmt.Sprintf(`DO $snapshot_graph_profile$
DECLARE
  link_rows bigint;
  unmatched_link_subjects bigint;
  dual_link_subjects bigint;
  distinct_mixable_ids bigint;
  orphan_mixable_ids bigint;
  orphan_mixable_occurrences bigint;
BEGIN
  SELECT count(*),
         count(*) FILTER (WHERE NOT EXISTS (
           SELECT 1 FROM %[1]s AS category WHERE category.id::text = link.link_id::text
         ) AND NOT EXISTS (
           SELECT 1 FROM %[2]s AS goods_info WHERE goods_info.id::text = link.link_id::text
         )),
         count(*) FILTER (WHERE EXISTS (
           SELECT 1 FROM %[1]s AS category WHERE category.id::text = link.link_id::text
         ) AND EXISTS (
           SELECT 1 FROM %[2]s AS goods_info WHERE goods_info.id::text = link.link_id::text
         ))
    INTO link_rows, unmatched_link_subjects, dual_link_subjects
    FROM %[3]s AS link;

  WITH mixable AS (
    SELECT btrim(value) AS object_id
    FROM %[3]s AS link
    CROSS JOIN LATERAL regexp_split_to_table(COALESCE(link.object_ids_data::text, ''), ',') AS value
    WHERE btrim(value) <> ''
  )
  SELECT count(DISTINCT object_id),
         count(DISTINCT object_id) FILTER (WHERE NOT EXISTS (
           SELECT 1 FROM %[1]s AS category WHERE category.id::text = mixable.object_id
         )),
         count(*) FILTER (WHERE NOT EXISTS (
           SELECT 1 FROM %[1]s AS category WHERE category.id::text = mixable.object_id
         ))
    INTO distinct_mixable_ids, orphan_mixable_ids, orphan_mixable_occurrences
    FROM mixable;

  IF link_rows <> 325
     OR unmatched_link_subjects <> 5
     OR dual_link_subjects <> 0
     OR distinct_mixable_ids <> 22
     OR orphan_mixable_ids <> 1
     OR orphan_mixable_occurrences <> 1 THEN
    RAISE EXCEPTION 'catalog/logistics snapshot graph profile changed; review isolation before continuing';
  END IF;

  IF EXISTS (
       SELECT 1 FROM %[1]s
       WHERE pack_rule IS NULL OR jsonb_typeof(pack_rule::jsonb) <> 'array'
     ) OR EXISTS (
       SELECT 1 FROM %[2]s
       WHERE pack_rule IS NULL OR jsonb_typeof(pack_rule::jsonb) <> 'array'
     ) THEN
    RAISE EXCEPTION 'catalog/logistics pack_rule JSON shape changed';
  END IF;
END
$snapshot_graph_profile$`, categories, goodsInfos, links)
}

func buildRuntimeGrantsBatch(schemas stage.Schemas, roles stage.Roles) Batch {
	// Core objects are created by the MSS migrator after this plan and inherit
	// the exact defaults established above. Never blanket-GRANT pre-existing
	// objects: a marked schema may still have been populated out of band.
	// Each object creation path establishes its exact read ACL and every retry
	// validates it without issuing catalog writes. This terminal batch is
	// assertions only; it must not repeat 51 semantically redundant GRANTs.
	tenantReadOnlyObjects := []string{tenantSharedResource + ":v"}
	mallReadOnlyObjects := make([]string, 0, len(mallLegacyViews)+len(mallSnapshots)+3)
	for _, name := range legacyViewNames() {
		mallReadOnlyObjects = append(mallReadOnlyObjects, name+":v")
	}
	for _, name := range snapshotNames() {
		mallReadOnlyObjects = append(mallReadOnlyObjects, name+":r")
	}
	mallReadOnlyObjects = append(mallReadOnlyObjects,
		mallSettingsPrivateView+":v",
		memberLevelsProjectionAuditView+":v",
		snapshotAuditTable+":r",
	)
	mallCompatibilityRelations := append(append(legacyViewNames(), snapshotNames()...),
		mallSettingsPrivateView,
		memberLevelsProjectionAuditView,
		snapshotAuditTable,
	)
	mallRuntimeRelations := append(append(legacyViewNames(), snapshotNames()...),
		mallSettingsPrivateView,
		memberLevelsProjectionAuditView,
	)
	statements := []Statement{
		validateCoreSchemaEmpty(schemas.TenantCore),
		validateCoreSchemaEmpty(schemas.MallCore),
		validateReadOnlySchemaObjectInventory(schemas.TenantShared, tenantReadOnlyObjects),
		validateReadOnlySchemaObjectInventory(schemas.MallBusiness, mallReadOnlyObjects),
		validateCompatibilityOwnerRealm(
			roles.TenantCompatibilityOwner,
			schemas.TenantShared,
			[]string{tenantSharedResource},
			tenantCompatibilitySourceNames(),
		),
		validateCompatibilityOwnerRealm(
			roles.MallCompatibilityOwner,
			schemas.MallBusiness,
			mallCompatibilityRelations,
			mallCompatibilitySourceNames(),
		),
		validateApplicationRoleRealm(roles.TenantMigrator, true, schemas.TenantCore, schemas.TenantShared, nil),
		validateApplicationRoleRealm(roles.TenantRuntime, false, schemas.TenantCore, schemas.TenantShared, []string{tenantSharedResource}),
		validateApplicationRoleRealm(roles.MallMigrator, true, schemas.MallCore, schemas.MallBusiness, nil),
		validateApplicationRoleRealm(roles.MallRuntime, false, schemas.MallCore, schemas.MallBusiness, mallRuntimeRelations),
	}
	statements = append(statements,
		validateManagedRoleACLs(
			"tenant",
			roles.TenantMigrator,
			roles.TenantRuntime,
			roles.TenantCompatibilityOwner,
			schemas.TenantCore,
			schemas.TenantShared,
			[]string{tenantSharedResource},
			[]string{tenantSharedResource},
		),
		validateManagedRoleACLs(
			"mall",
			roles.MallMigrator,
			roles.MallRuntime,
			roles.MallCompatibilityOwner,
			schemas.MallCore,
			schemas.MallBusiness,
			mallCompatibilityRelations,
			mallRuntimeRelations,
		),
		validateManagedSchemaACL(schemas.TenantCore, roles.TenantMigrator, roles.TenantRuntime),
		validateManagedSchemaACL(schemas.MallCore, roles.MallMigrator, roles.MallRuntime),
		validateCompatibilityOwnedSchemaACL(
			schemas.TenantShared,
			roles.TenantCompatibilityOwner,
			roles.TenantMigrator,
			roles.TenantRuntime,
		),
		validateCompatibilityOwnedSchemaACL(
			schemas.MallBusiness,
			roles.MallCompatibilityOwner,
			roles.MallMigrator,
			roles.MallRuntime,
		),
	)
	return Batch{Name: "runtime-least-privilege-grants", Statements: statements}
}

// validateCoreSchemaEmpty deliberately refuses to adopt an already-populated
// MSS core schema. This stage plan runs before the MSS 1.3.7 migrate init
// container. Supporting post-migration reconciliation requires a separately
// reviewed, versioned exact MSS object manifest; a generic owner/ACL check is
// not sufficient because defaults, checks and generated expressions execute.
func validateCoreSchemaEmpty(schema string) Statement {
	return plain("validate-empty-core-schema-"+schema, fmt.Sprintf(`DO $r1_empty_core$
DECLARE
  core_schema_oid oid := (SELECT oid FROM pg_catalog.pg_namespace WHERE nspname = %s);
BEGIN
  IF EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_type WHERE typnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_collation WHERE collnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_conversion WHERE connamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_operator WHERE oprnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_opclass WHERE opcnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_opfamily WHERE opfnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_ts_config WHERE cfgnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_ts_dict WHERE dictnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_ts_parser WHERE prsnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_ts_template WHERE tmplnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_statistic_ext WHERE stxnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_extension WHERE extnamespace = core_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_publication WHERE puballtables)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_publication_namespace WHERE pnnspid = core_schema_oid) THEN
    RAISE EXCEPTION 'MSS core schema is not empty; a versioned post-migration manifest is required';
  END IF;
END
$r1_empty_core$`, quoteLiteral(schema)))
}

// validateReadOnlySchemaObjectInventory closes the catalog surface that is not
// covered by the per-view and per-snapshot fingerprints. The only types
// allowed are PostgreSQL's automatically coupled row and array types for the
// reviewed relations. Standalone executable or extensible objects are refused.
func validateReadOnlySchemaObjectInventory(schema string, relationKeys []string) Statement {
	expected := append([]string(nil), relationKeys...)
	sort.Strings(expected)
	return plain("validate-readonly-schema-object-inventory-"+schema, fmt.Sprintf(`DO $mss_readonly_inventory$
DECLARE
  readonly_schema_oid oid := (SELECT oid FROM pg_catalog.pg_namespace WHERE nspname = %s);
  expected_relations text[] := %s;
BEGIN
  IF readonly_schema_oid IS NULL
     OR (SELECT array_agg(relation.relname || ':' || relation.relkind::text
                         ORDER BY relation.relname, relation.relkind)
           FROM pg_catalog.pg_class AS relation
          WHERE relation.relnamespace = readonly_schema_oid
            AND relation.relkind NOT IN ('i','I'))
        IS DISTINCT FROM expected_relations
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_class AS index_relation
       WHERE index_relation.relnamespace = readonly_schema_oid
         AND index_relation.relkind IN ('i','I')
         AND NOT EXISTS (
           SELECT 1
           FROM pg_catalog.pg_index AS index_record
           JOIN pg_catalog.pg_class AS parent_relation ON parent_relation.oid = index_record.indrelid
           WHERE index_record.indexrelid = index_relation.oid
             AND parent_relation.relnamespace = readonly_schema_oid
             AND parent_relation.relname || ':' || parent_relation.relkind::text = ANY (expected_relations)
         )
     )
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_type AS type_record
       WHERE type_record.typnamespace = readonly_schema_oid
         AND NOT (
           type_record.typtype = 'c'
           AND EXISTS (
             SELECT 1 FROM pg_catalog.pg_class AS relation
             WHERE relation.oid = type_record.typrelid
               AND relation.relnamespace = readonly_schema_oid
               AND relation.relname || ':' || relation.relkind::text = ANY (expected_relations)
           )
           OR type_record.typtype = 'b'
           AND type_record.typcategory = 'A'
           AND EXISTS (
             SELECT 1
             FROM pg_catalog.pg_type AS element_type
             JOIN pg_catalog.pg_class AS relation ON relation.oid = element_type.typrelid
             WHERE element_type.oid = type_record.typelem
               AND element_type.typarray = type_record.oid
               AND relation.relnamespace = readonly_schema_oid
               AND relation.relname || ':' || relation.relkind::text = ANY (expected_relations)
           )
         )
     )
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_constraint AS constraint_record
       WHERE constraint_record.connamespace = readonly_schema_oid
         AND NOT EXISTS (
           SELECT 1 FROM pg_catalog.pg_class AS relation
           WHERE relation.oid = constraint_record.conrelid
             AND relation.relnamespace = readonly_schema_oid
             AND relation.relname || ':' || relation.relkind::text = ANY (expected_relations)
         )
     )
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_collation WHERE collnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_conversion WHERE connamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_operator WHERE oprnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_opclass WHERE opcnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_opfamily WHERE opfnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_ts_config WHERE cfgnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_ts_dict WHERE dictnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_ts_parser WHERE prsnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_ts_template WHERE tmplnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_statistic_ext WHERE stxnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_extension WHERE extnamespace = readonly_schema_oid)
     OR EXISTS (SELECT 1 FROM pg_catalog.pg_publication_namespace WHERE pnnspid = readonly_schema_oid)
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_depend AS dependency
       CROSS JOIN LATERAL pg_catalog.pg_identify_object(
         dependency.classid, dependency.objid, dependency.objsubid
       ) AS identified(object_type, object_schema, object_name, object_identity)
       WHERE dependency.refclassid = 'pg_catalog.pg_extension'::regclass
         AND dependency.deptype = 'e'
         AND identified.object_schema = %s
     ) THEN
    RAISE EXCEPTION 'compatibility-owned read-only schema object inventory drifted';
  END IF;
END
$mss_readonly_inventory$`, quoteLiteral(schema), sqlTextArray(expected), quoteLiteral(schema)))
}

func validateCompatibilityOwnerRealm(owner, schema string, relations, sourceRelations []string) Statement {
	expectedRelations := append([]string(nil), relations...)
	sort.Strings(expectedRelations)
	expectedSourceRelations := append([]string(nil), sourceRelations...)
	sort.Strings(expectedSourceRelations)
	return plain("validate-compatibility-owner-realm-"+owner, fmt.Sprintf(`DO $mss_compat_owner_realm$
DECLARE
  owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %[1]s);
  target_schema_oid oid := (SELECT oid FROM pg_catalog.pg_namespace WHERE nspname = %[2]s);
  expected_relations text[] := %[3]s;
  source_schema_oid oid := (SELECT oid FROM pg_catalog.pg_namespace WHERE nspname = 'public');
  expected_source_relations text[] := %[4]s;
  bootstrap_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user);
  current_database_oid oid := (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database());
BEGIN
  IF owner_oid IS NULL OR target_schema_oid IS NULL
     OR NOT EXISTS (
       SELECT 1 FROM pg_catalog.pg_namespace
       WHERE oid = target_schema_oid AND nspowner = owner_oid
     )
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_database AS database
       WHERE database.datallowconn
         AND (
           pg_catalog.has_database_privilege(%[1]s, database.oid, 'CONNECT')
           OR pg_catalog.has_database_privilege(%[1]s, database.oid, 'CREATE')
           OR pg_catalog.has_database_privilege(%[1]s, database.oid, 'TEMP')
         )
     )
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_namespace
       WHERE nspowner = owner_oid AND oid <> target_schema_oid
     )
     OR pg_catalog.has_schema_privilege(%[1]s, 'public', 'CREATE') THEN
    RAISE EXCEPTION 'compatibility owner crosses its database or schema realm';
  END IF;

  IF EXISTS (
       SELECT 1
       FROM pg_catalog.pg_database AS database
       CROSS JOIN LATERAL pg_catalog.aclexplode(database.datacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_tablespace AS tablespace
       CROSS JOIN LATERAL pg_catalog.aclexplode(tablespace.spcacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_namespace AS namespace
       CROSS JOIN LATERAL pg_catalog.aclexplode(namespace.nspacl) AS privilege
       WHERE privilege.grantee = owner_oid
         AND namespace.oid NOT IN (target_schema_oid, source_schema_oid)
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_class AS relation
       JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
       CROSS JOIN LATERAL pg_catalog.aclexplode(relation.relacl) AS privilege
       WHERE privilege.grantee = owner_oid
         AND NOT (
           namespace.oid = target_schema_oid
           AND relation.relowner = owner_oid
           AND relation.relname = ANY (expected_relations)
           AND relation.relkind IN ('r','v')
           OR namespace.oid = source_schema_oid
           AND relation.relname = ANY (expected_source_relations)
           AND relation.relkind = 'r'
           AND privilege.grantor = bootstrap_oid
           AND privilege.privilege_type = 'SELECT'
           AND NOT privilege.is_grantable
         )
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_attribute AS attribute
       CROSS JOIN LATERAL pg_catalog.aclexplode(attribute.attacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_proc AS routine
       CROSS JOIN LATERAL pg_catalog.aclexplode(routine.proacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_type AS type_record
       CROSS JOIN LATERAL pg_catalog.aclexplode(type_record.typacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_language AS language
       CROSS JOIN LATERAL pg_catalog.aclexplode(language.lanacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_foreign_data_wrapper AS wrapper
       CROSS JOIN LATERAL pg_catalog.aclexplode(wrapper.fdwacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_foreign_server AS server
       CROSS JOIN LATERAL pg_catalog.aclexplode(server.srvacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_largeobject_metadata AS large_object
       CROSS JOIN LATERAL pg_catalog.aclexplode(large_object.lomacl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_parameter_acl AS parameter_acl
       CROSS JOIN LATERAL pg_catalog.aclexplode(parameter_acl.paracl) AS privilege
       WHERE privilege.grantee = owner_oid
     ) THEN
    RAISE EXCEPTION 'compatibility owner has a direct ACL outside its exact realm';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    WHERE relation.relowner = owner_oid
      AND NOT (
        relation.relnamespace = target_schema_oid
        AND relation.relname = ANY (expected_relations)
        AND relation.relkind IN ('r','v')
	        OR relation.relkind = 't'
	        AND EXISTS (
	          SELECT 1 FROM pg_catalog.pg_class AS parent_relation
	          WHERE parent_relation.reltoastrelid = relation.oid
	            AND parent_relation.relnamespace = target_schema_oid
	            AND parent_relation.relname = ANY (expected_relations)
	            AND parent_relation.relkind = 'r'
	        )
        OR relation.relkind = 'i'
        AND EXISTS (
          SELECT 1
          FROM pg_catalog.pg_index AS index_record
          JOIN pg_catalog.pg_class AS parent_relation ON parent_relation.oid = index_record.indrelid
          WHERE index_record.indexrelid = relation.oid
	            AND (
	              parent_relation.relnamespace = target_schema_oid
	              AND parent_relation.relname = ANY (expected_relations)
	              AND parent_relation.relkind = 'r'
	              OR parent_relation.relkind = 't'
	              AND EXISTS (
	                SELECT 1 FROM pg_catalog.pg_class AS base_relation
	                WHERE base_relation.reltoastrelid = parent_relation.oid
	                  AND base_relation.relnamespace = target_schema_oid
	                  AND base_relation.relname = ANY (expected_relations)
	                  AND base_relation.relkind = 'r'
	              )
	            )
        )
      )
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_proc WHERE proowner = owner_oid
	  ) OR EXISTS (
	    SELECT 1
	    FROM pg_catalog.pg_type AS type_record
	    WHERE type_record.typowner = owner_oid
	      AND NOT (
	        type_record.typnamespace = target_schema_oid
	        AND type_record.typtype = 'c'
	        AND EXISTS (
	          SELECT 1 FROM pg_catalog.pg_class AS relation
	          WHERE relation.oid = type_record.typrelid
	            AND relation.relnamespace = target_schema_oid
	            AND relation.relname = ANY (expected_relations)
	            AND relation.relkind IN ('r','v')
	        )
	        OR type_record.typnamespace = target_schema_oid
	        AND type_record.typtype = 'b'
	        AND type_record.typcategory = 'A'
	        AND EXISTS (
	          SELECT 1
	          FROM pg_catalog.pg_type AS element_type
	          JOIN pg_catalog.pg_class AS relation ON relation.oid = element_type.typrelid
	          WHERE element_type.oid = type_record.typelem
	            AND element_type.typarray = type_record.oid
	            AND relation.relnamespace = target_schema_oid
	            AND relation.relname = ANY (expected_relations)
	            AND relation.relkind IN ('r','v')
	        )
	      )
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_collation WHERE collowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_conversion WHERE conowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_operator WHERE oprowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_opclass WHERE opcowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_opfamily WHERE opfowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_ts_config WHERE cfgowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_ts_dict WHERE dictowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_statistic_ext WHERE stxowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_extension WHERE extowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_publication WHERE pubowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_subscription WHERE subowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_event_trigger WHERE evtowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_language WHERE lanowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_foreign_server WHERE srvowner = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_user_mapping WHERE umuser = owner_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_largeobject_metadata WHERE lomowner = owner_oid
  ) THEN
    RAISE EXCEPTION 'compatibility owner owns an object outside its exact read-only realm';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_shdepend AS dependency
    WHERE dependency.refclassid = 'pg_catalog.pg_authid'::regclass
      AND dependency.refobjid = owner_oid
      AND dependency.deptype IN ('a','o')
      AND dependency.dbid NOT IN (0, current_database_oid)
  ) THEN
    RAISE EXCEPTION 'compatibility owner has a cross-database dependency';
  END IF;
END
$mss_compat_owner_realm$`,
		quoteLiteral(owner),
		quoteLiteral(schema),
		sqlTextArray(expectedRelations),
		sqlTextArray(expectedSourceRelations),
	))
}

func validateApplicationRoleRealm(role string, migrator bool, coreSchema, readOnlySchema string, readOnlyRelations []string) Statement {
	allowedReadRelations := make([]string, 0, len(readOnlyRelations))
	for _, relation := range readOnlyRelations {
		allowedReadRelations = append(allowedReadRelations, quoteLiteral(relation))
	}
	allowedReadArray := "ARRAY[]::text[]"
	if len(allowedReadRelations) != 0 {
		allowedReadArray = "ARRAY[" + strings.Join(allowedReadRelations, ", ") + "]::text[]"
	}
	isMigrator := "false"
	if migrator {
		isMigrator = "true"
	}
	return plain("validate-application-role-realm-"+role, fmt.Sprintf(`DO $r1_application_realm$
DECLARE
  app_role_name text := %s;
  app_role_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %s);
  is_migrator boolean := %s;
  core_schema_name text := %s;
  readonly_schema_name text := %s;
  readonly_relations text[] := %s;
BEGIN
  -- Application roles receive CONNECT only on the isolated application
  -- database. Ownership, CREATE and TEMP would escape the schema boundary.
  IF app_role_oid IS NULL
     OR (SELECT datdba = app_role_oid FROM pg_catalog.pg_database WHERE datname = current_database())
     OR NOT pg_catalog.has_database_privilege(app_role_name, current_database(), 'CONNECT')
     OR pg_catalog.has_database_privilege(app_role_name, current_database(), 'CREATE')
     OR pg_catalog.has_database_privilege(app_role_name, current_database(), 'TEMP')
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_database AS database
       WHERE database.datallowconn
         AND database.datname <> current_database()
         AND pg_catalog.has_database_privilege(app_role_name, database.oid, 'CONNECT')
     )
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_tablespace AS tablespace
       WHERE pg_catalog.has_tablespace_privilege(app_role_name, tablespace.oid, 'CREATE')
     ) THEN
    RAISE EXCEPTION 'managed application role has an unsafe database capability';
  END IF;

  -- A role ACL or ownership dependency in any other database or on a shared
  -- object is never adopted. The sole shared dependency allowed here is its
  -- CONNECT ACL on this exact database.
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_shdepend AS dependency
    WHERE dependency.refclassid = 'pg_catalog.pg_authid'::regclass
      AND dependency.refobjid = app_role_oid
      AND dependency.deptype IN ('a','o')
      AND dependency.dbid IS DISTINCT FROM (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
      AND NOT (
        dependency.dbid = 0
        AND dependency.classid = 'pg_catalog.pg_database'::regclass
        AND dependency.objid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
        AND dependency.objsubid = 0
        AND dependency.deptype = 'a'
      )
  ) THEN
    RAISE EXCEPTION 'managed application role has a cross-database or shared-object dependency';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_namespace AS namespace
    WHERE (
      namespace.nspowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
    ) OR (
      namespace.nspname NOT LIKE 'pg_%%'
      AND namespace.nspname <> 'information_schema'
      AND (
        (pg_catalog.has_schema_privilege(app_role_name, namespace.oid, 'CREATE')
         AND NOT (is_migrator AND namespace.nspname = core_schema_name))
        OR (
          pg_catalog.has_schema_privilege(app_role_name, namespace.oid, 'USAGE')
          AND NOT (
            namespace.nspname = core_schema_name
            OR (NOT is_migrator AND namespace.nspname = readonly_schema_name)
          )
        )
      )
    )
  ) THEN
    RAISE EXCEPTION 'managed application role crosses its schema realm';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE relation.relowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_type AS type_record
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = type_record.typnamespace
    WHERE type_record.typowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_proc AS routine
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
    WHERE routine.proowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_collation AS collation_record
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = collation_record.collnamespace
    WHERE collation_record.collowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_conversion AS conversion_record
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = conversion_record.connamespace
    WHERE conversion_record.conowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_operator AS operator_record
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = operator_record.oprnamespace
    WHERE operator_record.oprowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_opclass AS operator_class
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = operator_class.opcnamespace
    WHERE operator_class.opcowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_opfamily AS operator_family
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = operator_family.opfnamespace
    WHERE operator_family.opfowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_ts_config AS text_search_config
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = text_search_config.cfgnamespace
    WHERE text_search_config.cfgowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_ts_dict AS text_search_dictionary
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = text_search_dictionary.dictnamespace
    WHERE text_search_dictionary.dictowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_statistic_ext AS statistics_record
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = statistics_record.stxnamespace
    WHERE statistics_record.stxowner = app_role_oid
      AND NOT (is_migrator AND namespace.nspname = core_schema_name)
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_extension WHERE extowner = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_publication WHERE pubowner = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_subscription WHERE subowner = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_event_trigger WHERE evtowner = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_language WHERE lanowner = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwowner = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_foreign_server WHERE srvowner = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_user_mapping WHERE umuser = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_largeobject_metadata WHERE lomowner = app_role_oid
  ) OR EXISTS (
    SELECT 1 FROM pg_catalog.pg_parameter_acl AS parameter_acl
    CROSS JOIN LATERAL pg_catalog.aclexplode(parameter_acl.paracl) AS privilege
    WHERE privilege.grantee = app_role_oid
  ) THEN
    RAISE EXCEPTION 'managed application role owns an object outside its core realm';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE relation.relkind IN ('r','p','v','m','f')
      AND namespace.nspname NOT LIKE 'pg_%%'
      AND namespace.nspname <> 'information_schema'
      AND (
        pg_catalog.has_table_privilege(app_role_name, relation.oid, 'SELECT')
        OR pg_catalog.has_table_privilege(app_role_name, relation.oid, 'INSERT')
        OR pg_catalog.has_table_privilege(app_role_name, relation.oid, 'UPDATE')
        OR pg_catalog.has_table_privilege(app_role_name, relation.oid, 'DELETE')
        OR pg_catalog.has_table_privilege(app_role_name, relation.oid, 'TRUNCATE')
        OR pg_catalog.has_table_privilege(app_role_name, relation.oid, 'REFERENCES')
        OR pg_catalog.has_table_privilege(app_role_name, relation.oid, 'TRIGGER')
        OR pg_catalog.has_table_privilege(app_role_name, relation.oid, 'MAINTAIN')
        OR pg_catalog.has_any_column_privilege(app_role_name, relation.oid, 'SELECT')
        OR pg_catalog.has_any_column_privilege(app_role_name, relation.oid, 'INSERT')
        OR pg_catalog.has_any_column_privilege(app_role_name, relation.oid, 'UPDATE')
        OR pg_catalog.has_any_column_privilege(app_role_name, relation.oid, 'REFERENCES')
      )
      AND NOT (
        is_migrator AND namespace.nspname = core_schema_name
        OR (
          NOT is_migrator AND namespace.nspname = core_schema_name
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'TRUNCATE')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'REFERENCES')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'TRIGGER')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'MAINTAIN')
          AND NOT pg_catalog.has_any_column_privilege(app_role_name, relation.oid, 'REFERENCES')
        )
        OR (
          NOT is_migrator AND namespace.nspname = readonly_schema_name
          AND relation.relname = ANY (readonly_relations)
          AND relation.relkind IN ('r','v')
          AND pg_catalog.has_table_privilege(app_role_name, relation.oid, 'SELECT')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'INSERT')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'UPDATE')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'DELETE')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'TRUNCATE')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'REFERENCES')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'TRIGGER')
          AND NOT pg_catalog.has_table_privilege(app_role_name, relation.oid, 'MAINTAIN')
          AND NOT pg_catalog.has_any_column_privilege(app_role_name, relation.oid, 'INSERT')
          AND NOT pg_catalog.has_any_column_privilege(app_role_name, relation.oid, 'UPDATE')
          AND NOT pg_catalog.has_any_column_privilege(app_role_name, relation.oid, 'REFERENCES')
        )
      )
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS sequence_relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = sequence_relation.relnamespace
    WHERE sequence_relation.relkind = 'S'
      AND namespace.nspname NOT LIKE 'pg_%%'
      AND namespace.nspname <> 'information_schema'
      AND (
        pg_catalog.has_sequence_privilege(app_role_name, sequence_relation.oid, 'SELECT')
        OR pg_catalog.has_sequence_privilege(app_role_name, sequence_relation.oid, 'UPDATE')
        OR pg_catalog.has_sequence_privilege(app_role_name, sequence_relation.oid, 'USAGE')
      )
      AND namespace.nspname <> core_schema_name
  ) THEN
    RAISE EXCEPTION 'managed application role has relation privileges outside its exact realm';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_proc AS routine
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
    WHERE routine.prokind IN ('f','p')
      AND namespace.nspname NOT LIKE 'pg_%%'
      AND namespace.nspname <> 'information_schema'
      AND pg_catalog.has_function_privilege(app_role_name, routine.oid, 'EXECUTE')
      AND namespace.nspname <> core_schema_name
  ) THEN
    RAISE EXCEPTION 'managed application role can execute a routine outside its core realm';
  END IF;
END
$r1_application_realm$`,
		quoteLiteral(role),
		quoteLiteral(role),
		isMigrator,
		quoteLiteral(coreSchema),
		quoteLiteral(readOnlySchema),
		allowedReadArray,
	))
}

func validateLegacySourcePublicACLs() Statement {
	return plain("validate-legacy-source-public-acls", fmt.Sprintf(`DO $r1_public_acl$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    CROSS JOIN LATERAL pg_catalog.aclexplode(relation.relacl) AS privilege
    WHERE namespace.nspname = %s
      AND relation.relkind IN ('r','p','v','m','S','f')
      AND privilege.grantee = 0
  ) THEN
    RAISE EXCEPTION 'legacy compatibility source has a PUBLIC relation privilege';
  END IF;
END
$r1_public_acl$`, quoteLiteral(stage.LegacySchema)))
}

func validateLegacySourceCompatibilityACLs(roles stage.Roles) Statement {
	allSources := allLegacyResourceNames()
	sort.Strings(allSources)
	tenantSources := tenantCompatibilitySourceNames()
	mallSources := mallCompatibilitySourceNames()
	snapshotSources := snapshotNames()
	sort.Strings(snapshotSources)
	return plain("validate-legacy-source-compatibility-acls", fmt.Sprintf(`DO $mss_source_acl$
DECLARE
  bootstrap_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user);
  tenant_owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %[3]s);
  mall_owner_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = %[4]s);
  source_tables text[] := %[2]s;
  tenant_sources text[] := %[5]s;
  mall_sources text[] := %[6]s;
  snapshot_sources text[] := %[7]s;
BEGIN
  IF tenant_owner_oid IS NULL OR mall_owner_oid IS NULL
     OR NOT EXISTS (
       SELECT 1 FROM pg_catalog.pg_namespace
       WHERE nspname = %[1]s AND nspowner = bootstrap_oid
     )
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_namespace AS namespace
       CROSS JOIN LATERAL pg_catalog.aclexplode(
         COALESCE(namespace.nspacl, pg_catalog.acldefault('n', namespace.nspowner))
       ) AS privilege
       WHERE namespace.nspname = %[1]s
         AND NOT (
           privilege.grantee = bootstrap_oid
           AND privilege.grantor = bootstrap_oid
           AND privilege.privilege_type IN ('USAGE','CREATE')
           AND NOT privilege.is_grantable
           OR privilege.grantee IN (tenant_owner_oid, mall_owner_oid)
           AND privilege.grantor = bootstrap_oid
           AND privilege.privilege_type = 'USAGE'
           AND NOT privilege.is_grantable
         )
     )
     OR NOT pg_catalog.has_schema_privilege(%[3]s, %[1]s, 'USAGE')
     OR NOT pg_catalog.has_schema_privilege(%[4]s, %[1]s, 'USAGE')
     OR pg_catalog.has_schema_privilege(%[3]s, %[1]s, 'CREATE')
     OR pg_catalog.has_schema_privilege(%[4]s, %[1]s, 'CREATE') THEN
    RAISE EXCEPTION 'legacy source schema owner or compatibility ACL drifted';
  END IF;

  IF (SELECT array_agg(relation.relname::text ORDER BY relation.relname)
        FROM pg_catalog.pg_class AS relation
        JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
       WHERE namespace.nspname = %[1]s AND relation.relkind = 'r')
       IS DISTINCT FROM source_tables
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_class AS relation
       JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
       WHERE namespace.nspname = %[1]s AND relation.relkind NOT IN ('r','i')
     )
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_index AS index_record
       JOIN pg_catalog.pg_class AS relation ON relation.oid = index_record.indrelid
       JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
       WHERE namespace.nspname = %[1]s AND relation.relname <> ALL (snapshot_sources)
     )
     OR EXISTS (
       SELECT 1
       FROM pg_catalog.pg_constraint AS constraint_record
       JOIN pg_catalog.pg_class AS relation ON relation.oid = constraint_record.conrelid
       JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
       WHERE namespace.nspname = %[1]s AND relation.relname <> ALL (snapshot_sources)
     )
     OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_proc AS routine
       JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
       WHERE namespace.nspname = %[1]s
     ) THEN
    RAISE EXCEPTION 'legacy source catalog contains an unreviewed relation, index, constraint, or routine';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    CROSS JOIN LATERAL pg_catalog.aclexplode(
      COALESCE(relation.relacl, pg_catalog.acldefault('r', relation.relowner))
    ) AS privilege
    WHERE namespace.nspname = %[1]s AND relation.relkind = 'r'
      AND (
        relation.relowner <> bootstrap_oid
        OR privilege.is_grantable
        OR NOT (
          privilege.grantee = bootstrap_oid AND privilege.grantor = bootstrap_oid
          OR privilege.grantee = tenant_owner_oid
             AND relation.relname = ANY (tenant_sources)
             AND privilege.grantor = bootstrap_oid
             AND privilege.privilege_type = 'SELECT'
          OR privilege.grantee = mall_owner_oid
             AND relation.relname = ANY (mall_sources)
             AND privilege.grantor = bootstrap_oid
             AND privilege.privilege_type = 'SELECT'
        )
      )
  ) OR EXISTS (
    SELECT 1
    FROM unnest(tenant_sources) AS source_name(name)
    JOIN pg_catalog.pg_class AS relation ON relation.relname = source_name.name
    JOIN pg_catalog.pg_namespace AS namespace
      ON namespace.oid = relation.relnamespace AND namespace.nspname = %[1]s
    WHERE NOT pg_catalog.has_table_privilege(%[3]s, relation.oid, 'SELECT')
  ) OR EXISTS (
    SELECT 1
    FROM unnest(mall_sources) AS source_name(name)
    JOIN pg_catalog.pg_class AS relation ON relation.relname = source_name.name
    JOIN pg_catalog.pg_namespace AS namespace
      ON namespace.oid = relation.relnamespace AND namespace.nspname = %[1]s
    WHERE NOT pg_catalog.has_table_privilege(%[4]s, relation.oid, 'SELECT')
  ) OR EXISTS (
    SELECT 1
    FROM unnest(ARRAY[%[3]s, %[4]s]::text[]) AS owner_role(name)
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.nspname = %[1]s
    JOIN pg_catalog.pg_class AS relation
      ON relation.relnamespace = namespace.oid AND relation.relkind = 'r'
    WHERE pg_catalog.has_table_privilege(owner_role.name, relation.oid, 'INSERT')
       OR pg_catalog.has_table_privilege(owner_role.name, relation.oid, 'UPDATE')
       OR pg_catalog.has_table_privilege(owner_role.name, relation.oid, 'DELETE')
       OR pg_catalog.has_table_privilege(owner_role.name, relation.oid, 'TRUNCATE')
       OR pg_catalog.has_table_privilege(owner_role.name, relation.oid, 'REFERENCES')
       OR pg_catalog.has_table_privilege(owner_role.name, relation.oid, 'TRIGGER')
       OR pg_catalog.has_table_privilege(owner_role.name, relation.oid, 'MAINTAIN')
  ) OR EXISTS (
    SELECT 1
    FROM pg_catalog.pg_attribute AS attribute
    JOIN pg_catalog.pg_class AS relation ON relation.oid = attribute.attrelid
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = %[1]s AND attribute.attnum > 0
      AND NOT attribute.attisdropped AND attribute.attacl IS NOT NULL
  ) THEN
    RAISE EXCEPTION 'legacy source table compatibility ACL drifted';
  END IF;
END
$mss_source_acl$`,
		quoteLiteral(stage.LegacySchema),
		sqlTextArray(allSources),
		quoteLiteral(roles.TenantCompatibilityOwner),
		quoteLiteral(roles.MallCompatibilityOwner),
		sqlTextArray(tenantSources),
		sqlTextArray(mallSources),
		sqlTextArray(snapshotSources),
	))
}

func validateManagedRoleACLs(
	realm, migrator, runtime, compatibilityOwner, coreSchema, readOnlySchema string,
	allowedRelations, runtimeReadRelations []string,
) Statement {
	allowed := make([]string, 0, len(allowedRelations))
	for _, relation := range allowedRelations {
		allowed = append(allowed, quoteLiteral(relation))
	}
	runtimeReads := make([]string, 0, len(runtimeReadRelations))
	for _, relation := range runtimeReadRelations {
		runtimeReads = append(runtimeReads, quoteLiteral(relation))
	}
	return plain("validate-managed-role-acls-"+realm, fmt.Sprintf(`DO $r1_role_acl$
DECLARE
  migrator_oid oid;
  runtime_oid oid;
	  compatibility_owner_oid oid;
BEGIN
  SELECT oid INTO migrator_oid FROM pg_catalog.pg_roles WHERE rolname = %s;
  SELECT oid INTO runtime_oid FROM pg_catalog.pg_roles WHERE rolname = %s;
	  SELECT oid INTO compatibility_owner_oid FROM pg_catalog.pg_roles WHERE rolname = %s;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    CROSS JOIN LATERAL pg_catalog.aclexplode(relation.relacl) AS privilege
    WHERE privilege.grantee = runtime_oid
      AND (
        namespace.nspname NOT IN (%s, %s)
        OR privilege.is_grantable
        OR (
          namespace.nspname = %s
	          AND (
	            relation.relname <> ALL (ARRAY[%s]::text[])
	            OR privilege.privilege_type <> 'SELECT'
	            OR privilege.grantor <> compatibility_owner_oid
          )
        )
        OR (
          namespace.nspname = %s
          AND relation.relkind = 'S'
          AND privilege.privilege_type NOT IN ('USAGE', 'SELECT', 'UPDATE')
        )
        OR (
          namespace.nspname = %s
          AND relation.relkind <> 'S'
          AND privilege.privilege_type NOT IN ('SELECT', 'INSERT', 'UPDATE', 'DELETE')
        )
      )
  ) THEN
    RAISE EXCEPTION 'managed runtime role has an ACL outside its realm';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    CROSS JOIN LATERAL pg_catalog.aclexplode(relation.relacl) AS privilege
    WHERE namespace.nspname IN (%s, %s)
      AND (
        (
          namespace.nspname = %s
          AND privilege.grantee NOT IN (migrator_oid, runtime_oid)
        )
	        OR (
	          namespace.nspname = %s
	          AND privilege.grantee NOT IN (compatibility_owner_oid, runtime_oid)
        )
        OR (privilege.grantee = runtime_oid AND privilege.is_grantable)
      )
  ) THEN
    RAISE EXCEPTION 'managed schema relation has an unexpected ACL grantee';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = %s
      AND relation.relkind IN ('r','p','v','m','f','S')
	      AND (
	        relation.relname <> ALL (ARRAY[%s]::text[])
	        OR relation.relowner <> compatibility_owner_oid
        OR relation.relkind NOT IN ('r','v')
      )
  ) THEN
    RAISE EXCEPTION 'read-only schema contains an unmanaged or foreign-owned relation';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_proc AS routine
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
    WHERE namespace.nspname = %s
  ) THEN
    RAISE EXCEPTION 'read-only schema contains an unexpected function or procedure';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_attribute AS attribute
    JOIN pg_catalog.pg_class AS relation ON relation.oid = attribute.attrelid
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname IN (%s, %s)
      AND attribute.attnum > 0 AND NOT attribute.attisdropped AND attribute.attacl IS NOT NULL
  ) THEN
    RAISE EXCEPTION 'managed schema contains column-level privileges';
  END IF;
  IF EXISTS (
    SELECT 1 FROM unnest(ARRAY[%s]::text[]) AS required_read(relation_name)
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.nspname = %s
    JOIN pg_catalog.pg_class AS relation
      ON relation.relnamespace = namespace.oid AND relation.relname = required_read.relation_name
    WHERE NOT pg_catalog.has_table_privilege(%s, relation.oid, 'SELECT')
  ) THEN
    RAISE EXCEPTION 'managed runtime role lacks an explicit compatibility read grant';
  END IF;
END
$r1_role_acl$`,
		quoteLiteral(migrator),
		quoteLiteral(runtime),
		quoteLiteral(compatibilityOwner),
		quoteLiteral(coreSchema),
		quoteLiteral(readOnlySchema),
		quoteLiteral(readOnlySchema),
		strings.Join(runtimeReads, ", "),
		quoteLiteral(coreSchema),
		quoteLiteral(coreSchema),
		quoteLiteral(coreSchema),
		quoteLiteral(readOnlySchema),
		quoteLiteral(coreSchema),
		quoteLiteral(readOnlySchema),
		quoteLiteral(readOnlySchema),
		strings.Join(allowed, ", "),
		quoteLiteral(readOnlySchema),
		quoteLiteral(coreSchema),
		quoteLiteral(readOnlySchema),
		strings.Join(runtimeReads, ", "),
		quoteLiteral(readOnlySchema),
		quoteLiteral(runtime),
	))
}

func plain(name, sql string) Statement {
	return Statement{Name: name, SQL: sql}
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func qualified(schema, relation string) string {
	return quoteIdentifier(schema) + "." + quoteIdentifier(relation)
}

func grantSchemaUsage(schema, role string) string {
	return fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", quoteIdentifier(schema), quoteIdentifier(role))
}

func defaultTablePrivileges(owner, schema, runtime string) string {
	return fmt.Sprintf(
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA %s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s",
		quoteIdentifier(owner), quoteIdentifier(schema), quoteIdentifier(runtime),
	)
}

func defaultSequencePrivileges(owner, schema, runtime string) string {
	return fmt.Sprintf(
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA %s GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO %s",
		quoteIdentifier(owner), quoteIdentifier(schema), quoteIdentifier(runtime),
	)
}

func revokeDefaultPublicFunctionPrivileges(owner string) string {
	return fmt.Sprintf(
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s REVOKE ALL ON FUNCTIONS FROM PUBLIC",
		quoteIdentifier(owner),
	)
}

func revokeDefaultPrivileges(owner, schema, objectKind, runtime string) string {
	return fmt.Sprintf(
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA %s REVOKE ALL ON %s FROM %s",
		quoteIdentifier(owner), quoteIdentifier(schema), objectKind, quoteIdentifier(runtime),
	)
}

func revokeAllExisting(objectKind, schema, runtime string) string {
	return fmt.Sprintf(
		"REVOKE ALL ON ALL %s IN SCHEMA %s FROM %s",
		objectKind, quoteIdentifier(schema), quoteIdentifier(runtime),
	)
}

func softDeletePredicate(enabled bool) string {
	if !enabled {
		return ""
	}
	return " AND owner." + quoteIdentifier("deleted_at") + " IS NULL"
}

func joinQualifiedLegacyResources() string {
	resources := make([]string, 0, len(mallLegacyViews))
	for _, resource := range mallLegacyViews {
		resources = append(resources, qualified(stage.LegacySchema, resource.Name))
	}
	return strings.Join(resources, ", ")
}

func allLegacyResourceNames() []string {
	names := legacyViewNames()
	for _, resource := range mallSnapshots {
		names = append(names, resource.Name)
	}
	names = append(names, tenantSharedResource)
	return names
}

func joinQualifiedAllLegacyResources() string {
	resources := make([]string, 0, len(allLegacyResourceNames()))
	for _, resource := range allLegacyResourceNames() {
		resources = append(resources, qualified(stage.LegacySchema, resource))
	}
	return strings.Join(resources, ", ")
}

func validateCompiledSourceColumns() error {
	names := allLegacyResourceNames()
	if len(legacySourceColumns) != len(names) || len(legacySourceColumnFingerprints) != len(names) {
		return errors.New("compiled legacy source column inventory is inconsistent")
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		fingerprint := legacySourceColumnFingerprints[name]
		if _, duplicate := seen[name]; duplicate || len(legacySourceColumns[name]) == 0 ||
			len(fingerprint) != 64 || strings.Trim(fingerprint, "0123456789abcdef") != "" {
			return errors.New("compiled legacy source column inventory is inconsistent")
		}
		seen[name] = struct{}{}
	}
	for _, resource := range mallSnapshots {
		if len(resource.Columns) != len(legacySourceColumns[resource.Name]) {
			return errors.New("compiled snapshot DDL inventory is inconsistent")
		}
		for index, column := range resource.Columns {
			if column.Name != legacySourceColumns[resource.Name][index] || column.Type == "" {
				return errors.New("compiled snapshot DDL inventory is inconsistent")
			}
		}
		for _, primary := range resource.PrimaryKey {
			_ = snapshotColumnByName(resource, primary)
		}
		for _, indexColumns := range resource.Indexes {
			for _, column := range indexColumns {
				_ = snapshotColumnByName(resource, column)
			}
		}
	}
	return nil
}

func validateLegacySourceColumnStatements() []Statement {
	statements := make([]Statement, 0, len(allLegacyResourceNames()))
	for _, relation := range allLegacyResourceNames() {
		columns := legacySourceColumns[relation]
		quotedColumns := make([]string, 0, len(columns))
		for _, column := range columns {
			quotedColumns = append(quotedColumns, quoteLiteral(column))
		}
		statements = append(statements, plain("validate-legacy-source-columns-"+relation, fmt.Sprintf(`DO $r1_source_columns$
DECLARE
  source_oid oid;
  bootstrap_oid oid := (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user);
  actual_columns text[];
  actual_fingerprint text;
BEGIN
  SELECT source_relation.oid INTO source_oid
    FROM pg_catalog.pg_class AS source_relation
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = source_relation.relnamespace
    WHERE namespace.nspname = %s AND source_relation.relname = %s;

  IF source_oid IS NULL OR NOT EXISTS (
       SELECT 1
       FROM pg_catalog.pg_class AS source_relation
       JOIN pg_catalog.pg_am AS access_method ON access_method.oid = source_relation.relam
       WHERE source_relation.oid = source_oid
         AND source_relation.relkind = 'r'
         AND source_relation.relpersistence = 'p'
         AND source_relation.relowner = bootstrap_oid
         AND access_method.amname = 'heap'
         AND source_relation.reltablespace = 0
         AND source_relation.relreplident = 'd'
         AND NOT source_relation.relispartition
         AND NOT source_relation.relrowsecurity
         AND NOT source_relation.relforcerowsecurity
         AND NOT source_relation.relhasrules
         AND NOT source_relation.relhastriggers
         AND source_relation.reloptions IS NULL
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_inherits
       WHERE inhrelid = source_oid OR inhparent = source_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_policy WHERE polrelid = source_oid
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid = source_oid AND NOT tgisinternal
     ) OR EXISTS (
       SELECT 1 FROM pg_catalog.pg_rewrite WHERE ev_class = source_oid
     ) THEN
    RAISE EXCEPTION 'legacy source relation %% has an unexpected owner/storage/executable shape', %s;
  END IF;

  -- Reject executable or user-defined type output paths before calculating a
  -- human-reviewable catalog fingerprint. In particular, never stringify an
  -- untrusted attmissingval or custom-type value.
  IF EXISTS (
    SELECT 1
    FROM pg_catalog.pg_attribute AS attribute
    JOIN pg_catalog.pg_type AS type_record ON type_record.oid = attribute.atttypid
    JOIN pg_catalog.pg_namespace AS type_namespace ON type_namespace.oid = type_record.typnamespace
    LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = attribute.attcollation
    LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace
    WHERE attribute.attrelid = source_oid AND attribute.attnum > 0 AND NOT attribute.attisdropped
      AND (
        type_namespace.nspname <> 'pg_catalog'
        OR type_record.typtype <> 'b'
        OR attribute.atthasmissing
        OR attribute.attacl IS NOT NULL
        OR (attribute.attcollation <> 0 AND collation_namespace.nspname <> 'pg_catalog')
      )
  ) THEN
    RAISE EXCEPTION 'legacy source relation %% has an unreviewed type, collation, missing value, or column ACL', %s;
  END IF;

  SELECT array_agg(attribute.attname::text ORDER BY attribute.attnum)
    INTO actual_columns
    FROM pg_catalog.pg_attribute AS attribute
    WHERE attribute.attrelid = source_oid AND attribute.attnum > 0
      AND NOT attribute.attisdropped;

  SELECT pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to(
           pg_catalog.jsonb_agg(pg_catalog.jsonb_build_array(
             attribute.attnum,
             attribute.attname,
             type_namespace.nspname,
             type_record.typname,
             type_record.typtype,
             type_record.typcategory,
             pg_catalog.format_type(attribute.atttypid, attribute.atttypmod),
             attribute.atttypmod,
             attribute.attndims,
             attribute.attnotnull,
             attribute.atthasdef,
             CASE WHEN default_record.oid IS NULL THEN NULL
                  ELSE pg_catalog.pg_get_expr(default_record.adbin, default_record.adrelid, true) END,
             attribute.attidentity::text,
             attribute.attgenerated::text,
             attribute.attstorage::text,
             attribute.attcompression::text,
             attribute.atthasmissing,
             NULL::text,
             attribute.attislocal,
             attribute.attinhcount,
             collation_namespace.nspname,
             collation_record.collname,
             collation_record.collprovider::text,
             collation_record.collisdeterministic,
             collation_record.collencoding,
             CASE WHEN attribute.attacl IS NULL THEN NULL ELSE attribute.attacl::text END
           ) ORDER BY attribute.attnum)::text,
           'UTF8'
         )), 'hex')
    INTO actual_fingerprint
    FROM pg_catalog.pg_attribute AS attribute
    JOIN pg_catalog.pg_type AS type_record ON type_record.oid = attribute.atttypid
    JOIN pg_catalog.pg_namespace AS type_namespace ON type_namespace.oid = type_record.typnamespace
    LEFT JOIN pg_catalog.pg_attrdef AS default_record
      ON default_record.adrelid = attribute.attrelid AND default_record.adnum = attribute.attnum
    LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = attribute.attcollation
    LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace
    WHERE attribute.attrelid = source_oid AND attribute.attnum > 0
      AND NOT attribute.attisdropped;

  IF EXISTS (
       SELECT 1 FROM pg_catalog.pg_attribute
       WHERE attrelid = source_oid AND attnum > 0 AND attisdropped
     ) OR actual_columns IS DISTINCT FROM ARRAY[%s]::text[]
       OR actual_fingerprint IS DISTINCT FROM %s THEN
    RAISE EXCEPTION 'legacy source relation %% has an unexpected ordered column or catalog fingerprint', %s;
  END IF;
END
$r1_source_columns$`,
			quoteLiteral(stage.LegacySchema),
			quoteLiteral(relation),
			quoteLiteral(relation),
			quoteLiteral(relation),
			strings.Join(quotedColumns, ", "),
			quoteLiteral(legacySourceColumnFingerprints[relation]),
			quoteLiteral(relation),
		)))
	}
	return statements
}

func legacyViewNames() []string {
	names := make([]string, 0, len(mallLegacyViews))
	for _, resource := range mallLegacyViews {
		names = append(names, resource.Name)
	}
	return names
}

func snapshotNames() []string {
	names := make([]string, 0, len(mallSnapshots))
	for _, resource := range mallSnapshots {
		names = append(names, resource.Name)
	}
	return names
}

func keyHashExpression(alias string, primaryKey []string) string {
	parts := make([]string, 0, len(primaryKey))
	order := make([]string, 0, len(primaryKey))
	for _, column := range primaryKey {
		reference := quoteIdentifier(alias) + "." + quoteIdentifier(column) + "::text"
		parts = append(parts, "COALESCE("+reference+", '<NULL>')")
		order = append(order, reference)
	}
	return fmt.Sprintf(
		"COALESCE(md5(string_agg(concat_ws(E'\\x1f', %s), E'\\x1e' ORDER BY %s)), md5(''))",
		strings.Join(parts, ", "), strings.Join(order, ", "),
	)
}

func rowHashExpression(alias string, primaryKey []string) string {
	qualifiedAlias := quoteIdentifier(alias)
	order := make([]string, 0, len(primaryKey)+1)
	for _, column := range primaryKey {
		order = append(order, qualifiedAlias+"."+quoteIdentifier(column)+"::text")
	}
	// PostgreSQL composite text uses each column's native output function. In
	// particular, json (as distinct from jsonb) retains its lexical whitespace
	// and key ordering, while CSV/text values remain byte-significant.
	record := qualifiedAlias + "::text"
	// Include the complete row as the final ordering key so duplicate declared
	// keys still produce a deterministic digest and cannot hide row drift.
	order = append(order, record)
	framedRecord := "octet_length(" + record + ")::text || ':' || " + record
	return fmt.Sprintf(
		"COALESCE(md5(string_agg(%s, '' ORDER BY %s)), md5(''))",
		framedRecord, strings.Join(order, ", "),
	)
}

func orphanCheck(schema, childTable, childColumn, parentTable, parentColumn string) string {
	return fmt.Sprintf(`EXISTS (
    SELECT 1
    FROM %s AS child
    WHERE NULLIF(btrim(child.%s::text), '') IS NOT NULL
      AND child.%s::text <> '0'
      AND NOT EXISTS (
        SELECT 1 FROM %s AS parent
        WHERE parent.%s::text = child.%s::text
      )
  )`,
		qualified(schema, childTable),
		quoteIdentifier(childColumn),
		quoteIdentifier(childColumn),
		qualified(schema, parentTable),
		quoteIdentifier(parentColumn),
		quoteIdentifier(childColumn),
	)
}
