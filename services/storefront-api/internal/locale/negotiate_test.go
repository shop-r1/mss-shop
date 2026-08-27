package locale

import "testing"

func TestNegotiateHonorsQualityAndAliases(t *testing.T) {
	t.Parallel()

	enabled := []string{"zh-CN", "en-US"}
	if got := Negotiate("zh-CN;q=0.4,en-US;q=0.9", enabled, "zh-CN"); got != "en-US" {
		t.Fatalf("Negotiate() = %q, want en-US", got)
	}
	if got := Negotiate("zh-Hans", enabled, "en-US"); got != "zh-CN" {
		t.Fatalf("Negotiate() alias = %q, want zh-CN", got)
	}
}

func TestNegotiateDoesNotCollapseTraditionalChinese(t *testing.T) {
	t.Parallel()

	if got := Negotiate("zh-TW", []string{"zh-CN", "en-US"}, "en-US"); got != "en-US" {
		t.Fatalf("Negotiate() = %q, want tenant default en-US", got)
	}
}
