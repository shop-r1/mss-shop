package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/manifest"
)

const (
	targetDatabase              = "mss_shop_dev"
	emptyDatabaseMarker         = "r1shop.io/operator-binding=mss-shop-dev:PostgreSQL:mss_shop_dev;state=isolated-empty"
	importedMarkerPrefix        = "mss-shop-isolated-dev:legacy-import:v1:"
	expectedTargetPG            = "170006"
	expectedSourceRoutineCount  = 91
	expectedSourceRoutineSHA256 = "32c0b88f3178e4a15647eef85da4a718b4e490070bd7fa2c77876101f386d81e"
	sourceColumnInventorySQL    = `
SELECT relation.relname::text,
       attribute.attnum::integer,
       attribute.attname::text,
       attribute.attisdropped,
       pg_catalog.format_type(attribute.atttypid, attribute.atttypmod),
       type_namespace.nspname::text,
       type_record.typname::text,
       type_record.typtype::text,
       attribute.atttypmod::integer,
       attribute.attnotnull,
       attribute.atthasdef,
       COALESCE(pg_catalog.pg_get_expr(default_record.adbin, default_record.adrelid, true), ''),
       attribute.attidentity::text,
       attribute.attgenerated::text,
       attribute.attstorage::text,
       attribute.attcompression::text,
       attribute.atthasmissing,
       COALESCE(collation_namespace.nspname::text, ''),
       COALESCE(collation_record.collname::text, ''),
       COALESCE(collation_record.collprovider::text, ''),
       COALESCE(collation_record.collisdeterministic, false),
       COALESCE(collation_record.collencoding, 0),
       COALESCE(attribute.attacl::text, '')
FROM pg_catalog.pg_class AS relation
JOIN pg_catalog.pg_namespace AS relation_namespace ON relation_namespace.oid = relation.relnamespace
JOIN pg_catalog.pg_attribute AS attribute ON attribute.attrelid = relation.oid
JOIN pg_catalog.pg_type AS type_record ON type_record.oid = attribute.atttypid
JOIN pg_catalog.pg_namespace AS type_namespace ON type_namespace.oid = type_record.typnamespace
LEFT JOIN pg_catalog.pg_attrdef AS default_record
  ON default_record.adrelid = relation.oid AND default_record.adnum = attribute.attnum
LEFT JOIN pg_catalog.pg_collation AS collation_record ON collation_record.oid = attribute.attcollation
LEFT JOIN pg_catalog.pg_namespace AS collation_namespace ON collation_namespace.oid = collation_record.collnamespace
WHERE relation_namespace.nspname = 'public'
  AND relation.relname = ANY($1::text[])
  AND attribute.attnum > 0
ORDER BY relation.relname, attribute.attnum
`
	sourceExecutableObjectInventorySQL = `
WITH public_routines AS (
  SELECT routine.*
    FROM pg_catalog.pg_proc AS routine
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
   WHERE namespace.nspname = 'public'
),
reviewed_routines AS (
  SELECT routine.*
    FROM public_routines AS routine
    JOIN pg_catalog.pg_depend AS dependency
      ON dependency.classid = 'pg_catalog.pg_proc'::pg_catalog.regclass
     AND dependency.objid = routine.oid
     AND dependency.objsubid = 0
     AND dependency.refclassid = 'pg_catalog.pg_extension'::pg_catalog.regclass
     AND dependency.refobjsubid = 0
     AND dependency.deptype = 'e'
    JOIN pg_catalog.pg_extension AS extension ON extension.oid = dependency.refobjid
    JOIN pg_catalog.pg_namespace AS extension_namespace ON extension_namespace.oid = extension.extnamespace
   WHERE extension.extname = 'timescaledb'
     AND extension.extversion = '2.20.2'
     AND extension_namespace.nspname = 'public'
),
standalone_types AS (
  SELECT type_record.oid
    FROM pg_catalog.pg_type AS type_record
    JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = type_record.typnamespace
   WHERE namespace.nspname = 'public'
     AND type_record.typrelid = 0
     AND NOT EXISTS (
       SELECT 1
         FROM pg_catalog.pg_type AS element_type
         JOIN pg_catalog.pg_class AS relation ON relation.reltype = element_type.oid
        WHERE type_record.typelem = element_type.oid
          AND type_record.typcategory = 'A'
     )
)
SELECT (SELECT count(*) FROM public_routines) - (SELECT count(*) FROM reviewed_routines),
       (SELECT count(*) FROM standalone_types),
       (SELECT count(*) FROM reviewed_routines),
       (SELECT COALESCE(
          pg_catalog.jsonb_agg(pg_catalog.to_jsonb(reviewed_routine) ORDER BY reviewed_routine.oid),
          '[]'::pg_catalog.jsonb
        )::text FROM reviewed_routines AS reviewed_routine),
       (SELECT COALESCE(array_agg(
          extension.extname::text || '|' || extension.extversion::text || '|' || COALESCE(namespace.nspname::text, '')
          ORDER BY extension.extname
        ), ARRAY[]::text[])
          FROM pg_catalog.pg_extension AS extension
          LEFT JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = extension.extnamespace)
`
)

type sourceRelation struct {
	Name             string
	Kind             string
	Persistence      string
	AccessMethod     string
	RowSecurity      bool
	ForceRowSecurity bool
	Partition        bool
	InheritanceEdges int64
	Triggers         int64
	Rules            int64
	Policies         int64
}

type sourceCatalog struct {
	Relations                []sourceRelation
	Columns                  map[string][]manifest.Column
	Extensions               []string
	ReviewedPublicRoutines   int64
	ReviewedRoutineSHA256    string
	UnreviewedPublicRoutines int64
	StandaloneTypes          int64
}

type targetBoundary struct {
	ServerVersion            string
	EventTriggersDisabled    bool
	SSL                      bool
	DatabaseName             string
	SessionIdentityExact     bool
	DatabaseOwnerCurrent     bool
	CurrentRoleSuperuser     bool
	Marker                   string
	PublicDatabasePrivileges int64
	PublicSchemaOwnerCurrent bool
	PublicSchemaPrivileges   int64
	UserSchemas              int64
	UserObjects              int64
	DatabaseSettings         int64
	RoleSettings             int64
	DefaultPrivileges        int64
	ForeignServers           int64
	EventTriggers            int64
	Publications             int64
	Subscriptions            int64
	Extensions               []string
}

func inspectSourceCatalog(
	ctx context.Context,
	tx pgx.Tx,
	tables []manifest.Table,
) (sourceCatalog, error) {
	rows, err := tx.Query(ctx, `
SELECT relation.relname::text,
       relation.relkind::text,
       relation.relpersistence::text,
       COALESCE(access_method.amname::text, ''),
       relation.relrowsecurity,
       relation.relforcerowsecurity,
       relation.relispartition,
       (SELECT count(*) FROM pg_catalog.pg_inherits AS inheritance
         WHERE inheritance.inhrelid = relation.oid OR inheritance.inhparent = relation.oid),
       (SELECT count(*) FROM pg_catalog.pg_trigger AS trigger
         WHERE trigger.tgrelid = relation.oid),
       (SELECT count(*) FROM pg_catalog.pg_rewrite AS rewrite
         WHERE rewrite.ev_class = relation.oid),
       (SELECT count(*) FROM pg_catalog.pg_policy AS policy
         WHERE policy.polrelid = relation.oid)
FROM pg_catalog.pg_class AS relation
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
LEFT JOIN pg_catalog.pg_am AS access_method ON access_method.oid = relation.relam
WHERE namespace.nspname = 'public'
  AND relation.relkind NOT IN ('i', 'I')
ORDER BY relation.relname
`)
	if err != nil {
		return sourceCatalog{}, errors.New("inspect source relation inventory failed")
	}
	defer rows.Close()
	catalog := sourceCatalog{Columns: make(map[string][]manifest.Column, len(tables))}
	for rows.Next() {
		var relation sourceRelation
		if err := rows.Scan(
			&relation.Name,
			&relation.Kind,
			&relation.Persistence,
			&relation.AccessMethod,
			&relation.RowSecurity,
			&relation.ForceRowSecurity,
			&relation.Partition,
			&relation.InheritanceEdges,
			&relation.Triggers,
			&relation.Rules,
			&relation.Policies,
		); err != nil {
			return sourceCatalog{}, errors.New("inspect source relation inventory failed")
		}
		catalog.Relations = append(catalog.Relations, relation)
	}
	if rows.Err() != nil {
		return sourceCatalog{}, errors.New("inspect source relation inventory failed")
	}

	tableNames := make([]string, 0, len(tables))
	for _, table := range tables {
		tableNames = append(tableNames, table.Name)
	}
	columnRows, err := tx.Query(ctx, sourceColumnInventorySQL, tableNames)
	if err != nil {
		return sourceCatalog{}, errors.New("inspect source column inventory failed")
	}
	defer columnRows.Close()
	for columnRows.Next() {
		var tableName string
		var column manifest.Column
		if err := columnRows.Scan(
			&tableName,
			&column.Position,
			&column.Name,
			&column.Dropped,
			&column.Type,
			&column.TypeNamespace,
			&column.TypeName,
			&column.TypeKind,
			&column.TypeMod,
			&column.NotNull,
			&column.HasDefault,
			&column.DefaultExpression,
			&column.Identity,
			&column.Generated,
			&column.Storage,
			&column.Compression,
			&column.HasMissing,
			&column.CollationNamespace,
			&column.Collation,
			&column.CollationProvider,
			&column.CollationDeterministic,
			&column.CollationEncoding,
			&column.ColumnACL,
		); err != nil {
			return sourceCatalog{}, errors.New("inspect source column inventory failed")
		}
		catalog.Columns[tableName] = append(catalog.Columns[tableName], column)
	}
	if columnRows.Err() != nil {
		return sourceCatalog{}, errors.New("inspect source column inventory failed")
	}
	var reviewedRoutineInventory string
	if err := tx.QueryRow(ctx, sourceExecutableObjectInventorySQL).Scan(
		&catalog.UnreviewedPublicRoutines,
		&catalog.StandaloneTypes,
		&catalog.ReviewedPublicRoutines,
		&reviewedRoutineInventory,
		&catalog.Extensions,
	); err != nil {
		return sourceCatalog{}, errors.New("inspect source executable object inventory failed")
	}
	routineDigest := sha256.Sum256([]byte(reviewedRoutineInventory))
	catalog.ReviewedRoutineSHA256 = hex.EncodeToString(routineDigest[:])
	return catalog, nil
}

func validateSourceCatalog(catalog sourceCatalog, tables []manifest.Table) error {
	if !sourceExtensionInventoryReviewed(catalog.Extensions) ||
		catalog.ReviewedPublicRoutines != expectedSourceRoutineCount ||
		catalog.ReviewedRoutineSHA256 != expectedSourceRoutineSHA256 ||
		catalog.UnreviewedPublicRoutines != 0 || catalog.StandaloneTypes != 0 {
		return errors.New("source public schema contains an unreviewed executable object or type")
	}
	expectedRelations := manifest.ImportNames()
	expectedRelations = append(expectedRelations, manifest.SourceIdentityNames()...)
	sort.Strings(expectedRelations)
	if len(catalog.Relations) != len(expectedRelations) {
		return errors.New("source public relation inventory is not the reviewed 54-table set")
	}
	for index, relation := range catalog.Relations {
		if relation.Name != expectedRelations[index] || relation.Kind != "r" ||
			relation.Persistence != "p" || relation.AccessMethod != "heap" ||
			relation.RowSecurity || relation.ForceRowSecurity || relation.Partition ||
			relation.InheritanceEdges != 0 || relation.Triggers != 0 ||
			relation.Rules != 0 || relation.Policies != 0 {
			return errors.New("source relation inventory is outside the reviewed safe shape")
		}
	}
	if len(catalog.Columns) != len(tables) {
		return errors.New("source column inventory is incomplete")
	}
	for _, table := range tables {
		actual, exists := catalog.Columns[table.Name]
		if !exists || !reflect.DeepEqual(actual, table.Columns) {
			return fmt.Errorf("source columns for %q are outside the compiled allow-list", table.Name)
		}
	}
	return nil
}

func inspectTargetBoundary(ctx context.Context, tx pgx.Tx) (targetBoundary, error) {
	boundary := targetBoundary{}
	var eventTriggers string
	if err := tx.QueryRow(ctx, `
SELECT current_setting('server_version_num'),
       current_setting('event_triggers'),
       COALESCE((SELECT ssl FROM pg_catalog.pg_stat_ssl WHERE pid = pg_catalog.pg_backend_pid()), false),
       current_database(),
       session_user = current_user,
       COALESCE((SELECT datdba = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user)
                   FROM pg_catalog.pg_database WHERE datname = current_database()), false),
       COALESCE((SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user), false),
       COALESCE(pg_catalog.shobj_description(
         (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database()),
         'pg_database'
       ), ''),
       (SELECT count(*)
          FROM pg_catalog.pg_database AS database,
               LATERAL pg_catalog.aclexplode(COALESCE(database.datacl,
                 pg_catalog.acldefault('d', database.datdba))) AS acl
         WHERE database.datname = current_database() AND acl.grantee = 0),
       COALESCE((SELECT namespace.nspowner =
                   (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user)
                   FROM pg_catalog.pg_namespace AS namespace
                  WHERE namespace.nspname = 'public'), false),
       (SELECT count(*)
          FROM pg_catalog.pg_namespace AS namespace,
               LATERAL pg_catalog.aclexplode(COALESCE(namespace.nspacl,
                 pg_catalog.acldefault('n', namespace.nspowner))) AS acl
         WHERE namespace.nspname = 'public' AND acl.grantee = 0),
       (SELECT count(*) FROM pg_catalog.pg_namespace
         WHERE nspname <> 'public'
           AND nspname <> 'information_schema'
           AND nspname !~ '^pg_'),
       (
         (SELECT count(*) FROM pg_catalog.pg_class AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.relnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_proc AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.pronamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_type AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.typnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_collation AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.collnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_conversion AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.connamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_operator AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.oprnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_opclass AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.opcnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_opfamily AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.opfnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_statistic_ext AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.stxnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_ts_config AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.cfgnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_ts_dict AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.dictnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_ts_parser AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.prsnamespace
           WHERE namespace.nspname = 'public')
         + (SELECT count(*) FROM pg_catalog.pg_ts_template AS object
           JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = object.tmplnamespace
           WHERE namespace.nspname = 'public')
       ),
       (SELECT count(*) FROM pg_catalog.pg_db_role_setting
         WHERE setdatabase = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())),
       (SELECT count(*) FROM pg_catalog.pg_roles
         WHERE rolname = current_user AND rolconfig IS NOT NULL),
       (SELECT count(*) FROM pg_catalog.pg_default_acl
         WHERE defaclrole = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user)),
       ((SELECT count(*) FROM pg_catalog.pg_foreign_data_wrapper)
         + (SELECT count(*) FROM pg_catalog.pg_foreign_server)
         + (SELECT count(*) FROM pg_catalog.pg_user_mapping)),
       (SELECT count(*) FROM pg_catalog.pg_event_trigger),
       (SELECT count(*) FROM pg_catalog.pg_publication),
       (SELECT count(*) FROM pg_catalog.pg_subscription)
`).Scan(
		&boundary.ServerVersion,
		&eventTriggers,
		&boundary.SSL,
		&boundary.DatabaseName,
		&boundary.SessionIdentityExact,
		&boundary.DatabaseOwnerCurrent,
		&boundary.CurrentRoleSuperuser,
		&boundary.Marker,
		&boundary.PublicDatabasePrivileges,
		&boundary.PublicSchemaOwnerCurrent,
		&boundary.PublicSchemaPrivileges,
		&boundary.UserSchemas,
		&boundary.UserObjects,
		&boundary.DatabaseSettings,
		&boundary.RoleSettings,
		&boundary.DefaultPrivileges,
		&boundary.ForeignServers,
		&boundary.EventTriggers,
		&boundary.Publications,
		&boundary.Subscriptions,
	); err != nil {
		return targetBoundary{}, errors.New("inspect target database boundary failed")
	}
	boundary.EventTriggersDisabled = eventTriggers == "off"
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(array_agg(extname::text ORDER BY extname), ARRAY[]::text[])
FROM pg_catalog.pg_extension
`).Scan(&boundary.Extensions); err != nil {
		return targetBoundary{}, errors.New("inspect target extension inventory failed")
	}
	return boundary, nil
}

func validateTargetBoundary(boundary targetBoundary) error {
	if boundary.ServerVersion != expectedTargetPG || !boundary.EventTriggersDisabled ||
		!boundary.SSL || boundary.DatabaseName != targetDatabase ||
		!boundary.SessionIdentityExact || !boundary.DatabaseOwnerCurrent ||
		!boundary.CurrentRoleSuperuser || boundary.Marker != emptyDatabaseMarker ||
		boundary.PublicDatabasePrivileges != 0 || !boundary.PublicSchemaOwnerCurrent ||
		boundary.PublicSchemaPrivileges != 0 ||
		boundary.UserSchemas != 0 || boundary.UserObjects != 0 ||
		boundary.DatabaseSettings != 0 || boundary.RoleSettings != 0 ||
		boundary.DefaultPrivileges != 0 || boundary.ForeignServers != 0 ||
		boundary.EventTriggers != 0 ||
		boundary.Publications != 0 || boundary.Subscriptions != 0 ||
		!reflect.DeepEqual(boundary.Extensions, []string{"plpgsql"}) {
		return errors.New("target database is not the empty isolated mss_shop_dev boundary")
	}
	return nil
}
