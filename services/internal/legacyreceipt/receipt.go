// Package legacyreceipt parses the public, count-only one-time import receipt.
// It intentionally knows nothing about database credentials or row values.
package legacyreceipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const Version = "mss-shop-legacy-import/v1"

type Table struct {
	Name         string `json:"name"`
	Mode         string `json:"mode"`
	SourceRows   int64  `json:"sourceRows"`
	TargetRows   int64  `json:"targetRows"`
	SourceSHA256 string `json:"sourceSHA256"`
	TargetSHA256 string `json:"targetSHA256"`
}

type Receipt struct {
	Version        string  `json:"version"`
	TargetDatabase string  `json:"targetDatabase"`
	ManifestSHA256 string  `json:"manifestSHA256"`
	SchemaSHA256   string  `json:"schemaSHA256"`
	Tables         []Table `json:"tables"`
	SHA256         string  `json:"sha256"`
}

type payload struct {
	Version        string  `json:"version"`
	TargetDatabase string  `json:"targetDatabase"`
	ManifestSHA256 string  `json:"manifestSHA256"`
	SchemaSHA256   string  `json:"schemaSHA256"`
	Tables         []Table `json:"tables"`
}

// Parse accepts exactly one complete JSON receipt. Unknown fields, trailing
// values, noncanonical digests, and a mismatched canonical payload digest are
// all rejected before a caller may trust any evidence.
func Parse(encoded []byte) (Receipt, error) {
	if len(encoded) == 0 || len(encoded) > 1<<20 {
		return Receipt{}, errors.New("receipt is incomplete")
	}
	if err := rejectDuplicateKeys(encoded); err != nil {
		return Receipt{}, errors.New("receipt is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var result Receipt
	if err := decoder.Decode(&result); err != nil {
		return Receipt{}, errors.New("receipt is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Receipt{}, errors.New("receipt is incomplete")
	}
	if err := Validate(result); err != nil {
		return Receipt{}, err
	}
	return result, nil
}

func rejectDuplicateKeys(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := inspectJSONValue(decoder); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func inspectJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			key, keyErr := decoder.Token()
			name, isName := key.(string)
			if keyErr != nil || !isName {
				return errors.New("invalid JSON object")
			}
			if _, duplicate := seen[name]; duplicate {
				return errors.New("duplicate JSON field")
			}
			seen[name] = struct{}{}
			if err := inspectJSONValue(decoder); err != nil {
				return err
			}
		}
		end, endErr := decoder.Token()
		if endErr != nil || end != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := inspectJSONValue(decoder); err != nil {
				return err
			}
		}
		end, endErr := decoder.Token()
		if endErr != nil || end != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func Validate(value Receipt) error {
	if value.Version != Version || value.TargetDatabase == "" || !validDigest(value.ManifestSHA256) || !validDigest(value.SchemaSHA256) || !validDigest(value.SHA256) || len(value.Tables) == 0 {
		return errors.New("receipt is invalid")
	}
	seen := make(map[string]struct{}, len(value.Tables))
	for _, table := range value.Tables {
		if table.Name == "" || table.SourceRows < 0 || table.TargetRows < 0 || !validDigest(table.SourceSHA256) || !validDigest(table.TargetSHA256) {
			return errors.New("receipt table evidence is invalid")
		}
		if _, duplicate := seen[table.Name]; duplicate {
			return errors.New("receipt table inventory is invalid")
		}
		seen[table.Name] = struct{}{}
		if table.Mode != "copied" && table.Mode != "structure-only" {
			return errors.New("receipt table mode is invalid")
		}
		if table.Mode == "copied" && (table.SourceRows != table.TargetRows || table.SourceSHA256 != table.TargetSHA256) {
			return errors.New("receipt copied evidence differs")
		}
		if table.Mode == "structure-only" && table.TargetRows != 0 {
			return errors.New("receipt structure-only target is nonempty")
		}
	}
	canonical, err := json.Marshal(payload{Version: value.Version, TargetDatabase: value.TargetDatabase, ManifestSHA256: value.ManifestSHA256, SchemaSHA256: value.SchemaSHA256, Tables: value.Tables})
	if err != nil {
		return errors.New("receipt is invalid")
	}
	digest := sha256.Sum256(canonical)
	if value.SHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("receipt digest does not match payload")
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
