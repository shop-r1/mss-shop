package tenancy

import (
	"errors"
	"strings"
	"testing"
)

func TestParseTenantID(t *testing.T) {
	t.Parallel()
	id, err := ParseTenantID("tenant-018f")
	if err != nil {
		t.Fatal(err)
	}
	if id.String() != "tenant-018f" {
		t.Fatalf("tenant id = %q", id)
	}

	for _, invalid := range []string{"", " tenant", "tenant ", strings.Repeat("x", 129)} {
		if _, err := ParseTenantID(invalid); !errors.Is(err, ErrInvalidTenantID) {
			t.Fatalf("value %q error = %v, want ErrInvalidTenantID", invalid, err)
		}
	}
}
