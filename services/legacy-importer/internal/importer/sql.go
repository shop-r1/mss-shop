package importer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/manifest"
)

type statement struct {
	Name string
	SQL  string
}

func createTableStatements(tables []manifest.Table) ([]statement, error) {
	statements := make([]statement, 0, len(tables)*2)
	for _, table := range tables {
		if table.Name == "" || len(table.Columns) == 0 {
			return nil, errors.New("compiled table manifest is invalid")
		}
		columns := make([]string, 0, len(table.Columns))
		for _, column := range table.Columns {
			if column.Name == "" || column.Type == "" || column.Dropped ||
				column.HasDefault || column.DefaultExpression != "" ||
				column.Identity != "" || column.Generated != "" || column.HasMissing ||
				column.Compression != "" || column.TypeNamespace != "pg_catalog" ||
				column.TypeKind != "b" || column.ColumnACL != "" {
				return nil, errors.New("compiled column manifest is unsafe")
			}
			definition := quoteIdentifier(column.Name) + " " + column.Type
			if column.Collation != "" {
				if column.CollationNamespace != "pg_catalog" || column.Collation != "default" {
					return nil, errors.New("compiled column collation is unsafe")
				}
				definition += ` COLLATE pg_catalog."default"`
			}
			if column.NotNull {
				definition += " NOT NULL"
			}
			columns = append(columns, definition)
		}
		qualified := qualifiedTable(table.Name)
		statements = append(statements,
			statement{
				Name: "create-" + table.Name,
				SQL:  fmt.Sprintf("CREATE TABLE %s (%s) USING heap", qualified, strings.Join(columns, ", ")),
			},
			statement{
				Name: "revoke-" + table.Name,
				SQL:  "REVOKE ALL ON TABLE " + qualified + " FROM PUBLIC",
			},
		)
	}
	return statements, nil
}

func createIndexStatements(tables []manifest.Table) ([]statement, error) {
	statements := make([]statement, 0)
	for _, table := range tables {
		for indexPosition, index := range table.Indexes {
			if len(index.Columns) == 0 {
				return nil, errors.New("compiled index manifest is invalid")
			}
			columns := make([]string, 0, len(index.Columns))
			for _, column := range index.Columns {
				columns = append(columns, quoteIdentifier(column))
			}
			if index.Primary {
				name := quoteIdentifier("mss_import_" + table.Name + "_pkey")
				statements = append(statements, statement{
					Name: "primary-key-" + table.Name,
					SQL: fmt.Sprintf(
						"ALTER TABLE %s ADD CONSTRAINT %s PRIMARY KEY (%s)",
						qualifiedTable(table.Name), name, strings.Join(columns, ", "),
					),
				})
				continue
			}
			name := quoteIdentifier(fmt.Sprintf("mss_import_%s_%02d_idx", table.Name, indexPosition))
			statements = append(statements, statement{
				Name: fmt.Sprintf("index-%s-%02d", table.Name, indexPosition),
				SQL: fmt.Sprintf(
					"CREATE INDEX %s ON %s USING btree (%s)",
					name, qualifiedTable(table.Name), strings.Join(columns, ", "),
				),
			})
		}
	}
	return statements, nil
}

func sourceCopySQL(table manifest.Table) string {
	return fmt.Sprintf(
		"COPY (SELECT %s FROM ONLY %s) TO STDOUT (FORMAT binary)",
		quotedColumnList(table), qualifiedTable(table.Name),
	)
}

func targetCopySQL(table manifest.Table) string {
	return fmt.Sprintf(
		"COPY %s (%s) FROM STDIN (FORMAT binary)",
		qualifiedTable(table.Name), quotedColumnList(table),
	)
}

func quotedColumnList(table manifest.Table) string {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		columns = append(columns, quoteIdentifier(column.Name))
	}
	return strings.Join(columns, ", ")
}

func qualifiedTable(table string) string {
	return quoteIdentifier("public") + "." + quoteIdentifier(table)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
