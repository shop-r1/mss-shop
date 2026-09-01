package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/shop-r1/mss-shop/services/legacy-importer/internal/manifest"
)

const receiptVersion = "mss-shop-legacy-import/v1"

func writeCompleteReceipt(output io.Writer, encoded []byte) error {
	if output == nil || len(encoded) == 0 {
		return errors.New("receipt output is incomplete")
	}
	written, err := output.Write(encoded)
	if err != nil || written != len(encoded) {
		return errors.New("receipt output is incomplete")
	}
	return nil
}

type tableEvidence struct {
	SourceRows   int64
	TargetRows   int64
	SourceSHA256 string
	TargetSHA256 string
}

// TableReceipt contains count and digest evidence only. It never contains a
// row value.
type TableReceipt struct {
	Name         string `json:"name"`
	Mode         string `json:"mode"`
	SourceRows   int64  `json:"sourceRows"`
	TargetRows   int64  `json:"targetRows"`
	SourceSHA256 string `json:"sourceSHA256"`
	TargetSHA256 string `json:"targetSHA256"`
}

type receiptPayload struct {
	Version        string         `json:"version"`
	TargetDatabase string         `json:"targetDatabase"`
	ManifestSHA256 string         `json:"manifestSHA256"`
	SchemaSHA256   string         `json:"schemaSHA256"`
	Tables         []TableReceipt `json:"tables"`
}

// Receipt is the safe, deterministic audit output. SHA256 covers the exact
// canonical JSON encoding of every preceding field.
type Receipt struct {
	Version        string         `json:"version"`
	TargetDatabase string         `json:"targetDatabase"`
	ManifestSHA256 string         `json:"manifestSHA256"`
	SchemaSHA256   string         `json:"schemaSHA256"`
	Tables         []TableReceipt `json:"tables"`
	SHA256         string         `json:"sha256"`
}

func buildReceipt(tables []manifest.Table, evidence map[string]tableEvidence) (Receipt, error) {
	encodedPlan, err := json.Marshal(tables)
	if err != nil {
		return Receipt{}, errors.New("encode compiled import plan failed")
	}
	schemaDigest := sha256.Sum256(encodedPlan)
	payload := receiptPayload{
		Version:        receiptVersion,
		TargetDatabase: targetDatabase,
		ManifestSHA256: manifest.ReviewedColumnsSHA256,
		SchemaSHA256:   hex.EncodeToString(schemaDigest[:]),
		Tables:         make([]TableReceipt, 0, len(tables)),
	}
	if len(evidence) != len(tables) {
		return Receipt{}, errors.New("receipt table evidence inventory is incomplete")
	}
	for _, table := range tables {
		tableEvidence, exists := evidence[table.Name]
		if !exists || tableEvidence.SourceRows < 0 || tableEvidence.TargetRows < 0 ||
			!validSHA256(tableEvidence.SourceSHA256) || !validSHA256(tableEvidence.TargetSHA256) {
			return Receipt{}, errors.New("receipt table evidence is invalid")
		}
		mode := "copied"
		if !table.CopyRows {
			mode = "structure-only"
			if tableEvidence.TargetRows != 0 {
				return Receipt{}, errors.New("structure-only target row count must be zero")
			}
		} else if tableEvidence.SourceRows != tableEvidence.TargetRows ||
			tableEvidence.SourceSHA256 != tableEvidence.TargetSHA256 {
			return Receipt{}, errors.New("receipt source and target evidence differs")
		}
		payload.Tables = append(payload.Tables, TableReceipt{
			Name:         table.Name,
			Mode:         mode,
			SourceRows:   tableEvidence.SourceRows,
			TargetRows:   tableEvidence.TargetRows,
			SourceSHA256: tableEvidence.SourceSHA256,
			TargetSHA256: tableEvidence.TargetSHA256,
		})
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return Receipt{}, errors.New("encode receipt payload failed")
	}
	digest := sha256.Sum256(canonical)
	receiptDigest := hex.EncodeToString(digest[:])
	if receiptDigest == strings.Repeat("0", sha256.Size*2) {
		return Receipt{}, errors.New("receipt digest is invalid")
	}
	return Receipt{
		Version:        payload.Version,
		TargetDatabase: payload.TargetDatabase,
		ManifestSHA256: payload.ManifestSHA256,
		SchemaSHA256:   payload.SchemaSHA256,
		Tables:         payload.Tables,
		SHA256:         receiptDigest,
	}, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
