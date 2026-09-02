// Command capture-original-dev-fingerprint emits a bounded, non-secret
// Kubernetes metadata fingerprint for the immutable original development
// environment. It has no database, Secret, exec, or mutation path.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
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
)

const (
	readOnlyConfirmation = "r1shop-dev-read-only"
	zeroRevision         = "0000000000000000000000000000000000000000"
)

var fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

type options struct {
	kubeconfig  string
	environment string
	revision    string
}

type checkoutState struct {
	root     string
	revision string
}

type runDependencies struct {
	inspectCheckout func(context.Context, string) (checkoutState, error)
	newReader       func(string) (clusterReader, error)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		slog.Error("original development fingerprint stopped safely", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, output io.Writer) error {
	return runWithDependencies(ctx, arguments, output, runDependencies{
		inspectCheckout: inspectCheckout,
		newReader:       newKubernetesReader,
	})
}

func runWithDependencies(
	ctx context.Context,
	arguments []string,
	output io.Writer,
	dependencies runDependencies,
) error {
	opts, err := parseOptions(arguments)
	if err != nil {
		return err
	}
	if output == nil || dependencies.inspectCheckout == nil || dependencies.newReader == nil {
		return errors.New("original development fingerprint dependencies are invalid")
	}
	if _, err := dependencies.inspectCheckout(ctx, opts.revision); err != nil {
		return err
	}
	canonicalKubeconfig, err := validateCanonicalKubeconfig(opts.kubeconfig)
	if err != nil {
		return err
	}
	reader, err := dependencies.newReader(canonicalKubeconfig)
	if err != nil {
		return err
	}
	fingerprint, err := captureOriginalDev(ctx, reader, opts.revision)
	if err != nil {
		return err
	}
	encoded, err := encodeFingerprint(fingerprint)
	if err != nil {
		return err
	}
	if err := writeCompleteOutput(output, encoded); err != nil {
		return err
	}
	return nil
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("capture-original-dev-fingerprint", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result options
	flags.StringVar(&result.kubeconfig, "kubeconfig", "", "absolute canonical read-only kubeconfig path")
	flags.StringVar(&result.environment, "environment", "", "required original development read-only confirmation")
	flags.StringVar(&result.revision, "revision", "", "complete immutable Git revision")
	if err := flags.Parse(arguments); err != nil {
		return options{}, errors.New("parse original development fingerprint options")
	}
	if flags.NArg() != 0 || result.environment != readOnlyConfirmation ||
		!filepath.IsAbs(result.kubeconfig) || !validRevision(result.revision) {
		return options{}, errors.New("original development fingerprint requires r1shop-dev-read-only confirmation, an absolute canonical kubeconfig, and a complete nonzero lowercase Git SHA")
	}
	return result, nil
}

func validRevision(value string) bool {
	return fullRevision.MatchString(value) && value != zeroRevision
}

func inspectCheckout(ctx context.Context, revision string) (checkoutState, error) {
	rootOutput, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return checkoutState{}, errors.New("inspect original development fingerprint checkout")
	}
	root, err := filepath.Abs(strings.TrimSpace(string(rootOutput)))
	if err != nil {
		return checkoutState{}, errors.New("inspect original development fingerprint checkout")
	}
	root, err = filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return checkoutState{}, errors.New("inspect original development fingerprint checkout")
	}
	head, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return checkoutState{}, errors.New("original development fingerprint checkout does not match the requested revision")
	}
	status, statusErr := exec.CommandContext(
		ctx,
		"git", "-C", root, "status", "--porcelain", "--untracked-files=normal",
	).Output()
	if err := validateCheckoutRevision(revision, head, status, statusErr); err != nil {
		return checkoutState{}, err
	}
	return checkoutState{root: root, revision: revision}, nil
}

func validateCheckoutRevision(revision string, head, status []byte, statusErr error) error {
	if !validRevision(revision) || strings.TrimSpace(string(head)) != revision {
		return errors.New("original development fingerprint checkout does not match the requested revision")
	}
	if statusErr != nil {
		return errors.New("inspect original development fingerprint checkout")
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("original development fingerprint requires a clean checkout")
	}
	return nil
}

func validateCanonicalKubeconfig(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("original development fingerprint kubeconfig path is not canonical")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errors.New("original development fingerprint kubeconfig path is not canonical")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", errors.New("original development fingerprint kubeconfig is not a regular nonempty file")
	}
	return path, nil
}

func newKubernetesReader(kubeconfig string) (clusterReader, error) {
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, errors.New("load original development read-only kubeconfig")
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, errors.New("initialize original development read-only Kubernetes client")
	}
	return &typedClusterReader{client: client}, nil
}

func writeCompleteOutput(output io.Writer, encoded []byte) error {
	if output == nil || len(encoded) == 0 {
		return errors.New("original development fingerprint output is incomplete")
	}
	written, err := output.Write(encoded)
	if err != nil || written != len(encoded) {
		return errors.New("original development fingerprint output is incomplete")
	}
	return nil
}
