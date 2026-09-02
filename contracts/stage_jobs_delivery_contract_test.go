package contracts_test

import (
	"strings"
	"testing"
)

func TestStageJobsCommandIsFixedTypedAndCreateOnly(t *testing.T) {
	mainSource := readContractFile(t, "../services/reconciler/cmd/stage-jobs/main.go")
	renderSource := readContractFile(t, "../services/reconciler/cmd/stage-jobs/render.go")
	preflightSource := readContractFile(t, "../services/reconciler/cmd/stage-jobs/preflight.go")
	all := mainSource + renderSource + preflightSource

	for _, required := range []string{
		`modeImporter   jobMode = "importer"`,
		`modeReconciler jobMode = "reconciler"`,
		`importerManifestPath   = "deploy/mss-shop-dev/legacy-import-job.yaml"`,
		`reconcilerManifestPath = "deploy/mss-shop-dev/reconciler-job.yaml"`,
		`filepath.Clean(result.kubeconfig) != result.kubeconfig`,
		`result.environment != stage.Environment`,
		`kubernetes.Interface`,
		`Jobs(metav1.NamespaceAll).List`,
		`DryRun:       []string{metav1.DryRunAll}`,
		`Jobs(stage.Namespace).Create`,
		`validateEquivalentJob`,
		`validateImporterTarget`,
		`validateBootstrapSecret`,
	} {
		if !strings.Contains(all, required) {
			t.Fatalf("stage-jobs delivery path lacks reviewed control %q", required)
		}
	}
	for _, forbidden := range []string{
		`.Update(`,
		`.UpdateStatus(`,
		`.Patch(`,
		`.Delete(`,
		`.DeleteCollection(`,
		`dynamic.Interface`,
		`--manifest`,
	} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("stage-jobs delivery path contains forbidden mutation or arbitrary input %q", forbidden)
		}
	}
	if strings.Index(preflightSource, "validateTargetNamespace(ctx, client)") >
		strings.Index(preflightSource, "Jobs(metav1.NamespaceAll).List") {
		t.Fatal("stage-jobs must prove the exact Namespace before any cluster-wide Job inventory")
	}
}

func TestStageJobsRunbookDocumentsTheTwentyFourObjectAndJobGates(t *testing.T) {
	runbook := readContractFile(t, "../docs/runbooks/remote-development-and-dev-acceptance.md")
	for _, required := range []string{
		"exact 24 resources",
		"two non-mounting scheduling-only binder Pods",
		"deliberately two-phase",
		"stable cluster-wide PV",
		"snapshots prove the reviewed node/path ownership",
		"go run ./services/reconciler/cmd/stage-jobs",
		"--mode importer",
		"--mode reconciler",
		"--image-digest",
		"--import-receipt-sha256",
		"repeat the identical command with `--create`",
		"performs no persistent",
		"operation other than `Create Job`",
	} {
		if !strings.Contains(runbook, required) {
			t.Fatalf("isolated acceptance runbook lacks %q", required)
		}
	}
	if strings.Contains(runbook, "exact 22 resources") {
		t.Fatal("isolated acceptance runbook regressed to the obsolete infrastructure inventory")
	}
}

func TestReconcilerManifestCarriesExactReceiptPlaceholders(t *testing.T) {
	manifest := readContractFile(t, "../deploy/mss-shop-dev/reconciler-job.yaml")
	placeholder := "r1shop.io/import-receipt-sha256: \"" + strings.Repeat("0", 64) + "\""
	if strings.Count(manifest, placeholder) != 2 {
		t.Fatalf("reconciler receipt placeholder bindings = %d, want Job and Pod template", strings.Count(manifest, placeholder))
	}
	for _, required := range []string{
		"--mode reconciler",
		"--environment mss-shop-dev",
		"--import-receipt-sha256 <verified-lowercase-receipt-sha256>",
		"its only persistent operation is Job Create",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("reconciler fixed-template operator comment lacks %q", required)
		}
	}
}
