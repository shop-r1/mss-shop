package mallsettings

import (
	"errors"
	"testing"
)

func TestPostgresMallSettingsProjectionRequiresExactSelectOnlySecurityBarrierView(t *testing.T) {
	t.Parallel()

	ready := postgresSystemConfigRelation{
		RelationKind: "v", SecurityBarrier: true, CanSelect: true,
		Columns: append([]string(nil), requiredSystemConfigColumns...),
	}
	if err := verifyPostgresReadOnlyView(ready, requiredSystemConfigColumns); err != nil {
		t.Fatalf("ready projection rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*postgresSystemConfigRelation)
	}{
		{name: "table", mutate: func(value *postgresSystemConfigRelation) { value.RelationKind = "r" }},
		{name: "no security barrier", mutate: func(value *postgresSystemConfigRelation) { value.SecurityBarrier = false }},
		{name: "security invoker", mutate: func(value *postgresSystemConfigRelation) { value.SecurityInvoker = true }},
		{name: "no select", mutate: func(value *postgresSystemConfigRelation) { value.CanSelect = false }},
		{name: "insert", mutate: func(value *postgresSystemConfigRelation) { value.CanInsert = true }},
		{name: "update", mutate: func(value *postgresSystemConfigRelation) { value.CanUpdate = true }},
		{name: "delete", mutate: func(value *postgresSystemConfigRelation) { value.CanDelete = true }},
		{name: "maintain", mutate: func(value *postgresSystemConfigRelation) { value.CanMaintain = true }},
		{name: "column update", mutate: func(value *postgresSystemConfigRelation) { value.CanColumnUpdate = true }},
		{name: "extra column", mutate: func(value *postgresSystemConfigRelation) { value.Columns = append(value.Columns, "secret") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := ready
			candidate.Columns = append([]string(nil), ready.Columns...)
			test.mutate(&candidate)
			if err := verifyPostgresReadOnlyView(candidate, requiredSystemConfigColumns); !errors.Is(err, ErrSchemaNotReady) {
				t.Fatalf("unsafe projection error = %v", err)
			}
		})
	}
}

func TestGenericAndPrivateSystemConfigsRetainTheSameClosedColumnFingerprint(t *testing.T) {
	t.Parallel()

	if len(requiredSystemConfigColumns) != 7 || len(genericSystemConfigColumns) != 7 {
		t.Fatalf("private/generic column counts = %d/%d", len(requiredSystemConfigColumns), len(genericSystemConfigColumns))
	}
	for index, column := range requiredSystemConfigColumns {
		if genericSystemConfigColumns[index] != column {
			t.Fatalf("generic column %d = %q, want %q", index, genericSystemConfigColumns[index], column)
		}
	}
}
