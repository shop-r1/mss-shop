package mallsettings

import (
	"errors"
	"testing"

	"github.com/shop-r1/mss-shop/apps/mall-platform/internal/platform/fixedbinding"
)

func TestSystemConfigsRelationSeparatesPostgresPrivateReadFromSQLiteCompatibility(t *testing.T) {
	t.Parallel()
	binding := mallSettingsTestBinding()

	postgres, postgresWritable, err := systemConfigsRelation(binding, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if postgres != `mss_m_aussibuy_biz.r1_mall_settings_system_configs` || postgresWritable {
		t.Fatalf("PostgreSQL relation/writable = %q/%t", postgres, postgresWritable)
	}

	sqlite, sqliteWritable, err := systemConfigsRelation(binding, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if sqlite != `mss_m_aussibuy_biz.system_configs` || !sqliteWritable {
		t.Fatalf("SQLite relation/writable = %q/%t", sqlite, sqliteWritable)
	}

	if _, _, err := systemConfigsRelation(binding, "mysql"); !errors.Is(err, ErrSchemaNotReady) {
		t.Fatalf("unsupported dialect error = %v", err)
	}
}

func TestRepositoryPutFailsBeforeDatabaseWhenRelationIsReadOnly(t *testing.T) {
	t.Parallel()

	repository := &Repository{writable: false}
	if _, err := repository.PutGeneral(t.Context(), GeneralSettings{}); !errors.Is(err, ErrMutationDisabled) {
		t.Fatalf("read-only repository error = %v", err)
	}
}

func mallSettingsTestBinding() fixedbinding.Binding {
	return fixedbinding.Binding{
		TenantID: "tenant-aussibuy-dev", AdminTenantID: fixedbinding.MSS137AdminTenantID,
		LegacyTenantID: "518729051064631297", BusinessSchema: "mss_m_aussibuy_biz",
	}
}
