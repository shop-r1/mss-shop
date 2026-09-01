// Command stage-jobs is the trusted render, preflight, and create-only path
// for the four one-time Jobs in the isolated mss-shop-dev namespace. It never
// accepts an arbitrary manifest and never applies, patches, updates, deletes,
// or writes outside mss-shop-dev.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/shop-r1/mss-shop/services/reconciler/internal/stage"
)

type jobMode string

const (
	modeImporter   jobMode = "importer"
	modeReconciler jobMode = "reconciler"
	modeReadiness  jobMode = "readiness"
	modeVerifier   jobMode = "verifier"

	importerManifestPath   = "deploy/mss-shop-dev/legacy-import-job.yaml"
	reconcilerManifestPath = "deploy/mss-shop-dev/reconciler-job.yaml"
	readinessManifestPath  = "deploy/mss-shop-dev/legacy-readiness-job.yaml"
	verifierManifestPath   = "deploy/mss-shop-dev/legacy-verifier-job.yaml"

	zeroRevision = "0000000000000000000000000000000000000000"
	zeroDigest   = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	zeroReceipt  = "0000000000000000000000000000000000000000000000000000000000000000"

	operatorManager        = "r1shop-operator"
	operatorBindingKey     = "r1shop.io/operator-binding"
	revisionKey            = "r1shop.io/full-git-sha"
	receiptKey             = "r1shop.io/import-receipt-sha256"
	infrastructureContract = "isolated-dev-v1"
	credentialContractKey  = "r1shop.io/credential-contract"
	imageDigestKey         = "r1shop.io/image-digest"
)

var (
	fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)
	fullDigest   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	fullReceipt  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type options struct {
	mode                jobMode
	kubeconfig          string
	environment         string
	revision            string
	imageDigest         string
	importReceiptSHA256 string
	receiptFile         string
	create              bool
}

type convergeResult struct {
	created    bool
	exactRetry bool
	dryRun     bool
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("isolated create-only Job stage stopped safely", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	opts, err := parseOptions(arguments)
	if err != nil {
		return err
	}
	if err := verifyCheckoutRevision(ctx, opts.revision); err != nil {
		return err
	}
	var receiptEvidence []byte
	if opts.mode == modeVerifier {
		receiptEvidence, err = loadReceiptEvidence(ctx, opts)
		if err != nil {
			return err
		}
	}
	manifest, err := os.ReadFile(manifestPath(opts.mode))
	if err != nil {
		return errors.New("read fixed isolated Job manifest")
	}
	desired, err := renderJob(opts.mode, manifest, opts.revision, opts.imageDigest, opts.importReceiptSHA256)
	if err != nil {
		return err
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", opts.kubeconfig)
	if err != nil {
		return errors.New("load trusted create-only Job operator kubeconfig")
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return errors.New("initialize trusted create-only Job operator Kubernetes client")
	}
	result, err := converge(ctx, client, desired, opts.mode, opts.importReceiptSHA256, opts.create, receiptEvidence)
	if err != nil {
		return err
	}
	slog.Info(
		"isolated create-only Job stage completed",
		"environment", stage.Environment,
		"mode", opts.mode,
		"revision", opts.revision,
		"created", result.created,
		"exactRetry", result.exactRetry,
		"dryRun", result.dryRun,
	)
	return nil
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("mss-shop-stage-jobs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result options
	var modeText string
	flags.StringVar(&modeText, "mode", "", "fixed Job mode: readiness, importer, verifier, or reconciler")
	flags.StringVar(&result.kubeconfig, "kubeconfig", "", "absolute trusted operator kubeconfig path")
	flags.StringVar(&result.environment, "environment", "", "required isolated environment confirmation")
	flags.StringVar(&result.revision, "revision", "", "complete immutable Git revision")
	flags.StringVar(&result.imageDigest, "image-digest", "", "exact image sha256 digest from the CI receipt")
	flags.StringVar(&result.importReceiptSHA256, "import-receipt-sha256", "", "verified legacy import receipt SHA-256; verifier and reconciler only")
	flags.StringVar(&result.receiptFile, "receipt-file", "", "absolute canonical committed receipt.json path; verifier only")
	flags.BoolVar(&result.create, "create", false, "persist only the fully preflighted Job")
	if err := flags.Parse(arguments); err != nil {
		return options{}, errors.New("parse isolated create-only Job options")
	}
	result.mode = jobMode(modeText)
	if flags.NArg() != 0 || !approvedMode(result.mode) ||
		!filepath.IsAbs(result.kubeconfig) || filepath.Clean(result.kubeconfig) != result.kubeconfig ||
		result.environment != stage.Environment ||
		!validRevision(result.revision) || !validDigest(result.imageDigest) {
		return options{}, errors.New("stage-jobs requires a fixed approved mode, an absolute canonical kubeconfig, mss-shop-dev confirmation, complete nonzero Git SHA, and exact nonzero CI image digest")
	}
	if (result.mode == modeImporter || result.mode == modeReadiness) &&
		(result.importReceiptSHA256 != "" || result.receiptFile != "") {
		return options{}, errors.New("pre-import Jobs do not accept receipt evidence")
	}
	if result.mode == modeReconciler && !validReceipt(result.importReceiptSHA256) {
		return options{}, errors.New("reconciler Job requires a complete nonzero lowercase import receipt SHA-256")
	}
	if result.mode == modeReconciler && result.receiptFile != "" {
		return options{}, errors.New("reconciler Job does not mount the full import receipt")
	}
	if result.mode == modeVerifier && (!validReceipt(result.importReceiptSHA256) ||
		!filepath.IsAbs(result.receiptFile) || filepath.Clean(result.receiptFile) != result.receiptFile) {
		return options{}, errors.New("verifier Job requires a complete receipt SHA-256 and its absolute canonical committed receipt.json path")
	}
	return result, nil
}

func approvedMode(mode jobMode) bool {
	return mode == modeReadiness || mode == modeImporter || mode == modeVerifier || mode == modeReconciler
}

func manifestPath(mode jobMode) string {
	if mode == modeImporter {
		return importerManifestPath
	}
	if mode == modeReconciler {
		return reconcilerManifestPath
	}
	if mode == modeReadiness {
		return readinessManifestPath
	}
	if mode == modeVerifier {
		return verifierManifestPath
	}
	return ""
}

func validRevision(value string) bool {
	return fullRevision.MatchString(value) && value != zeroRevision
}

func validDigest(value string) bool {
	return fullDigest.MatchString(value) && value != zeroDigest
}

func validReceipt(value string) bool {
	return fullReceipt.MatchString(value) && value != zeroReceipt
}

func verifyCheckoutRevision(ctx context.Context, revision string) error {
	head, err := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return errors.New("trusted create-only Job checkout does not match the requested revision")
	}
	status, statusErr := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=normal").Output()
	return validateCheckoutRevision(revision, head, status, statusErr)
}

func validateCheckoutRevision(revision string, head, status []byte, statusErr error) error {
	if !validRevision(revision) || strings.TrimSpace(string(head)) != revision {
		return errors.New("trusted create-only Job checkout does not match the requested revision")
	}
	if statusErr != nil {
		return errors.New("inspect trusted create-only Job checkout")
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("isolated create-only Job stage requires a clean checkout")
	}
	return nil
}

func modeError(mode jobMode) error {
	return fmt.Errorf("unapproved isolated Job mode %q", mode)
}
