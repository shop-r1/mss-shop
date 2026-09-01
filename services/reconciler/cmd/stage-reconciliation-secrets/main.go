// Command stage-reconciliation-secrets creates only the two generated
// application Secrets and the short-lived reconciliation bootstrap Secret in
// the isolated mss-shop-dev namespace. It never reads or writes r1shop-dev.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	kubernetesdriver "github.com/shop-r1/mss-shop/services/reconciler/internal/driver/kubernetes"
	postgresdriver "github.com/shop-r1/mss-shop/services/reconciler/internal/driver/postgres"
	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

const (
	environment  = "mss-shop-dev"
	operatorName = "r1shop-operator"
	contract     = "isolated-dev-v1"
	zeroRevision = "0000000000000000000000000000000000000000"

	bindingAnnotation  = "r1shop.io/operator-binding"
	contractAnnotation = "r1shop.io/credential-contract"

	postgresAuthSecret = "mss-shop-postgres-auth"
	redisAuthSecret    = "mss-shop-redis-auth"
	bootstrapSecret    = "mss-shop-reconciler-bootstrap"

	postgresUser     = "mss_shop_bootstrap"
	postgresDatabase = "mss_shop_dev"

	legacyReceiptVersion     = "mss-shop-legacy-import/v1"
	legacyManifestSHA256     = "c108b11543f41bbd8384540b7314909cd8056e3a141cc7447c443cb98c7e6e5b"
	verificationVersion      = "mss-shop-disposable-verification/v1"
	importedDatabaseMarker   = "mss-shop-isolated-dev:legacy-import:v1:"
	evidenceDirectory        = "docs/evidence/legacy-import"
	verifierImageRepository  = "ghcr.io/shop-r1/mss-shop-legacy-importer"
	maximumReceiptBytes      = 1024 * 1024
	maximumVerificationBytes = 64 * 1024
	verifierPodPrefix        = "mss-shop-legacy-verify-"
	observedRevisionPrefix   = 32
)

var (
	fullRevision  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	receiptSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	safePassword  = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	podUID        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dnsLabel      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
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

var (
	receiptJSONFields = []string{
		"version", "targetDatabase", "manifestSHA256", "schemaSHA256", "tables", "sha256",
	}
	tableReceiptJSONFields = []string{
		"name", "mode", "sourceRows", "targetRows", "sourceSHA256", "targetSHA256",
	}
	verificationJSONFields = []string{
		"version", "targetDatabase", "databaseMarker", "receiptSHA256", "manifestSHA256",
		"schemaSHA256", "tableCount", "ordersRows", "orderGoodsRows", "namespace", "podName",
		"podUID", "revision", "imageRepository", "imageDigest", "imageReference",
	}
)

type options struct {
	kubeconfig           string
	environment          string
	revision             string
	importReceiptSHA256  string
	receiptEvidence      string
	verificationEvidence string
}

type checkoutState struct {
	root     string
	revision string
}

type tableReceipt struct {
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
	Tables         []tableReceipt `json:"tables"`
}

type importReceipt struct {
	Version        string         `json:"version"`
	TargetDatabase string         `json:"targetDatabase"`
	ManifestSHA256 string         `json:"manifestSHA256"`
	SchemaSHA256   string         `json:"schemaSHA256"`
	Tables         []tableReceipt `json:"tables"`
	SHA256         string         `json:"sha256"`
}

type verificationEvidence struct {
	Version         string `json:"version"`
	TargetDatabase  string `json:"targetDatabase"`
	DatabaseMarker  string `json:"databaseMarker"`
	ReceiptSHA256   string `json:"receiptSHA256"`
	ManifestSHA256  string `json:"manifestSHA256"`
	SchemaSHA256    string `json:"schemaSHA256"`
	TableCount      int    `json:"tableCount"`
	OrdersRows      int64  `json:"ordersRows"`
	OrderGoodsRows  int64  `json:"orderGoodsRows"`
	Namespace       string `json:"namespace"`
	PodName         string `json:"podName"`
	PodUID          string `json:"podUID"`
	Revision        string `json:"revision"`
	ImageRepository string `json:"imageRepository"`
	ImageDigest     string `json:"imageDigest"`
	ImageReference  string `json:"imageReference"`
}

type runDependencies struct {
	inspectCheckout func(context.Context, string) (checkoutState, error)
	newClient       func(string) (kubernetes.Interface, error)
	random          io.Reader
}

type convergeResult struct {
	applicationSecretsChanged bool
	bootstrapCreated          bool
	bootstrapExactRetry       bool
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("isolated reconciliation credential stage stopped safely", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	return runWithDependencies(ctx, arguments, runDependencies{
		inspectCheckout: inspectCheckout,
		newClient:       newKubernetesClient,
		random:          rand.Reader,
	})
}

func runWithDependencies(ctx context.Context, arguments []string, dependencies runDependencies) error {
	opts, err := parseOptions(arguments)
	if err != nil {
		return err
	}
	if dependencies.inspectCheckout == nil || dependencies.newClient == nil || dependencies.random == nil {
		return errors.New("isolated reconciliation credential stage dependencies are invalid")
	}
	checkout, err := dependencies.inspectCheckout(ctx, opts.revision)
	if err != nil {
		return err
	}
	if err := validateCommittedEvidence(
		ctx,
		checkout,
		opts.receiptEvidence,
		opts.verificationEvidence,
		opts.importReceiptSHA256,
	); err != nil {
		return err
	}
	client, err := dependencies.newClient(opts.kubeconfig)
	if err != nil {
		return err
	}
	result, err := convergeReconciliationSecrets(ctx, client, opts.importReceiptSHA256, dependencies.random)
	if err != nil {
		return err
	}
	slog.Info(
		"isolated reconciliation credentials completed",
		"environment", environment,
		"revision", opts.revision,
		"applicationSecretsChanged", result.applicationSecretsChanged,
		"bootstrapCreated", result.bootstrapCreated,
		"bootstrapExactRetry", result.bootstrapExactRetry,
	)
	return nil
}

func newKubernetesClient(kubeconfig string) (kubernetes.Interface, error) {
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, errors.New("load trusted isolated reconciliation credential operator kubeconfig")
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, errors.New("initialize trusted isolated reconciliation credential operator Kubernetes client")
	}
	return client, nil
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("mss-shop-stage-reconciliation-secrets", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result options
	flags.StringVar(&result.kubeconfig, "kubeconfig", "", "absolute trusted operator kubeconfig path")
	flags.StringVar(&result.environment, "environment", "", "required isolated environment confirmation")
	flags.StringVar(&result.revision, "revision", "", "complete immutable Git revision")
	flags.StringVar(&result.importReceiptSHA256, "import-receipt-sha256", "", "verified legacy import receipt SHA-256")
	flags.StringVar(&result.receiptEvidence, "receipt-evidence", "", "absolute committed importer receipt evidence path")
	flags.StringVar(&result.verificationEvidence, "verification-evidence", "", "absolute committed disposable verifier evidence path")
	if err := flags.Parse(arguments); err != nil {
		return options{}, errors.New("parse isolated reconciliation credential options")
	}
	if flags.NArg() != 0 || !filepath.IsAbs(result.kubeconfig) || result.environment != environment ||
		!validRevision(result.revision) || !validReceipt(result.importReceiptSHA256) ||
		!filepath.IsAbs(result.receiptEvidence) || !filepath.IsAbs(result.verificationEvidence) {
		return options{}, errors.New("isolated reconciliation credential stage requires absolute kubeconfig and evidence paths, mss-shop-dev confirmation, complete nonzero lowercase Git SHA, and complete nonzero lowercase import receipt SHA-256")
	}
	return result, nil
}

func validRevision(value string) bool {
	return fullRevision.MatchString(value) && value != zeroRevision
}

func validReceipt(value string) bool {
	return receiptSHA256.MatchString(value) && strings.Trim(value, "0") != ""
}

func inspectCheckout(ctx context.Context, revision string) (checkoutState, error) {
	rootOutput, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return checkoutState{}, errors.New("inspect trusted isolated reconciliation credential checkout")
	}
	root, err := filepath.Abs(strings.TrimSpace(string(rootOutput)))
	if err != nil {
		return checkoutState{}, errors.New("inspect trusted isolated reconciliation credential checkout")
	}
	root, err = filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return checkoutState{}, errors.New("inspect trusted isolated reconciliation credential checkout")
	}
	head, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return checkoutState{}, errors.New("trusted isolated reconciliation credential checkout does not match the requested revision")
	}
	status, statusErr := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=normal").Output()
	if err := validateCheckoutRevision(revision, head, status, statusErr); err != nil {
		return checkoutState{}, err
	}
	return checkoutState{root: root, revision: revision}, nil
}

func validateCheckoutRevision(revision string, head, status []byte, statusErr error) error {
	if !validRevision(revision) || strings.TrimSpace(string(head)) != revision {
		return errors.New("trusted isolated reconciliation credential checkout does not match the requested revision")
	}
	if statusErr != nil {
		return errors.New("inspect trusted isolated reconciliation credential checkout")
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("isolated reconciliation credential stage requires a clean checkout")
	}
	return nil
}

func validateCommittedEvidence(
	ctx context.Context,
	checkout checkoutState,
	receiptPath string,
	verificationPath string,
	expectedReceiptSHA256 string,
) error {
	if !filepath.IsAbs(checkout.root) || !validRevision(checkout.revision) ||
		!validReceipt(expectedReceiptSHA256) {
		return errors.New("trusted evidence checkout binding is invalid")
	}
	// The receipt digest exists before either evidence commit, so it is the only
	// non-self-referential directory key. The verifier revision is independently
	// bound by its image fields, and the checkout revision binds this operator
	// plus both committed blobs.
	receiptRelative, verificationRelative, err := fixedEvidencePaths(
		checkout.root,
		receiptPath,
		verificationPath,
		expectedReceiptSHA256,
	)
	if err != nil {
		return errors.New("committed evidence paths do not match the exact receipt-bound directory")
	}
	receiptBytes, err := readCommittedEvidence(
		ctx,
		checkout,
		receiptPath,
		receiptRelative,
		maximumReceiptBytes,
	)
	if err != nil {
		return errors.New("committed legacy import receipt evidence is invalid")
	}
	verificationBytes, err := readCommittedEvidence(
		ctx,
		checkout,
		verificationPath,
		verificationRelative,
		maximumVerificationBytes,
	)
	if err != nil {
		return errors.New("committed disposable verification evidence is invalid")
	}

	var receipt importReceipt
	if err := decodeStrictJSON(receiptBytes, &receipt); err != nil ||
		validateImportReceipt(receipt, expectedReceiptSHA256) != nil {
		return errors.New("committed legacy import receipt evidence is invalid")
	}
	var verification verificationEvidence
	if err := decodeStrictJSON(verificationBytes, &verification); err != nil ||
		validateVerificationEvidence(verification, receipt, expectedReceiptSHA256) != nil {
		return errors.New("committed disposable verification evidence is invalid")
	}
	if err := validateEvidenceRevisionChain(
		ctx,
		checkout,
		receiptRelative,
		verificationRelative,
		verification.Revision,
	); err != nil {
		return errors.New("committed evidence does not form the reviewed receipt-verifier-operator revision chain")
	}

	head, headErr := exec.CommandContext(ctx, "git", "-C", checkout.root, "rev-parse", "--verify", "HEAD").Output()
	status, statusErr := exec.CommandContext(
		ctx,
		"git", "-C", checkout.root, "status", "--porcelain", "--untracked-files=normal",
	).Output()
	if headErr != nil || validateCheckoutRevision(checkout.revision, head, status, statusErr) != nil {
		return errors.New("isolated reconciliation credential evidence checkout changed during validation")
	}
	return nil
}

func fixedEvidencePaths(root, receiptPath, verificationPath, expectedReceiptSHA256 string) (string, string, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(receiptPath) || !filepath.IsAbs(verificationPath) {
		return "", "", errors.New("evidence paths must be absolute")
	}
	receiptRelative, err := filepath.Rel(root, filepath.Clean(receiptPath))
	if err != nil {
		return "", "", errors.New("resolve receipt evidence path failed")
	}
	verificationRelative, err := filepath.Rel(root, filepath.Clean(verificationPath))
	if err != nil {
		return "", "", errors.New("resolve verification evidence path failed")
	}
	receiptRelative = filepath.ToSlash(receiptRelative)
	verificationRelative = filepath.ToSlash(verificationRelative)
	receiptParts := strings.Split(receiptRelative, "/")
	verificationParts := strings.Split(verificationRelative, "/")
	if len(receiptParts) != 5 || len(verificationParts) != 5 ||
		strings.Join(receiptParts[:3], "/") != evidenceDirectory ||
		strings.Join(verificationParts[:3], "/") != evidenceDirectory ||
		!validReceipt(expectedReceiptSHA256) || receiptParts[3] != expectedReceiptSHA256 ||
		receiptParts[3] != verificationParts[3] ||
		receiptParts[4] != "receipt.json" || verificationParts[4] != "verification.json" {
		return "", "", errors.New("evidence paths do not match the fixed receipt-bound repository layout")
	}
	return receiptRelative, verificationRelative, nil
}

func validateEvidenceRevisionChain(
	ctx context.Context,
	checkout checkoutState,
	receiptRelative string,
	verificationRelative string,
	verifierRevision string,
) error {
	if !validRevision(verifierRevision) || verifierRevision == checkout.revision {
		return errors.New("verifier and operator revisions are not independent")
	}
	if err := exec.CommandContext(
		ctx,
		"git", "-C", checkout.root, "merge-base", "--is-ancestor", verifierRevision, checkout.revision,
	).Run(); err != nil {
		return errors.New("verifier revision is not an ancestor of the operator checkout")
	}
	verifierReceiptBlob, err := committedBlobAtRevision(ctx, checkout.root, verifierRevision, receiptRelative)
	if err != nil || verifierReceiptBlob == "" {
		return errors.New("verifier revision does not contain the exact receipt")
	}
	operatorReceiptBlob, err := committedBlobAtRevision(ctx, checkout.root, checkout.revision, receiptRelative)
	if err != nil || operatorReceiptBlob != verifierReceiptBlob {
		return errors.New("receipt changed after the verifier revision")
	}
	verifierVerificationBlob, err := committedBlobAtRevision(ctx, checkout.root, verifierRevision, verificationRelative)
	if err != nil || verifierVerificationBlob != "" {
		return errors.New("verification evidence predates its verifier execution")
	}
	return nil
}

func committedBlobAtRevision(ctx context.Context, root, revision, relativePath string) (string, error) {
	listing, err := exec.CommandContext(
		ctx,
		"git", "-C", root, "ls-tree", "--full-tree", revision, "--", relativePath,
	).Output()
	if err != nil {
		return "", errors.New("inspect evidence revision object failed")
	}
	line := strings.TrimSuffix(string(listing), "\n")
	if line == "" {
		return "", nil
	}
	sections := strings.SplitN(line, "\t", 2)
	if len(sections) != 2 || sections[1] != relativePath {
		return "", errors.New("evidence revision path is invalid")
	}
	metadata := strings.Fields(sections[0])
	if len(metadata) != 3 || metadata[0] != "100644" || metadata[1] != "blob" || metadata[2] == "" {
		return "", errors.New("evidence revision object shape is invalid")
	}
	return metadata[2], nil
}

func readCommittedEvidence(
	ctx context.Context,
	checkout checkoutState,
	providedPath string,
	relativePath string,
	maximumBytes int64,
) ([]byte, error) {
	expectedPath := filepath.Join(checkout.root, filepath.FromSlash(relativePath))
	if !filepath.IsAbs(providedPath) || filepath.Clean(providedPath) != expectedPath {
		return nil, errors.New("evidence path is outside the fixed committed directory")
	}
	resolvedPath, err := filepath.EvalSymlinks(providedPath)
	if err != nil || resolvedPath != expectedPath {
		return nil, errors.New("evidence path contains a symbolic link")
	}
	info, err := os.Lstat(providedPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumBytes {
		return nil, errors.New("evidence file shape is invalid")
	}

	listing, err := exec.CommandContext(
		ctx,
		"git", "-C", checkout.root, "ls-tree", "--full-tree", checkout.revision, "--", relativePath,
	).Output()
	if err != nil {
		return nil, errors.New("inspect committed evidence object failed")
	}
	line := strings.TrimSuffix(string(listing), "\n")
	sections := strings.SplitN(line, "\t", 2)
	if len(sections) != 2 || sections[1] != relativePath {
		return nil, errors.New("evidence file is not tracked at the fixed path")
	}
	metadata := strings.Fields(sections[0])
	if len(metadata) != 3 || metadata[0] != "100644" || metadata[1] != "blob" || metadata[2] == "" {
		return nil, errors.New("committed evidence object shape is invalid")
	}

	file, err := os.Open(providedPath)
	if err != nil {
		return nil, errors.New("read committed evidence failed")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || len(data) == 0 || int64(len(data)) > maximumBytes {
		return nil, errors.New("read committed evidence failed")
	}
	hashCommand := exec.CommandContext(ctx, "git", "-C", checkout.root, "hash-object", "--stdin")
	hashCommand.Stdin = bytes.NewReader(data)
	objectHash, err := hashCommand.Output()
	if err != nil || strings.TrimSpace(string(objectHash)) != metadata[2] {
		return nil, errors.New("evidence bytes differ from the committed object")
	}
	return data, nil
}

func decodeStrictJSON(data []byte, target any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return errors.New("evidence JSON object keys are invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("decode evidence failed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("evidence must contain exactly one JSON document")
	}
	if err := validateRequiredJSONFields(data, target); err != nil {
		return errors.New("evidence JSON fields are incomplete")
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("evidence contains multiple JSON values")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errors.New("read evidence JSON token failed")
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			key, ok := keyToken.(string)
			if keyErr != nil || !ok {
				return errors.New("evidence JSON object key is invalid")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("evidence JSON contains a duplicate object key")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim('}') {
			return errors.New("evidence JSON object is incomplete")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim(']') {
			return errors.New("evidence JSON array is incomplete")
		}
	default:
		return errors.New("evidence JSON delimiter is invalid")
	}
	return nil
}

func validateRequiredJSONFields(data []byte, target any) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return errors.New("decode evidence JSON object failed")
	}
	switch target.(type) {
	case *importReceipt:
		if !hasExactJSONFields(document, receiptJSONFields) {
			return errors.New("legacy import receipt fields are incomplete")
		}
		var tables []json.RawMessage
		if err := json.Unmarshal(document["tables"], &tables); err != nil {
			return errors.New("legacy import receipt tables are invalid")
		}
		for _, rawTable := range tables {
			var table map[string]json.RawMessage
			if err := json.Unmarshal(rawTable, &table); err != nil ||
				!hasExactJSONFields(table, tableReceiptJSONFields) {
				return errors.New("legacy import receipt table fields are incomplete")
			}
		}
	case *verificationEvidence:
		if !hasExactJSONFields(document, verificationJSONFields) {
			return errors.New("disposable verification fields are incomplete")
		}
	default:
		return errors.New("unapproved evidence JSON type")
	}
	return nil
}

func hasExactJSONFields(actual map[string]json.RawMessage, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for _, name := range expected {
		if _, exists := actual[name]; !exists {
			return false
		}
	}
	return true
}

func validateImportReceipt(receipt importReceipt, expectedSHA256 string) error {
	if receipt.Version != legacyReceiptVersion || receipt.TargetDatabase != postgresDatabase ||
		receipt.ManifestSHA256 != legacyManifestSHA256 || !validReceipt(receipt.SchemaSHA256) ||
		receipt.SHA256 != expectedSHA256 || len(receipt.Tables) != len(importedTableNames) {
		return errors.New("legacy import receipt boundary is invalid")
	}
	seen := make(map[string]struct{}, len(receipt.Tables))
	for index, table := range receipt.Tables {
		if table.Name != importedTableNames[index] {
			return errors.New("legacy import receipt table inventory is invalid")
		}
		if _, duplicate := seen[table.Name]; duplicate {
			return errors.New("legacy import receipt table inventory is invalid")
		}
		seen[table.Name] = struct{}{}
		if table.SourceRows < 0 || table.TargetRows < 0 ||
			!validReceipt(table.SourceSHA256) || !validReceipt(table.TargetSHA256) {
			return errors.New("legacy import receipt table evidence is invalid")
		}
		structureOnly := table.Name == "orders" || table.Name == "order_goods"
		if structureOnly {
			if table.Mode != "structure-only" || table.TargetRows != 0 {
				return errors.New("legacy import receipt order boundary is invalid")
			}
			continue
		}
		if table.Mode != "copied" || table.SourceRows != table.TargetRows ||
			table.SourceSHA256 != table.TargetSHA256 {
			return errors.New("legacy import receipt copied table evidence differs")
		}
	}
	payload := receiptPayload{
		Version:        receipt.Version,
		TargetDatabase: receipt.TargetDatabase,
		ManifestSHA256: receipt.ManifestSHA256,
		SchemaSHA256:   receipt.SchemaSHA256,
		Tables:         receipt.Tables,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return errors.New("canonicalize legacy import receipt failed")
	}
	digest := sha256.Sum256(canonical)
	if fmt.Sprintf("%x", digest[:]) != expectedSHA256 {
		return errors.New("legacy import receipt canonical payload digest differs")
	}
	return nil
}

func validateVerificationEvidence(
	verification verificationEvidence,
	receipt importReceipt,
	expectedReceiptSHA256 string,
) error {
	digest := strings.TrimPrefix(verification.ImageDigest, "sha256:")
	if verification.Version != verificationVersion || verification.TargetDatabase != postgresDatabase ||
		verification.DatabaseMarker != importedDatabaseMarker+expectedReceiptSHA256 ||
		verification.ReceiptSHA256 != expectedReceiptSHA256 ||
		verification.ManifestSHA256 != legacyManifestSHA256 ||
		verification.SchemaSHA256 != receipt.SchemaSHA256 ||
		verification.TableCount != len(importedTableNames) || verification.OrdersRows != 0 ||
		verification.OrderGoodsRows != 0 || verification.Namespace != stage.Namespace ||
		!validVerifierPodName(verification.PodName, verification.Revision) ||
		!podUID.MatchString(verification.PodUID) || strings.Trim(verification.PodUID, "0-") == "" ||
		!validRevision(verification.Revision) || verification.ImageRepository != verifierImageRepository ||
		!strings.HasPrefix(verification.ImageDigest, "sha256:") || !validReceipt(digest) ||
		verification.ImageReference != verification.ImageRepository+":"+verification.Revision+"@"+verification.ImageDigest {
		return errors.New("disposable verification evidence boundary is invalid")
	}
	return nil
}

func validVerifierPodName(name, revision string) bool {
	return validRevision(revision) && len(name) > len(verifierPodPrefix)+observedRevisionPrefix &&
		len(name) <= 63 && dnsLabel.MatchString(name) &&
		strings.HasPrefix(name, verifierPodPrefix+revision[:observedRevisionPrefix])
}

func convergeReconciliationSecrets(
	ctx context.Context,
	client kubernetes.Interface,
	importReceiptSHA256 string,
	random io.Reader,
) (convergeResult, error) {
	if client == nil || random == nil || !validReceipt(importReceiptSHA256) {
		return convergeResult{}, errors.New("isolated reconciliation credential inputs are invalid")
	}
	if err := validateTargetNamespace(ctx, client); err != nil {
		return convergeResult{}, err
	}
	postgresAuth, err := readInfrastructureAuthSecret(ctx, client, postgresAuthSecret)
	if err != nil {
		return convergeResult{}, err
	}
	redisAuth, err := readInfrastructureAuthSecret(ctx, client, redisAuthSecret)
	if err != nil {
		return convergeResult{}, err
	}
	config := buildStageConfig(postgresAuth, redisAuth, importReceiptSHA256)
	if err := config.Validate(); err != nil {
		return convergeResult{}, errors.New("isolated reconciliation target configuration is invalid")
	}

	driver, err := kubernetesdriver.NewWithRandom(client, random)
	if err != nil {
		return convergeResult{}, errors.New("initialize isolated application credential driver")
	}
	// Resolve all deterministic application collisions before any write.
	if err := driver.Preflight(ctx, config); err != nil {
		return convergeResult{}, err
	}
	existingCredentials, applicationsComplete, err := readExistingApplicationCredentials(ctx, client, config)
	if err != nil {
		return convergeResult{}, err
	}
	existingBootstrap, bootstrapExists, err := readBootstrapSecret(ctx, client)
	if err != nil {
		return convergeResult{}, err
	}
	if bootstrapExists {
		if !applicationsComplete {
			return convergeResult{}, errors.New("isolated reconciliation bootstrap Secret exists without both reviewed application Secrets")
		}
		expected, buildErr := bootstrapSecretData(config.DatabaseDSN, existingCredentials, importReceiptSHA256)
		if buildErr != nil || validateBootstrapSecret(existingBootstrap, expected) != nil {
			return convergeResult{}, errors.New("isolated reconciliation bootstrap Secret has an incompatible contract")
		}
	}

	materials, applicationResult, err := driver.EnsureSecrets(ctx, config)
	if err != nil {
		return convergeResult{}, err
	}
	credentials := materials.DatabaseCredentials()
	expectedBootstrapData, err := bootstrapSecretData(config.DatabaseDSN, credentials, importReceiptSHA256)
	if err != nil {
		return convergeResult{}, err
	}
	result := convergeResult{applicationSecretsChanged: applicationResult.Changed}
	if bootstrapExists {
		if err := validateBootstrapSecret(existingBootstrap, expectedBootstrapData); err != nil {
			return convergeResult{}, err
		}
		result.bootstrapExactRetry = true
		return result, nil
	}

	secrets := client.CoreV1().Secrets(stage.Namespace)
	desired := newBootstrapSecret(expectedBootstrapData)
	stored, createErr := secrets.Create(ctx, desired, metav1.CreateOptions{FieldManager: "mss-shop-stage-reconciliation-secrets"})
	if apierrors.IsAlreadyExists(createErr) {
		stored, createErr = secrets.Get(ctx, bootstrapSecret, metav1.GetOptions{})
	}
	if createErr != nil {
		return convergeResult{}, errors.New("create isolated reconciliation bootstrap Secret failed")
	}
	if err := validateBootstrapSecret(stored, expectedBootstrapData); err != nil {
		return convergeResult{}, err
	}
	result.bootstrapCreated = true
	return result, nil
}

func validateTargetNamespace(ctx context.Context, client kubernetes.Interface) error {
	namespace, err := client.CoreV1().Namespaces().Get(ctx, stage.Namespace, metav1.GetOptions{})
	if err != nil {
		return errors.New("read isolated reconciliation target Namespace failed")
	}
	wantLabels := map[string]string{
		"app.kubernetes.io/name":                     stage.Namespace,
		"app.kubernetes.io/instance":                 stage.Namespace,
		"app.kubernetes.io/component":                "namespace",
		"app.kubernetes.io/part-of":                  "mss-shop",
		"app.kubernetes.io/managed-by":               operatorName,
		"r1shop.io/environment":                      "dev",
		"pod-security.kubernetes.io/enforce":         "restricted",
		"pod-security.kubernetes.io/enforce-version": "v1.32",
		"pod-security.kubernetes.io/audit":           "restricted",
		"pod-security.kubernetes.io/audit-version":   "v1.32",
		"pod-security.kubernetes.io/warn":            "restricted",
		"pod-security.kubernetes.io/warn-version":    "v1.32",
	}
	if namespace.Name != stage.Namespace || namespace.Namespace != "" ||
		!exactNamespaceLabels(namespace.Labels, wantLabels) ||
		!reflect.DeepEqual(namespace.Annotations, map[string]string{
			bindingAnnotation:                   stage.Namespace + ":Namespace:" + stage.Namespace,
			"r1shop.io/infrastructure-contract": contract,
		}) || len(namespace.OwnerReferences) != 0 || len(namespace.Finalizers) != 0 ||
		namespace.DeletionTimestamp != nil || namespace.Status.Phase != corev1.NamespaceActive ||
		(len(namespace.Spec.Finalizers) != 0 &&
			!reflect.DeepEqual(namespace.Spec.Finalizers, []corev1.FinalizerName{corev1.FinalizerKubernetes})) {
		return errors.New("isolated reconciliation target Namespace lacks the exact ownership and active lifecycle contract")
	}
	return nil
}

func exactNamespaceLabels(actual, expected map[string]string) bool {
	if reflect.DeepEqual(actual, expected) {
		return true
	}
	if len(actual) != len(expected)+1 || actual["kubernetes.io/metadata.name"] != stage.Namespace {
		return false
	}
	clone := make(map[string]string, len(actual)-1)
	for key, value := range actual {
		if key != "kubernetes.io/metadata.name" {
			clone[key] = value
		}
	}
	return reflect.DeepEqual(clone, expected)
}

func readInfrastructureAuthSecret(
	ctx context.Context,
	client kubernetes.Interface,
	name string,
) (map[string][]byte, error) {
	secret, err := client.CoreV1().Secrets(stage.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read isolated infrastructure authentication Secret %q failed", name)
	}
	if err := validateInfrastructureAuthSecret(secret, name); err != nil {
		return nil, err
	}
	return cloneData(secret.Data), nil
}

func validateInfrastructureAuthSecret(secret *corev1.Secret, name string) error {
	keys := []string{"password"}
	if name == postgresAuthSecret {
		keys = []string{"database", "password", "username"}
	} else if name != redisAuthSecret {
		return errors.New("unapproved isolated infrastructure authentication Secret")
	}
	if secret == nil || secret.Namespace != stage.Namespace || secret.Name != name ||
		secret.Type != corev1.SecretTypeOpaque || secret.Immutable == nil || !*secret.Immutable ||
		!reflect.DeepEqual(secret.Labels, infrastructureSecretLabels(name)) ||
		!reflect.DeepEqual(secret.Annotations, infrastructureSecretAnnotations(name)) ||
		len(secret.OwnerReferences) != 0 || len(secret.Finalizers) != 0 || secret.DeletionTimestamp != nil ||
		!exactKeys(secret.Data, keys) {
		return fmt.Errorf("isolated infrastructure authentication Secret %q lacks the exact immutable contract", name)
	}
	if !safePassword.Match(secret.Data["password"]) {
		return fmt.Errorf("isolated infrastructure authentication Secret %q contains an incompatible credential", name)
	}
	if name == postgresAuthSecret &&
		(string(secret.Data["username"]) != postgresUser || string(secret.Data["database"]) != postgresDatabase) {
		return errors.New("isolated PostgreSQL authentication Secret has an incompatible identity")
	}
	return nil
}

func infrastructureSecretLabels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/instance":   stage.Namespace,
		"app.kubernetes.io/component":  "credentials",
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": operatorName,
		"r1shop.io/environment":        "dev",
	}
}

func infrastructureSecretAnnotations(name string) map[string]string {
	return map[string]string{
		bindingAnnotation:  stage.Namespace + ":Secret:" + name,
		contractAnnotation: contract,
	}
}

func buildStageConfig(postgresAuth, redisAuth map[string][]byte, importReceiptSHA256 string) stage.Config {
	query := make(url.Values)
	query.Set("sslmode", "verify-full")
	query.Set("sslrootcert", stage.DatabaseCAPath)
	databaseDSN := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(string(postgresAuth["username"]), string(postgresAuth["password"])),
		Host:     net.JoinHostPort(stage.DatabaseHost, fmt.Sprint(stage.DatabasePort)),
		Path:     "/" + string(postgresAuth["database"]),
		RawQuery: query.Encode(),
	}).String()
	return stage.Config{
		Environment:         stage.Environment,
		Namespace:           stage.Namespace,
		DatabaseDSN:         databaseDSN,
		RedisPassword:       append([]byte(nil), redisAuth["password"]...),
		TenantID:            stage.TenantID,
		TenantKey:           stage.TenantKey,
		LegacyTenantID:      stage.LegacyTenantID,
		ImportReceiptSHA256: importReceiptSHA256,
	}
}

func readExistingApplicationCredentials(
	ctx context.Context,
	client kubernetes.Interface,
	config stage.Config,
) (postgresdriver.Credentials, bool, error) {
	names := config.Names()
	secrets := client.CoreV1().Secrets(stage.Namespace)
	tenant, tenantErr := secrets.Get(ctx, names.TenantSecret, metav1.GetOptions{})
	if tenantErr != nil && !apierrors.IsNotFound(tenantErr) {
		return postgresdriver.Credentials{}, false, errors.New("read isolated tenant application Secret failed")
	}
	mall, mallErr := secrets.Get(ctx, names.MallSecret, metav1.GetOptions{})
	if mallErr != nil && !apierrors.IsNotFound(mallErr) {
		return postgresdriver.Credentials{}, false, errors.New("read isolated mall application Secret failed")
	}
	if apierrors.IsNotFound(tenantErr) || apierrors.IsNotFound(mallErr) {
		return postgresdriver.Credentials{}, false, nil
	}
	credentials := postgresdriver.Credentials{
		TenantMigratorPassword: append([]byte(nil), tenant.Data["database-migrator-password"]...),
		TenantRuntimePassword:  append([]byte(nil), tenant.Data["database-runtime-password"]...),
		MallMigratorPassword:   append([]byte(nil), mall.Data["database-migrator-password"]...),
		MallRuntimePassword:    append([]byte(nil), mall.Data["database-runtime-password"]...),
	}
	if err := credentials.Validate(); err != nil {
		return postgresdriver.Credentials{}, false, errors.New("isolated application database credentials are invalid")
	}
	return credentials, true, nil
}

func readBootstrapSecret(
	ctx context.Context,
	client kubernetes.Interface,
) (*corev1.Secret, bool, error) {
	secret, err := client.CoreV1().Secrets(stage.Namespace).Get(ctx, bootstrapSecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("read isolated reconciliation bootstrap Secret failed")
	}
	return secret, true, nil
}

func bootstrapSecretData(
	databaseDSN string,
	credentials postgresdriver.Credentials,
	importReceiptSHA256 string,
) (map[string][]byte, error) {
	if strings.TrimSpace(databaseDSN) == "" || credentials.Validate() != nil || !validReceipt(importReceiptSHA256) {
		return nil, errors.New("isolated reconciliation bootstrap material is invalid")
	}
	return map[string][]byte{
		"database-dsn":             []byte(databaseDSN),
		"tenant-migrator-password": append([]byte(nil), credentials.TenantMigratorPassword...),
		"tenant-runtime-password":  append([]byte(nil), credentials.TenantRuntimePassword...),
		"mall-migrator-password":   append([]byte(nil), credentials.MallMigratorPassword...),
		"mall-runtime-password":    append([]byte(nil), credentials.MallRuntimePassword...),
		"import-receipt-sha256":    []byte(importReceiptSHA256),
	}, nil
}

func newBootstrapSecret(data map[string][]byte) *corev1.Secret {
	immutable := true
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        bootstrapSecret,
			Namespace:   stage.Namespace,
			Labels:      infrastructureSecretLabels(bootstrapSecret),
			Annotations: infrastructureSecretAnnotations(bootstrapSecret),
		},
		Immutable: &immutable,
		Type:      corev1.SecretTypeOpaque,
		Data:      cloneData(data),
	}
}

func validateBootstrapSecret(secret *corev1.Secret, expectedData map[string][]byte) error {
	if secret == nil || secret.Namespace != stage.Namespace || secret.Name != bootstrapSecret ||
		secret.Type != corev1.SecretTypeOpaque || secret.Immutable == nil || !*secret.Immutable ||
		!reflect.DeepEqual(secret.Labels, infrastructureSecretLabels(bootstrapSecret)) ||
		!reflect.DeepEqual(secret.Annotations, infrastructureSecretAnnotations(bootstrapSecret)) ||
		len(secret.OwnerReferences) != 0 || len(secret.Finalizers) != 0 || secret.DeletionTimestamp != nil ||
		!equalData(secret.Data, expectedData) {
		return errors.New("isolated reconciliation bootstrap Secret has an incompatible exact immutable contract")
	}
	return nil
}

func exactKeys(data map[string][]byte, keys []string) bool {
	if len(data) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := data[key]; !ok {
			return false
		}
	}
	return true
}

func equalData(actual, expected map[string][]byte) bool {
	if len(actual) != len(expected) {
		return false
	}
	for key, value := range expected {
		if !bytes.Equal(actual[key], value) {
			return false
		}
	}
	return true
}

func cloneData(data map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(data))
	for key, value := range data {
		result[key] = append([]byte(nil), value...)
	}
	return result
}
