package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shop-r1/mss-shop/services/internal/legacyreceipt"
	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const (
	receiptConfigMapName = "mss-shop-legacy-import-receipt"
	receiptContentKey    = "r1shop.io/content-sha256"
	receiptEvidenceKey   = "r1shop.io/evidence-contract"
	receiptEvidenceValue = "legacy-import-receipt-v1"
	legacyManifestSHA256 = "c108b11543f41bbd8384540b7314909cd8056e3a141cc7447c443cb98c7e6e5b"
	maxReceiptBytes      = 1 << 20
)

var importedTableNames = []string{
	"activities", "activity_links", "brands", "categories", "classes",
	"collections", "consignees", "consumers", "coupon_links", "coupon_parents",
	"coupons", "courier_installs", "courier_links", "courier_pack_rules",
	"courier_templates", "couriers", "finance_logs", "finances",
	"function_circles", "gold_withdraws", "goods", "goods_assembles",
	"goods_infos", "goods_shipping_warehouses", "goods_specifications",
	"inventories", "inventory_checks", "inventory_tracks", "member_goods",
	"member_levels", "members", "message_events", "message_templates",
	"message_users", "messages", "order_goods", "order_unit_packs", "orders",
	"payment_installs", "payment_orders", "payments", "real_warehouses",
	"receipt_goods", "receipts", "sell_goods", "sells", "senders",
	"shipping_warehouses", "shopping_carts", "show_categories", "system_configs",
}

func loadReceiptEvidence(ctx context.Context, opts options) ([]byte, error) {
	rootOutput, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	root := strings.TrimSpace(string(rootOutput))
	if err != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("resolve trusted receipt evidence root")
	}
	expected := filepath.Join(root, "docs", "evidence", "legacy-import", opts.importReceiptSHA256, "receipt.json")
	if opts.receiptFile != expected {
		return nil, errors.New("verifier receipt must be the fixed SHA-addressed committed evidence path")
	}
	relative, err := filepath.Rel(root, expected)
	if err != nil || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("fixed receipt evidence escapes the clean checkout")
	}
	tracked, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--error-unmatch", "--", relative).Output()
	if err != nil || strings.TrimSpace(string(tracked)) != filepath.ToSlash(relative) {
		return nil, errors.New("fixed receipt evidence is not tracked by the clean checkout")
	}
	info, err := os.Lstat(expected)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("fixed committed receipt evidence is unavailable")
	}
	file, err := os.Open(expected)
	if err != nil {
		return nil, errors.New("open fixed committed receipt evidence")
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxReceiptBytes+1))
	if err != nil || len(encoded) == 0 || len(encoded) > maxReceiptBytes {
		return nil, errors.New("read complete fixed receipt evidence")
	}
	if err := validateReceiptEvidence(encoded, opts.importReceiptSHA256); err != nil {
		return nil, err
	}
	return append([]byte(nil), encoded...), nil
}

func validateReceiptEvidence(encoded []byte, expectedSHA string) error {
	receipt, err := legacyreceipt.Parse(encoded)
	if err != nil || !validReceipt(expectedSHA) || receipt.SHA256 != expectedSHA ||
		receipt.TargetDatabase != stage.DatabaseName || receipt.ManifestSHA256 != legacyManifestSHA256 ||
		len(receipt.Tables) != len(importedTableNames) {
		return errors.New("fixed receipt evidence does not match its exact isolated import binding")
	}
	for index, table := range receipt.Tables {
		if table.Name != importedTableNames[index] {
			return errors.New("fixed receipt evidence table inventory differs from the compiled importer manifest")
		}
		structureOnly := table.Name == "orders" || table.Name == "order_goods"
		if structureOnly && (table.Mode != "structure-only" || table.TargetRows != 0) {
			return errors.New("fixed receipt evidence violates the empty order boundary")
		}
		if !structureOnly && table.Mode != "copied" {
			return errors.New("fixed receipt evidence contains an unapproved structure-only table")
		}
	}
	return nil
}

func desiredReceiptConfigMap(revision, receiptSHA string, evidence []byte) (*corev1.ConfigMap, error) {
	if !validRevision(revision) || !validReceipt(receiptSHA) || validateReceiptEvidence(evidence, receiptSHA) != nil {
		return nil, errors.New("build fixed receipt evidence ConfigMap")
	}
	contentDigest := sha256.Sum256(evidence)
	immutable := true
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      receiptConfigMapName,
			Namespace: stage.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       receiptConfigMapName,
				"app.kubernetes.io/instance":   stage.Namespace,
				"app.kubernetes.io/component":  "evidence",
				"app.kubernetes.io/part-of":    "mss-shop",
				"app.kubernetes.io/managed-by": operatorManager,
				"r1shop.io/environment":        "dev",
			},
			Annotations: map[string]string{
				operatorBindingKey: stage.Namespace + ":ConfigMap:" + receiptConfigMapName,
				revisionKey:        revision,
				receiptKey:         receiptSHA,
				receiptContentKey:  hex.EncodeToString(contentDigest[:]),
				receiptEvidenceKey: receiptEvidenceValue,
			},
		},
		Immutable: &immutable,
		Data:      map[string]string{"receipt.json": string(evidence)},
	}, nil
}

func validateReceiptConfigMap(observed, desired *corev1.ConfigMap, persisted bool) error {
	if observed == nil || desired == nil || observed.Namespace != stage.Namespace ||
		observed.Name != receiptConfigMapName || observed.DeletionTimestamp != nil ||
		len(observed.OwnerReferences) != 0 || len(observed.Finalizers) != 0 ||
		!equalStringMap(observed.Labels, desired.Labels) || !equalStringMap(observed.Annotations, desired.Annotations) ||
		observed.Immutable == nil || !*observed.Immutable || len(observed.BinaryData) != 0 ||
		!equalStringMap(observed.Data, desired.Data) || observed.UID == "" ||
		(persisted && observed.ResourceVersion == "") {
		return errors.New("receipt evidence ConfigMap is not byte-exact and immutable")
	}
	return nil
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
