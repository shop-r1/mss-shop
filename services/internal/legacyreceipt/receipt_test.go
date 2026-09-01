package legacyreceipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func completeReceipt(t *testing.T) Receipt {
	t.Helper()
	value := Receipt{Version: Version, TargetDatabase: "mss_shop_dev", ManifestSHA256: digest("manifest"), SchemaSHA256: digest("schema"), Tables: []Table{{Name: "activities", Mode: "copied", SourceRows: 1, TargetRows: 1, SourceSHA256: digest("rows"), TargetSHA256: digest("rows")}}}
	encoded, err := json.Marshal(payload{Version: value.Version, TargetDatabase: value.TargetDatabase, ManifestSHA256: value.ManifestSHA256, SchemaSHA256: value.SchemaSHA256, Tables: value.Tables})
	if err != nil {
		t.Fatal(err)
	}
	value.SHA256 = digest(string(encoded))
	return value
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestParseRequiresWholeStrictReceipt(t *testing.T) {
	t.Parallel()
	value := completeReceipt(t)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(encoded); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}
	for _, bad := range [][]byte{append(encoded, []byte(" {}")...), []byte(`{"version":"` + Version + `","unknown":true}`), []byte(`{"version":"` + Version + `","version":"` + Version + `"}`), []byte(`{}`)} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("invalid receipt accepted: %s", bad)
		}
	}
}

func TestValidateRejectsDigestTampering(t *testing.T) {
	t.Parallel()
	value := completeReceipt(t)
	value.Tables[0].TargetRows++
	if err := Validate(value); err == nil {
		t.Fatal("tampered receipt accepted")
	}
}
