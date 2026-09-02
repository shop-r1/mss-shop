package memberlevels

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestCreateAndUpdateInputsNormalizeOnlyOwnedFields(t *testing.T) {
	t.Parallel()

	name := "  Standard  "
	discount := "10.50"
	status := StatusEnabled
	source := "  100000000000000002  "
	created, err := (CreateMemberLevelInput{
		Name: &name, DiscountPercent: &discount, Status: &status,
		PaymentPolicySourceLevelID: &source,
	}).values()
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Standard" || created.DiscountPercent != "10.5" ||
		created.Status != legacyEnabledStatus || created.PaymentPolicySourceLevelID != "100000000000000002" {
		t.Fatalf("normalized create values = %#v", created)
	}

	revision := strings.Repeat("a", 64)
	updated, err := (UpdateMemberLevelInput{
		Name: &name, DiscountPercent: &discount, Status: &status, Revision: &revision,
	}).values()
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != created.Name || updated.DiscountPercent != created.DiscountPercent ||
		updated.Status != created.Status || updated.Revision != revision {
		t.Fatalf("normalized update values = %#v", updated)
	}
}

func TestDTORejectsInvalidNamesDiscountsStatusesSourcesAndRevisions(t *testing.T) {
	t.Parallel()

	invalidUTF8 := string([]byte{0xff})
	for _, name := range []string{"", "   ", strings.Repeat("界", 34), invalidUTF8} {
		candidate := name
		if _, err := requiredName(&candidate); !errors.Is(err, ErrValidation) {
			t.Errorf("invalid name %q error = %v", name, err)
		}
	}
	if _, err := requiredName(nil); !errors.Is(err, ErrValidation) {
		t.Errorf("missing name error = %v", err)
	}

	validDiscounts := map[string]string{
		"0": "0", "0.00": "0", "01": "1", "99.90": "99.9", "100.00": "100",
	}
	for input, want := range validDiscounts {
		candidate := input
		got, err := requiredDiscount(&candidate)
		if err != nil || got != want {
			t.Errorf("discount %q = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, discount := range []string{"", "+1", "-1", ".5", "1.", "1.234", "100.01", "101"} {
		candidate := discount
		if _, err := requiredDiscount(&candidate); !errors.Is(err, ErrValidation) {
			t.Errorf("invalid discount %q error = %v", discount, err)
		}
	}

	unknown := StatusUnknown
	if _, err := requiredStatus(&unknown); !errors.Is(err, ErrValidation) {
		t.Errorf("unknown writable status error = %v", err)
	}
	blankSource := "   "
	name, discount, enabled := "Standard", "10", StatusEnabled
	if _, err := (CreateMemberLevelInput{
		Name: &name, DiscountPercent: &discount, Status: &enabled,
		PaymentPolicySourceLevelID: &blankSource,
	}).values(); !errors.Is(err, ErrValidation) {
		t.Errorf("blank explicit source error = %v", err)
	}

	for _, revision := range []string{
		"", strings.Repeat("a", 63), strings.Repeat("a", 65),
		strings.Repeat("A", 64), strings.Repeat("g", 64),
	} {
		candidate := revision
		if _, err := requiredRevision(&candidate); !errors.Is(err, ErrValidation) {
			t.Errorf("invalid revision %q error = %v", revision, err)
		}
	}
}

func TestListOptionsRejectUnknownDuplicateAndOverflowingQueryValues(t *testing.T) {
	t.Parallel()

	parsed, err := parseListOptions(url.Values{
		"current": {"2"}, "pageSize": {"50"}, "q": {" Standard "},
		"status": {"enabled"}, "isDefault": {"true"},
		"sortBy": {"name"}, "sortOrder": {"asc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Current != 2 || parsed.PageSize != 50 || parsed.Query != "Standard" ||
		parsed.Status == nil || *parsed.Status != legacyEnabledStatus ||
		parsed.IsDefault == nil || !*parsed.IsDefault || parsed.SortBy != "name" || parsed.SortOrder != "asc" {
		t.Fatalf("parsed list options = %#v", parsed)
	}

	for name, values := range map[string]url.Values{
		"unknown":       {"tenantId": {"forged"}},
		"duplicate":     {"current": {"1", "2"}},
		"page overflow": {"current": {strings.Repeat("9", 100)}},
		"bad bool":      {"isDefault": {"yes"}},
		"bad status":    {"status": {"unknown"}},
		"bad sort":      {"sortBy": {"payment_ids"}},
		"long query":    {"q": {strings.Repeat("界", 34)}},
	} {
		if _, err := parseListOptions(values); err == nil {
			t.Errorf("%s query was accepted", name)
		}
	}
}
