package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

const (
	testRevision     = "0123456789abcdef0123456789abcdef01234567"
	testDigest       = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testReceipt      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testZeroRevision = "0000000000000000000000000000000000000000"
	testZeroDigest   = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	testZeroReceipt  = "0000000000000000000000000000000000000000000000000000000000000000"

	testNamespace        = "mss-shop-dev"
	testOperator         = "r1shop-operator"
	testContract         = "isolated-dev-v1"
	testBindingKey       = "r1shop.io/operator-binding"
	testCredentialKey    = "r1shop.io/credential-contract"
	testInfrastructure   = "r1shop.io/infrastructure-contract"
	testReceiptKey       = "r1shop.io/import-receipt-sha256"
	testPostgresClaim    = "mss-shop-postgres-data"
	testPostgresWorkload = "mss-shop-postgres"
)

func TestParseOptionsAllowsOnlyTheIsolatedModeSpecificContract(t *testing.T) {
	t.Parallel()
	importerArguments := []string{
		"--mode", string(modeImporter),
		"--environment", testNamespace,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", testRevision,
		"--image-digest", testDigest,
	}
	importer, err := parseOptions(importerArguments)
	if err != nil {
		t.Fatalf("reviewed importer options rejected: %v", err)
	}
	if importer.mode != modeImporter || importer.create || importer.importReceiptSHA256 != "" {
		t.Fatalf("unexpected importer options: %+v", importer)
	}

	reconcilerArguments := append(append([]string(nil), importerArguments...),
		"--import-receipt-sha256", testReceipt,
	)
	reconcilerArguments[1] = string(modeReconciler)
	reconcilerArguments = append(reconcilerArguments, "--create")
	reconciler, err := parseOptions(reconcilerArguments)
	if err != nil {
		t.Fatalf("reviewed reconciler options rejected: %v", err)
	}
	if reconciler.mode != modeReconciler || !reconciler.create ||
		reconciler.importReceiptSHA256 != testReceipt {
		t.Fatalf("unexpected reconciler options: %+v", reconciler)
	}

	unsafe := [][]string{
		{"--mode", string(modeImporter), "--environment", "r1shop-dev", "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest},
		{"--mode", string(modeImporter), "--environment", "r1shop-prod", "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest},
		{"--mode", string(modeImporter), "--environment", testNamespace, "--kubeconfig", "relative.kubeconfig", "--revision", testRevision, "--image-digest", testDigest},
		{"--mode", string(modeImporter), "--environment", testNamespace, "--kubeconfig", "/trusted/../dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest},
		{"--mode", string(modeImporter), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testZeroRevision, "--image-digest", testDigest},
		{"--mode", string(modeImporter), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", strings.ToUpper(testRevision), "--image-digest", testDigest},
		{"--mode", string(modeImporter), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testZeroDigest},
		{"--mode", string(modeImporter), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", "latest"},
		{"--mode", string(modeImporter), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest, "--import-receipt-sha256", testReceipt},
		{"--mode", string(modeReconciler), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest},
		{"--mode", string(modeReconciler), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest, "--import-receipt-sha256", testZeroReceipt},
		{"--mode", string(modeReconciler), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest, "--import-receipt-sha256", strings.ToUpper(testReceipt)},
		{"--mode", "foreign", "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest},
		append(append([]string(nil), importerArguments...), "extra"),
	}
	for _, arguments := range unsafe {
		if _, err := parseOptions(arguments); err == nil {
			t.Fatalf("unsafe options unexpectedly accepted: %v", arguments)
		}
	}
}

func TestOptionErrorsDoNotExposeReceipt(t *testing.T) {
	t.Parallel()
	const sensitive = "receipt-value-that-must-not-be-logged"
	_, err := parseOptions([]string{
		"--mode", string(modeReconciler),
		"--environment", testNamespace,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", testRevision,
		"--image-digest", testDigest,
		"--import-receipt-sha256", sensitive,
	})
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatal("invalid receipt was accepted or disclosed")
	}
}

func TestCheckoutRevisionRequiresAnExactCleanFullSHA(t *testing.T) {
	t.Parallel()
	if err := validateCheckoutRevision(testRevision, []byte(testRevision+"\n"), nil, nil); err != nil {
		t.Fatalf("clean exact checkout rejected: %v", err)
	}
	for name, test := range map[string]struct {
		revision  string
		head      []byte
		status    []byte
		statusErr error
	}{
		"zero":         {revision: testZeroRevision, head: []byte(testZeroRevision + "\n")},
		"symbolic":     {revision: "HEAD", head: []byte(testRevision + "\n")},
		"wrong-head":   {revision: testRevision, head: []byte(strings.Repeat("b", 40) + "\n")},
		"dirty":        {revision: testRevision, head: []byte(testRevision + "\n"), status: []byte(" M deploy/job.yaml\n")},
		"status-error": {revision: testRevision, head: []byte(testRevision + "\n"), statusErr: errors.New("failed")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCheckoutRevision(test.revision, test.head, test.status, test.statusErr); err == nil {
				t.Fatal("unsafe checkout unexpectedly accepted")
			}
		})
	}
}

func TestRenderJobLocksBothReviewedManifests(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		mode          jobMode
		receipt       string
		wantName      string
		wantContainer string
		wantRole      string
	}{
		{
			mode:          modeImporter,
			wantName:      "mss-shop-legacy-import-" + testRevision,
			wantContainer: "legacy-importer",
			wantRole:      "legacy-import",
		},
		{
			mode:          modeReconciler,
			receipt:       testReceipt,
			wantName:      "mss-shop-reconciler-" + testRevision,
			wantContainer: "reconciler",
			wantRole:      "reconciler",
		},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			manifest := readTestJobManifest(t, test.mode)
			job, err := renderJob(test.mode, manifest, testRevision, testDigest, test.receipt)
			if err != nil {
				t.Fatalf("render reviewed Job: %v", err)
			}
			if job.APIVersion != "batch/v1" || job.Kind != "Job" ||
				job.Namespace != testNamespace || job.Name != test.wantName ||
				job.GenerateName != "" || len(job.OwnerReferences) != 0 || len(job.Finalizers) != 0 {
				t.Fatalf("unsafe rendered Job identity: %#v", job.ObjectMeta)
			}
			if job.Annotations[testBindingKey] != testNamespace+":Job:"+test.wantName ||
				job.Annotations["r1shop.io/full-git-sha"] != testRevision {
				t.Fatalf("rendered Job lacks exact immutable binding: %v", job.Annotations)
			}
			if len(job.Spec.Template.Spec.Containers) != 1 {
				t.Fatalf("container count = %d, want 1", len(job.Spec.Template.Spec.Containers))
			}
			container := job.Spec.Template.Spec.Containers[0]
			if container.Name != test.wantContainer ||
				container.Image != testImageRepository(test.mode)+":"+testRevision+"@"+testDigest {
				t.Fatalf("unexpected rendered image: %s/%s", container.Name, container.Image)
			}
			if job.Spec.Template.Labels["r1shop.io/network-role"] != test.wantRole ||
				job.Spec.Template.Spec.AutomountServiceAccountToken == nil ||
				*job.Spec.Template.Spec.AutomountServiceAccountToken {
				t.Fatal("rendered Pod escaped its exact network/API identity")
			}
			if test.mode == modeReconciler {
				if job.Annotations[testReceiptKey] != testReceipt ||
					job.Spec.Template.Annotations[testReceiptKey] != testReceipt {
					t.Fatal("reconciler Job is not bound to the verified import receipt")
				}
			} else if job.Annotations[testReceiptKey] != "" ||
				job.Spec.Template.Annotations[testReceiptKey] != "" {
				t.Fatal("importer Job unexpectedly claims a not-yet-produced receipt")
			}
			encoded := mustMarshalTestJob(t, job)
			if bytes.Contains(encoded, []byte(testZeroRevision)) || bytes.Contains(encoded, []byte(testZeroDigest)) {
				t.Fatal("rendered Job retained a zero placeholder")
			}
		})
	}
}

func TestRenderJobRejectsPlaceholderDigestReceiptAndNamespaceDrift(t *testing.T) {
	t.Parallel()
	importerManifest := readTestJobManifest(t, modeImporter)
	reconcilerManifest := readTestJobManifest(t, modeReconciler)
	unsafe := []struct {
		name     string
		mode     jobMode
		manifest []byte
		revision string
		digest   string
		receipt  string
	}{
		{name: "zero-revision", mode: modeImporter, manifest: importerManifest, revision: testZeroRevision, digest: testDigest},
		{name: "zero-digest", mode: modeImporter, manifest: importerManifest, revision: testRevision, digest: testZeroDigest},
		{name: "tag-not-digest", mode: modeImporter, manifest: importerManifest, revision: testRevision, digest: "latest"},
		{name: "importer-receipt", mode: modeImporter, manifest: importerManifest, revision: testRevision, digest: testDigest, receipt: testReceipt},
		{name: "missing-reconciler-receipt", mode: modeReconciler, manifest: reconcilerManifest, revision: testRevision, digest: testDigest},
		{name: "zero-reconciler-receipt", mode: modeReconciler, manifest: reconcilerManifest, revision: testRevision, digest: testDigest, receipt: testZeroReceipt},
		{name: "pre-rendered-template", mode: modeImporter, manifest: bytes.ReplaceAll(importerManifest, []byte(testZeroRevision), []byte(testRevision)), revision: testRevision, digest: testDigest},
		{name: "old-namespace", mode: modeReconciler, manifest: bytes.Replace(reconcilerManifest, []byte("  namespace: mss-shop-dev"), []byte("  namespace: r1shop-dev"), 1), revision: testRevision, digest: testDigest, receipt: testReceipt},
		{name: "second-document", mode: modeImporter, manifest: append(append([]byte(nil), importerManifest...), []byte("\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: foreign\n")...), revision: testRevision, digest: testDigest},
	}
	for _, test := range unsafe {
		t.Run(test.name, func(t *testing.T) {
			if _, err := renderJob(test.mode, test.manifest, test.revision, test.digest, test.receipt); err == nil {
				t.Fatal("unsafe Job template unexpectedly accepted")
			}
		})
	}
}

func TestConvergeDryRunNeverPersistsEitherJob(t *testing.T) {
	for _, test := range testModeCases() {
		t.Run(string(test.mode), func(t *testing.T) {
			desired := renderTestJob(t, test.mode, test.receipt)
			harness := newJobHarness(t, test.mode, test.receipt)
			result, err := converge(context.Background(), harness.client, desired, test.mode, test.receipt, false)
			if err != nil {
				t.Fatalf("dry-run reviewed Job: %v", err)
			}
			if result.created || result.exactRetry || !result.dryRun {
				t.Fatalf("unexpected dry-run result: %+v", result)
			}
			if harness.dryRuns != 1 || harness.realCreates != 0 {
				t.Fatalf("dry-run/create counts = %d/%d, want 1/0", harness.dryRuns, harness.realCreates)
			}
			if _, err := harness.client.BatchV1().Jobs(testNamespace).Get(
				context.Background(), desired.Name, metav1.GetOptions{},
			); !apierrors.IsNotFound(err) {
				t.Fatalf("dry-run Job persisted: %v", err)
			}
			assertJobMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestEquivalentJobRequiresExactKubernetesServerDefaults(t *testing.T) {
	desired := renderTestJob(t, modeReadiness, "")
	firstFieldRef := func(job *batchv1.Job) *corev1.ObjectFieldSelector {
		t.Helper()
		for containerIndex := range job.Spec.Template.Spec.Containers {
			for envIndex := range job.Spec.Template.Spec.Containers[containerIndex].Env {
				valueFrom := job.Spec.Template.Spec.Containers[containerIndex].Env[envIndex].ValueFrom
				if valueFrom != nil && valueFrom.FieldRef != nil {
					return valueFrom.FieldRef
				}
			}
		}
		t.Fatal("readiness fixture has no downward API fieldRef")
		return nil
	}
	if err := validateEquivalentJob(testPersistedJob(desired), desired, true); err != nil {
		t.Fatalf("exact Kubernetes server defaults rejected: %v", err)
	}

	tests := map[string]func(*batchv1.Job){
		"missing-completion-mode": func(job *batchv1.Job) {
			job.Spec.CompletionMode = nil
		},
		"indexed-completion-mode": func(job *batchv1.Job) {
			value := batchv1.IndexedCompletion
			job.Spec.CompletionMode = &value
		},
		"missing-suspend": func(job *batchv1.Job) {
			job.Spec.Suspend = nil
		},
		"suspended": func(job *batchv1.Job) {
			value := true
			job.Spec.Suspend = &value
		},
		"missing-pod-replacement-policy": func(job *batchv1.Job) {
			job.Spec.PodReplacementPolicy = nil
		},
		"failed-only-pod-replacement-policy": func(job *batchv1.Job) {
			value := batchv1.Failed
			job.Spec.PodReplacementPolicy = &value
		},
		"non-cluster-first-dns": func(job *batchv1.Job) {
			job.Spec.Template.Spec.DNSPolicy = corev1.DNSDefault
		},
		"missing-scheduler": func(job *batchv1.Job) {
			job.Spec.Template.Spec.SchedulerName = ""
		},
		"foreign-scheduler": func(job *batchv1.Job) {
			job.Spec.Template.Spec.SchedulerName = "foreign-scheduler"
		},
		"missing-downward-api-version": func(job *batchv1.Job) {
			firstFieldRef(job).APIVersion = ""
		},
		"foreign-downward-api-version": func(job *batchv1.Job) {
			firstFieldRef(job).APIVersion = "v2"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			observed := testPersistedJob(desired)
			mutate(observed)
			if err := validateEquivalentJob(observed, desired, true); err == nil {
				t.Fatal("unreviewed Kubernetes server default was accepted")
			}
		})
	}
}

func TestReconcilerRequiresExplicitTerminationMessageDefaults(t *testing.T) {
	desired := renderTestJob(t, modeReconciler, testReceipt)
	container := &desired.Spec.Template.Spec.Containers[0]
	if container.TerminationMessagePath != "/dev/termination-log" ||
		container.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
		t.Fatal("reconciler manifest does not pin the reviewed termination message defaults")
	}
	for name, mutate := range map[string]func(*corev1.Container){
		"missing-path": func(value *corev1.Container) { value.TerminationMessagePath = "" },
		"foreign-path": func(value *corev1.Container) { value.TerminationMessagePath = "/tmp/foreign" },
		"missing-policy": func(value *corev1.Container) {
			value.TerminationMessagePolicy = ""
		},
		"fallback-to-logs": func(value *corev1.Container) {
			value.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := desired.DeepCopy()
			mutate(&candidate.Spec.Template.Spec.Containers[0])
			if err := validateDesiredJob(candidate, modeReconciler, testRevision, testDigest, testReceipt); err == nil {
				t.Fatal("reconciler accepted an unreviewed termination message setting")
			}
		})
	}
}

func TestEquivalentJobAllowsOnlyBoundReviewedRevisionHistory(t *testing.T) {
	desired := renderTestJob(t, modeVerifier, testReceipt)
	start := time.Date(2026, time.September, 1, 10, 32, 49, 0, time.UTC)
	newObserved := func() *batchv1.Job {
		job := testPersistedJob(desired)
		job.CreationTimestamp = metav1.NewTime(start)
		return job
	}
	completed := func(job *batchv1.Job) {
		completion := metav1.NewTime(start.Add(15 * time.Second))
		job.Status.Succeeded = 1
		job.Status.CompletionTime = &completion
		job.Status.Conditions = []batchv1.JobCondition{
			{Type: batchv1.JobConditionType("SuccessCriteriaMet"), Status: corev1.ConditionTrue},
			{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
		}
	}
	failed := func(job *batchv1.Job) {
		job.Status.Failed = 1
		job.Status.Conditions = []batchv1.JobCondition{{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Reason:  "BackoffLimitExceeded",
			Message: "Job has reached the specified backoff limit",
		}}
	}
	bound := func(job *batchv1.Job, value string) string {
		return strings.ReplaceAll(value, "JOB_UID", string(job.UID))
	}
	runningRevision := func(job *batchv1.Job) string {
		return bound(job, `{"1":{"status":"running","desire":1,"uid":"JOB_UID","start-time":"2026-09-01T10:32:49Z","completion-time":"0001-01-01T00:00:00Z"}}`)
	}
	completedRevision := func(job *batchv1.Job) string {
		return bound(job, `{"1":{"status":"completed","succeed":1,"desire":1,"uid":"JOB_UID","start-time":"2026-09-01T10:32:49Z","completion-time":"2026-09-01T10:33:04Z"}}`)
	}
	failedRevision := func(job *batchv1.Job) string {
		return bound(job, `{"1":{"status":"failed","reasons":["BackoffLimitExceeded"],"messages":["Job has reached the specified backoff limit"],"desire":1,"failed":1,"uid":"JOB_UID","start-time":"2026-09-01T10:32:49Z","completion-time":"0001-01-01T00:00:00Z"}}`)
	}
	runningAfterFailure := func(job *batchv1.Job) {
		job.Status.Failed = 1
	}
	completedAfterFailure := func(job *batchv1.Job) {
		completed(job)
		job.Status.Failed = 1
	}
	failedAfterRetry := func(job *batchv1.Job) {
		failed(job)
		job.Status.Failed = 2
	}
	failedAtDeadline := func(job *batchv1.Job) {
		failed(job)
		job.Status.Failed = 0
		job.Status.Conditions[0].Reason = "DeadlineExceeded"
		job.Status.Conditions[0].Message = "Job was active longer than specified deadline"
	}

	valid := map[string]struct {
		mutate   func(*batchv1.Job)
		revision func(*batchv1.Job) string
		tracking bool
	}{
		"running":                     {revision: runningRevision},
		"completed":                   {mutate: completed, revision: completedRevision},
		"failed":                      {mutate: failed, revision: failedRevision},
		"running-with-empty-tracking": {revision: runningRevision, tracking: true},
		"running-after-failed-attempt": {
			mutate: runningAfterFailure,
			revision: func(job *batchv1.Job) string {
				return strings.Replace(runningRevision(job), `"desire":1`, `"desire":1,"failed":1`, 1)
			},
		},
		"completed-after-failed-attempt": {
			mutate: completedAfterFailure,
			revision: func(job *batchv1.Job) string {
				return strings.Replace(completedRevision(job), `"desire":1`, `"desire":1,"failed":1`, 1)
			},
		},
		"failed-after-retry": {
			mutate: failedAfterRetry,
			revision: func(job *batchv1.Job) string {
				return strings.Replace(failedRevision(job), `"failed":1`, `"failed":2`, 1)
			},
		},
		"failed-at-deadline-without-pod-failure": {
			mutate: failedAtDeadline,
			revision: func(job *batchv1.Job) string {
				value := strings.Replace(failedRevision(job), "BackoffLimitExceeded", "DeadlineExceeded", 1)
				value = strings.Replace(value, "Job has reached the specified backoff limit", "Job was active longer than specified deadline", 1)
				return strings.Replace(value, `,"failed":1`, "", 1)
			},
		},
	}
	for name, test := range valid {
		t.Run(name, func(t *testing.T) {
			observed := newObserved()
			if test.mutate != nil {
				test.mutate(observed)
			}
			revisions := test.revision(observed)
			generated, err := expectedJobRevisionHistory(observed)
			if err != nil {
				t.Fatalf("build reviewed controller revision history: %v", err)
			}
			if generated != revisions {
				t.Fatalf("controller revision history = %q, want %q", generated, revisions)
			}
			observed.Annotations["revisions"] = revisions
			if test.tracking {
				observed.Annotations["batch.kubernetes.io/job-tracking"] = ""
			}
			if err := validateEquivalentJob(observed, desired, true); err != nil {
				t.Fatalf("reviewed controller revision history rejected: %v", err)
			}
		})
	}

	invalid := map[string]func(*batchv1.Job){
		"malformed": func(job *batchv1.Job) {
			job.Annotations["revisions"] = `{`
		},
		"active-field": func(job *batchv1.Job) {
			job.Annotations["revisions"] = bound(job, `{"1":{"status":"running","active":1,"desire":1,"uid":"JOB_UID","start-time":"2026-09-01T10:32:49Z","completion-time":"0001-01-01T00:00:00Z"}}`)
		},
		"foreign-uid": func(job *batchv1.Job) {
			job.Annotations["revisions"] = `{"1":{"status":"running","desire":1,"uid":"foreign","start-time":"2026-09-01T10:32:49Z","completion-time":"0001-01-01T00:00:00Z"}}`
		},
		"unknown-field": func(job *batchv1.Job) {
			job.Annotations["revisions"] = strings.TrimSuffix(runningRevision(job), `}}`) + `,"foreign":true}}`
		},
		"unknown-status": func(job *batchv1.Job) {
			job.Annotations["revisions"] = bound(job, `{"1":{"status":"paused","desire":1,"uid":"JOB_UID","start-time":"2026-09-01T10:32:49Z","completion-time":"0001-01-01T00:00:00Z"}}`)
		},
		"wrong-success-count": func(job *batchv1.Job) {
			completed(job)
			job.Annotations["revisions"] = strings.Replace(completedRevision(job), `"succeed":1`, `"succeed":2`, 1)
		},
		"foreign-revision": func(job *batchv1.Job) {
			job.Annotations["revisions"] = strings.Replace(runningRevision(job), `"1"`, `"2"`, 1)
		},
		"extra-revision": func(job *batchv1.Job) {
			revision := strings.TrimSuffix(runningRevision(job), `}`)
			job.Annotations["revisions"] = revision + `,"2":` + strings.TrimPrefix(revision, `{"1":`) + `}`
		},
		"duplicate-revision-key": func(job *batchv1.Job) {
			entry := strings.TrimPrefix(strings.TrimSuffix(runningRevision(job), `}`), `{"1":`)
			job.Annotations["revisions"] = `{"1":` + entry + `,"1":` + entry + `}`
		},
		"duplicate-status-field": func(job *batchv1.Job) {
			job.Annotations["revisions"] = strings.Replace(runningRevision(job), `"status":"running"`, `"status":"running","status":"running"`, 1)
		},
		"noncanonical-whitespace": func(job *batchv1.Job) {
			job.Annotations["revisions"] = " " + runningRevision(job)
		},
		"missing-zero-completion": func(job *batchv1.Job) {
			job.Annotations["revisions"] = strings.Replace(runningRevision(job), `,"completion-time":"0001-01-01T00:00:00Z"`, "", 1)
		},
		"wrong-start-time": func(job *batchv1.Job) {
			job.Annotations["revisions"] = strings.Replace(runningRevision(job), "10:32:49Z", "10:32:50Z", 1)
		},
		"wrong-completion-time": func(job *batchv1.Job) {
			completed(job)
			job.Annotations["revisions"] = strings.Replace(completedRevision(job), "10:33:04Z", "10:33:05Z", 1)
		},
		"stale-running-status": func(job *batchv1.Job) {
			job.Annotations["revisions"] = runningRevision(job)
			completed(job)
		},
		"failed-reason-mismatch": func(job *batchv1.Job) {
			failed(job)
			job.Annotations["revisions"] = strings.Replace(failedRevision(job), "BackoffLimitExceeded", "ForeignReason", 1)
		},
		"failed-message-mismatch": func(job *batchv1.Job) {
			failed(job)
			job.Annotations["revisions"] = strings.Replace(failedRevision(job), "Job has reached the specified backoff limit", "foreign message", 1)
		},
		"contradictory-terminal-conditions": func(job *batchv1.Job) {
			completed(job)
			job.Status.Failed = 1
			job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{
				Type:    batchv1.JobFailed,
				Status:  corev1.ConditionTrue,
				Reason:  "BackoffLimitExceeded",
				Message: "Job has reached the specified backoff limit",
			})
			job.Annotations["revisions"] = completedRevision(job)
		},
		"nonempty-tracking": func(job *batchv1.Job) {
			job.Annotations["batch.kubernetes.io/job-tracking"] = "foreign"
		},
		"unknown-annotation": func(job *batchv1.Job) {
			job.Annotations["foreign.example/annotation"] = "value"
		},
		"negative-succeeded-count": func(job *batchv1.Job) {
			job.Status.Succeeded = -1
			job.Annotations["revisions"] = runningRevision(job)
		},
		"negative-failed-count": func(job *batchv1.Job) {
			job.Status.Failed = -1
			job.Annotations["revisions"] = runningRevision(job)
		},
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			observed := newObserved()
			mutate(observed)
			if err := validateEquivalentJob(observed, desired, true); err == nil {
				t.Fatal("unreviewed controller revision history was accepted")
			}
		})
	}
}

func TestConvergeNamespaceGateIsTheFirstAndOnlyActionOnOwnershipOrLifecycleFailure(t *testing.T) {
	desired := renderTestJob(t, modeImporter, "")
	for _, test := range []struct {
		name   string
		mutate func([]runtime.Object) []runtime.Object
	}{
		{
			name: "missing",
			mutate: func(objects []runtime.Object) []runtime.Object {
				result := make([]runtime.Object, 0, len(objects)-1)
				for _, object := range objects {
					if _, ok := object.(*corev1.Namespace); !ok {
						result = append(result, object)
					}
				}
				return result
			},
		},
		{
			name: "foreign-binding",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestNamespace(t, objects).Annotations[testBindingKey] = "foreign"
				return objects
			},
		},
		{
			name: "production-label",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestNamespace(t, objects).Labels["r1shop.io/environment"] = "prod"
				return objects
			},
		},
		{
			name: "foreign-owner",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestNamespace(t, objects).OwnerReferences = []metav1.OwnerReference{{Name: "foreign"}}
				return objects
			},
		},
		{
			name: "deleting",
			mutate: func(objects []runtime.Object) []runtime.Object {
				now := metav1.Now()
				findTestNamespace(t, objects).DeletionTimestamp = &now
				return objects
			},
		},
		{
			name: "terminating",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestNamespace(t, objects).Status.Phase = corev1.NamespaceTerminating
				return objects
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := test.mutate(testJobPrerequisites(modeImporter, ""))
			harness := newJobHarnessWithObjects(t, objects)
			if _, err := converge(context.Background(), harness.client, desired, modeImporter, "", true); err == nil {
				t.Fatal("unsafe Namespace unexpectedly passed the first gate")
			}
			actions := harness.client.Actions()
			if len(actions) != 1 {
				t.Fatalf("Namespace failure actions = %#v, want exactly one target Namespace GET", actions)
			}
			get, ok := actions[0].(ktesting.GetAction)
			if !ok || actions[0].GetVerb() != "get" ||
				actions[0].GetResource().Group != "" || actions[0].GetResource().Resource != "namespaces" ||
				actions[0].GetNamespace() != "" || get.GetName() != testNamespace {
				t.Fatalf("first Namespace gate action = %#v", actions[0])
			}
			if harness.dryRuns != 0 || harness.realCreates != 0 {
				t.Fatalf("Namespace failure reached Job create: dry=%d create=%d", harness.dryRuns, harness.realCreates)
			}
		})
	}
}

func TestConvergeCreatesOnlyTheReviewedJob(t *testing.T) {
	for _, test := range testModeCases() {
		t.Run(string(test.mode), func(t *testing.T) {
			desired := renderTestJob(t, test.mode, test.receipt)
			harness := newJobHarness(t, test.mode, test.receipt)
			result, err := converge(context.Background(), harness.client, desired, test.mode, test.receipt, true)
			if err != nil {
				t.Fatalf("create reviewed Job: %v", err)
			}
			if !result.created || result.exactRetry || !result.dryRun {
				t.Fatalf("unexpected create result: %+v", result)
			}
			if harness.dryRuns != 1 || harness.realCreates != 1 {
				t.Fatalf("dry-run/create counts = %d/%d, want 1/1", harness.dryRuns, harness.realCreates)
			}
			stored, err := harness.client.BatchV1().Jobs(testNamespace).Get(
				context.Background(), desired.Name, metav1.GetOptions{},
			)
			if err != nil || stored.Spec.Template.Spec.Containers[0].Image != desired.Spec.Template.Spec.Containers[0].Image {
				t.Fatalf("reviewed Job was not persisted exactly: %v", err)
			}
			assertJobMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestConvergeAcceptsExactControllerRevisionOnPostCreateGet(t *testing.T) {
	receiptBytes, receiptSHA := validStageReceipt(t)
	desired := renderTestJob(t, modeVerifier, receiptSHA)
	harness := newVerificationHarness(t, verificationPrerequisites(t, modeVerifier))
	injected := false
	harness.client.PrependReactor("get", "jobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		get, ok := action.(ktesting.GetAction)
		if !ok || get.GetName() != desired.Name || harness.jobCreates == 0 {
			return false, nil, nil
		}
		object, err := harness.client.Tracker().Get(
			batchv1.SchemeGroupVersion.WithResource("jobs"), testNamespace, desired.Name,
		)
		if err != nil {
			return true, nil, err
		}
		observed := object.(*batchv1.Job).DeepCopy()
		revisions, err := expectedJobRevisionHistory(observed)
		if err != nil {
			t.Fatalf("build post-create controller revision: %v", err)
		}
		observed.Annotations["revisions"] = revisions
		injected = true
		return true, observed, nil
	})

	result, err := converge(
		context.Background(), harness.client, desired, modeVerifier, receiptSHA, true, receiptBytes,
	)
	if err != nil {
		t.Fatalf("post-create controller revision rejected: %v", err)
	}
	if !injected || !result.created || result.exactRetry || !result.dryRun ||
		harness.configMapDryRuns != 1 || harness.configMapCreates != 1 ||
		harness.jobDryRuns != 1 || harness.jobCreates != 1 {
		t.Fatalf("unexpected post-create result: injected=%v result=%+v harness=%+v",
			injected, result, harness)
	}
	assertVerificationMutationBoundary(t, harness.client.Actions())
}

func TestConvergeExactExistingJobIsAReadOnlyRetry(t *testing.T) {
	for _, test := range testModeCases() {
		t.Run(string(test.mode), func(t *testing.T) {
			desired := renderTestJob(t, test.mode, test.receipt)
			existing := testPersistedJob(desired)
			harness := newJobHarness(t, test.mode, test.receipt, existing)
			result, err := converge(context.Background(), harness.client, desired, test.mode, test.receipt, true)
			if err != nil {
				t.Fatalf("exact retry rejected: %v", err)
			}
			if result.created || !result.exactRetry || result.dryRun ||
				harness.dryRuns != 0 || harness.realCreates != 0 {
				t.Fatalf("unexpected exact-retry result: %+v dry=%d create=%d", result, harness.dryRuns, harness.realCreates)
			}
			assertJobMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestConvergeRejectsForeignGlobalJobCollisionBeforeCreate(t *testing.T) {
	desired := renderTestJob(t, modeImporter, "")
	foreign := testPersistedJob(desired)
	foreign.Namespace = "foreign-dev"
	foreign.ResourceVersion = "foreign-1"
	harness := newJobHarness(t, modeImporter, "", foreign)
	result, err := converge(context.Background(), harness.client, desired, modeImporter, "", true)
	if err == nil {
		t.Fatal("foreign global Job-name collision unexpectedly accepted")
	}
	if result.created || result.exactRetry || harness.dryRuns != 0 || harness.realCreates != 0 {
		t.Fatalf("collision reached create: result=%+v dry=%d create=%d", result, harness.dryRuns, harness.realCreates)
	}
	assertJobMutationBoundary(t, harness.client.Actions())
}

func TestConvergeAlreadyExistsRaceAcceptsOnlyExactJob(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*batchv1.Job)
		wantErr bool
	}{
		{name: "exact"},
		{
			name: "different-image",
			mutate: func(job *batchv1.Job) {
				job.Spec.Template.Spec.Containers[0].Image = "ghcr.io/foreign/image:tag"
			},
			wantErr: true,
		},
		{
			name: "foreign-binding",
			mutate: func(job *batchv1.Job) {
				job.Annotations[testBindingKey] = "foreign"
			},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			desired := renderTestJob(t, modeReconciler, testReceipt)
			harness := newJobHarness(t, modeReconciler, testReceipt)
			harness.raceMutate = test.mutate
			harness.race = true
			_, err := converge(context.Background(), harness.client, desired, modeReconciler, testReceipt, true)
			if test.wantErr && err == nil {
				t.Fatal("non-equivalent concurrent Job unexpectedly accepted")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("exact concurrent Job rejected: %v", err)
			}
			if harness.dryRuns != 1 || harness.realCreates != 1 {
				t.Fatalf("race dry-run/create counts = %d/%d, want 1/1", harness.dryRuns, harness.realCreates)
			}
			assertJobMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestImporterRequiresReadyOwnedPostgresAndBoundPVC(t *testing.T) {
	desired := renderTestJob(t, modeImporter, "")
	for _, test := range []struct {
		name   string
		mutate func([]runtime.Object) []runtime.Object
	}{
		{
			name: "missing-statefulset",
			mutate: func(objects []runtime.Object) []runtime.Object {
				return testWithoutObject(objects, "StatefulSet", testPostgresWorkload)
			},
		},
		{
			name: "not-ready",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestStatefulSet(t, objects).Status.ReadyReplicas = 0
				return objects
			},
		},
		{
			name: "stale-generation",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestStatefulSet(t, objects).Status.ObservedGeneration = 0
				return objects
			},
		},
		{
			name: "revision-rollout-incomplete",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestStatefulSet(t, objects).Status.UpdateRevision = "different"
				return objects
			},
		},
		{
			name: "pending-pvc",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestPVC(t, objects).Status.Phase = corev1.ClaimPending
				return objects
			},
		},
		{
			name: "foreign-pvc-binding",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestPVC(t, objects).Annotations[testBindingKey] = "foreign"
				return objects
			},
		},
		{
			name: "mutable-source-secret",
			mutate: func(objects []runtime.Object) []runtime.Object {
				findTestSecret(t, objects, "mss-shop-legacy-source-auth").Immutable = nil
				return objects
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := test.mutate(testJobPrerequisites(modeImporter, ""))
			harness := newJobHarnessWithObjects(t, objects)
			if _, err := converge(context.Background(), harness.client, desired, modeImporter, "", true); err == nil {
				t.Fatal("unsafe importer prerequisite unexpectedly accepted")
			}
			if harness.realCreates != 0 {
				t.Fatal("failed prerequisite persisted a Job")
			}
			assertJobMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestReconcilerRequiresExactImmutableReceiptBootstrap(t *testing.T) {
	desired := renderTestJob(t, modeReconciler, testReceipt)
	for _, test := range []struct {
		name   string
		mutate func(*corev1.Secret)
	}{
		{
			name: "other-receipt",
			mutate: func(secret *corev1.Secret) {
				secret.Data["import-receipt-sha256"] = []byte(strings.Repeat("b", 64))
			},
		},
		{
			name: "mutable",
			mutate: func(secret *corev1.Secret) {
				secret.Immutable = nil
			},
		},
		{
			name: "foreign-contract",
			mutate: func(secret *corev1.Secret) {
				secret.Annotations[testCredentialKey] = "foreign"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := testJobPrerequisites(modeReconciler, testReceipt)
			test.mutate(findTestSecret(t, objects, "mss-shop-reconciler-bootstrap"))
			harness := newJobHarnessWithObjects(t, objects)
			if _, err := converge(context.Background(), harness.client, desired, modeReconciler, testReceipt, true); err == nil {
				t.Fatal("unsafe reconciliation bootstrap unexpectedly accepted")
			}
			if harness.realCreates != 0 {
				t.Fatal("invalid reconciliation bootstrap persisted a Job")
			}
			assertJobMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestConvergeRejectsAnyOldOrProductionTargetWithoutWriting(t *testing.T) {
	for _, namespace := range []string{"r1shop-dev", "r1shop-prod"} {
		t.Run(namespace, func(t *testing.T) {
			desired := renderTestJob(t, modeImporter, "")
			desired.Namespace = namespace
			harness := newJobHarness(t, modeImporter, "")
			if _, err := converge(context.Background(), harness.client, desired, modeImporter, "", true); err == nil {
				t.Fatal("old or production Job target unexpectedly accepted")
			}
			if harness.realCreates != 0 {
				t.Fatal("old or production target reached persistent create")
			}
			assertJobMutationBoundary(t, harness.client.Actions())
		})
	}
}

type testModeCase struct {
	mode    jobMode
	receipt string
}

func testModeCases() []testModeCase {
	return []testModeCase{{mode: modeImporter}, {mode: modeReconciler, receipt: testReceipt}}
}

func readTestJobManifest(t *testing.T, mode jobMode) []byte {
	t.Helper()
	path := ""
	switch mode {
	case modeImporter:
		path = "../../../../deploy/mss-shop-dev/legacy-import-job.yaml"
	case modeReconciler:
		path = "../../../../deploy/mss-shop-dev/reconciler-job.yaml"
	case modeReadiness:
		path = "../../../../deploy/mss-shop-dev/legacy-readiness-job.yaml"
	case modeVerifier:
		path = "../../../../deploy/mss-shop-dev/legacy-verifier-job.yaml"
	case modeProjection:
		path = "../../../../deploy/mss-shop-dev/member-levels-projection-verifier-job.yaml"
	default:
		t.Fatalf("unapproved mode %q", mode)
	}
	manifest, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s manifest: %v", mode, err)
	}
	return manifest
}

func renderTestJob(t *testing.T, mode jobMode, receipt string) *batchv1.Job {
	t.Helper()
	job, err := renderJob(mode, readTestJobManifest(t, mode), testRevision, testDigest, receipt)
	if err != nil {
		t.Fatalf("render %s Job: %v", mode, err)
	}
	return job
}

func testImageRepository(mode jobMode) string {
	if mode == modeReconciler || mode == modeProjection {
		return "ghcr.io/shop-r1/mss-shop-reconciler"
	}
	return "ghcr.io/shop-r1/mss-shop-legacy-importer"
}

func mustMarshalTestJob(t *testing.T, job *batchv1.Job) []byte {
	t.Helper()
	encoded, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("encode rendered Job: %v", err)
	}
	return encoded
}

type jobHarness struct {
	t           *testing.T
	client      *fake.Clientset
	dryRuns     int
	realCreates int
	race        bool
	raceMutate  func(*batchv1.Job)
}

func newJobHarness(t *testing.T, mode jobMode, receipt string, extra ...runtime.Object) *jobHarness {
	t.Helper()
	objects := testJobPrerequisites(mode, receipt)
	objects = append(objects, extra...)
	return newJobHarnessWithObjects(t, objects)
}

func newJobHarnessWithObjects(t *testing.T, objects []runtime.Object) *jobHarness {
	t.Helper()
	client := fake.NewSimpleClientset(objects...)
	harness := &jobHarness{t: t, client: client}
	client.PrependReactor("create", "jobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(interface {
			ktesting.CreateAction
			GetCreateOptions() metav1.CreateOptions
		})
		if !ok {
			t.Fatalf("create action has unexpected type %T", action)
		}
		job, ok := create.GetObject().(*batchv1.Job)
		if !ok {
			t.Fatalf("created object has type %T", create.GetObject())
		}
		if len(create.GetCreateOptions().DryRun) != 0 {
			harness.dryRuns++
			return true, testServerJob(job, false), nil
		}
		harness.realCreates++
		stored := testServerJob(job, true)
		if !harness.race {
			if err := client.Tracker().Create(
				batchv1.SchemeGroupVersion.WithResource("jobs"), stored, job.Namespace,
			); err != nil {
				t.Fatalf("persist created Job: %v", err)
			}
			return true, stored.DeepCopy(), nil
		}
		concurrent := stored
		if harness.raceMutate != nil {
			harness.raceMutate(concurrent)
		}
		if err := client.Tracker().Create(
			batchv1.SchemeGroupVersion.WithResource("jobs"), concurrent, job.Namespace,
		); err != nil {
			t.Fatalf("persist concurrent Job: %v", err)
		}
		return true, nil, apierrors.NewAlreadyExists(
			schema.GroupResource{Group: "batch", Resource: "jobs"}, job.Name,
		)
	})
	return harness
}

func testJobPrerequisites(mode jobMode, receipt string) []runtime.Object {
	objects := []runtime.Object{
		testTargetNamespaceFixture(),
		testCredentialSecret("mss-shop-ghcr-pull", corev1.SecretTypeDockerConfigJson, map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"ghcr.io":{"auth":"opaque"}}}`),
		}),
		testCredentialSecret("mss-shop-postgres-tls", corev1.SecretTypeTLS, map[string][]byte{
			"ca.crt": []byte("test-ca"), corev1.TLSCertKey: []byte("test-cert"), corev1.TLSPrivateKeyKey: []byte("test-key"),
		}),
	}
	if mode == modeImporter {
		objects = append(objects,
			testCredentialSecret("mss-shop-postgres-auth", corev1.SecretTypeOpaque, map[string][]byte{
				"username": []byte("mss_shop_bootstrap"), "password": []byte(strings.Repeat("p", 43)), "database": []byte("mss_shop_dev"),
			}),
			testCredentialSecret("mss-shop-legacy-source-auth", corev1.SecretTypeOpaque, map[string][]byte{
				"username": []byte("legacy_owner"), "password": []byte("legacy-source-password"), "database": []byte("r1shop_dev"),
			}),
			testPostgresStatefulSetFixture(),
			testPostgresPVCFixture(),
		)
	} else {
		objects = append(objects, testReconciliationBootstrapFixture(receipt))
	}
	return objects
}

func testTargetNamespaceFixture() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespace,
			UID:  types.UID("mss-shop-dev-uid"),
			Labels: map[string]string{
				"app.kubernetes.io/name":                     testNamespace,
				"app.kubernetes.io/instance":                 testNamespace,
				"app.kubernetes.io/component":                "namespace",
				"app.kubernetes.io/part-of":                  "mss-shop",
				"app.kubernetes.io/managed-by":               testOperator,
				"r1shop.io/environment":                      "dev",
				"pod-security.kubernetes.io/enforce":         "restricted",
				"pod-security.kubernetes.io/enforce-version": "v1.32",
				"pod-security.kubernetes.io/audit":           "restricted",
				"pod-security.kubernetes.io/audit-version":   "v1.32",
				"pod-security.kubernetes.io/warn":            "restricted",
				"pod-security.kubernetes.io/warn-version":    "v1.32",
				"kubernetes.io/metadata.name":                testNamespace,
			},
			Annotations: map[string]string{
				testBindingKey:     testNamespace + ":Namespace:" + testNamespace,
				testInfrastructure: testContract,
			},
		},
		Spec:   corev1.NamespaceSpec{Finalizers: []corev1.FinalizerName{corev1.FinalizerKubernetes}},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
}

func testCredentialSecret(name string, secretType corev1.SecretType, data map[string][]byte) *corev1.Secret {
	immutable := true
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: testNamespace, UID: types.UID("uid-" + name),
			Labels: map[string]string{
				"app.kubernetes.io/name":       name,
				"app.kubernetes.io/instance":   testNamespace,
				"app.kubernetes.io/component":  "credentials",
				"app.kubernetes.io/part-of":    "mss-shop",
				"app.kubernetes.io/managed-by": testOperator,
				"r1shop.io/environment":        "dev",
			},
			Annotations: map[string]string{
				testBindingKey:    testNamespace + ":Secret:" + name,
				testCredentialKey: testContract,
			},
		},
		Immutable: &immutable,
		Type:      secretType,
		Data:      data,
	}
}

func testReconciliationBootstrapFixture(receipt string) *corev1.Secret {
	return testCredentialSecret("mss-shop-reconciler-bootstrap", corev1.SecretTypeOpaque, map[string][]byte{
		"database-dsn":             []byte("postgres://mss_shop_bootstrap:password@mss-shop-postgres.mss-shop-dev.svc:5432/mss_shop_dev?sslmode=verify-full&sslrootcert=%2Fetc%2Fmss-shop%2Fpostgres-tls%2Fca.crt"),
		"import-receipt-sha256":    []byte(receipt),
		"tenant-migrator-password": []byte(strings.Repeat("t", 43)),
		"tenant-runtime-password":  []byte(strings.Repeat("u", 43)),
		"mall-migrator-password":   []byte(strings.Repeat("m", 43)),
		"mall-runtime-password":    []byte(strings.Repeat("n", 43)),
	})
}

func testPostgresStatefulSetFixture() *appsv1.StatefulSet {
	one := int32(1)
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: testPostgresWorkload, Namespace: testNamespace,
			UID: types.UID("postgres-statefulset-uid"), ResourceVersion: "20", Generation: 2,
			Labels: testInfrastructureLabels(testPostgresWorkload, "database"),
			Annotations: map[string]string{
				testBindingKey:     testNamespace + ":StatefulSet:" + testPostgresWorkload,
				testInfrastructure: testContract,
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &one,
			ServiceName: testPostgresWorkload,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name": testPostgresWorkload, "app.kubernetes.io/instance": testNamespace,
			}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "postgres", Image: "postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94"}},
				Volumes: []corev1.Volume{{
					Name: "data",
					VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: testPostgresClaim,
					}},
				}},
			}},
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 2,
			Replicas:           1, ReadyReplicas: 1, CurrentReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1,
			CurrentRevision: "postgres-revision", UpdateRevision: "postgres-revision",
		},
	}
}

func testPostgresPVCFixture() *corev1.PersistentVolumeClaim {
	storageClass := "local"
	volumeMode := corev1.PersistentVolumeFilesystem
	quantity := resource.MustParse("10Gi")
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: testPostgresClaim, Namespace: testNamespace,
			UID: types.UID("postgres-pvc-uid"), ResourceVersion: "30",
			Labels: testInfrastructureLabels(testPostgresWorkload, "database"),
			Annotations: map[string]string{
				testBindingKey:     testNamespace + ":PersistentVolumeClaim:" + testPostgresClaim,
				testInfrastructure: testContract,
				"volume.kubernetes.io/storage-provisioner": "openebs.io/local",
				"volume.kubernetes.io/selected-node":       "dev-node",
				"pv.kubernetes.io/bind-completed":          "yes",
				"pv.kubernetes.io/bound-by-controller":     "yes",
			},
			Finalizers: []string{"kubernetes.io/pvc-protection"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &storageClass,
			VolumeMode:       &volumeMode,
			VolumeName:       "pv-mss-shop-postgres-data",
			Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceStorage: quantity,
			}},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase:       corev1.ClaimBound,
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity:    corev1.ResourceList{corev1.ResourceStorage: quantity},
		},
	}
}

func testInfrastructureLabels(name, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/instance":   testNamespace,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/part-of":    "mss-shop",
		"app.kubernetes.io/managed-by": testOperator,
		"r1shop.io/environment":        "dev",
	}
}

func testPersistedJob(desired *batchv1.Job) *batchv1.Job {
	return testServerJob(desired, true)
}

func testServerJob(desired *batchv1.Job, persisted bool) *batchv1.Job {
	job := desired.DeepCopy()
	applyReviewedJobServerDefaults(job)
	for index := range job.Spec.Template.Spec.Containers {
		container := &job.Spec.Template.Spec.Containers[index]
		if container.TerminationMessagePath == "" {
			container.TerminationMessagePath = "/dev/termination-log"
		}
		if container.TerminationMessagePolicy == "" {
			container.TerminationMessagePolicy = corev1.TerminationMessageReadFile
		}
	}
	job.UID = types.UID("job-uid-" + desired.Name)
	if persisted {
		job.ResourceVersion = "100"
		job.CreationTimestamp = metav1.Now()
	}
	controllerUID := string(job.UID)
	job.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{
		"batch.kubernetes.io/controller-uid": controllerUID,
		"controller-uid":                     controllerUID,
	}}
	if job.Spec.Template.Labels == nil {
		job.Spec.Template.Labels = make(map[string]string)
	}
	job.Spec.Template.Labels["batch.kubernetes.io/controller-uid"] = controllerUID
	job.Spec.Template.Labels["batch.kubernetes.io/job-name"] = job.Name
	job.Spec.Template.Labels["controller-uid"] = controllerUID
	job.Spec.Template.Labels["job-name"] = job.Name
	return job
}

func testWithoutObject(objects []runtime.Object, kind, name string) []runtime.Object {
	result := make([]runtime.Object, 0, len(objects))
	for _, object := range objects {
		metadata, ok := object.(metav1.Object)
		if ok && object.GetObjectKind().GroupVersionKind().Kind == kind && metadata.GetName() == name {
			continue
		}
		if kind == "StatefulSet" {
			if statefulSet, ok := object.(*appsv1.StatefulSet); ok && statefulSet.Name == name {
				continue
			}
		}
		result = append(result, object)
	}
	return result
}

func findTestStatefulSet(t *testing.T, objects []runtime.Object) *appsv1.StatefulSet {
	t.Helper()
	for _, object := range objects {
		if value, ok := object.(*appsv1.StatefulSet); ok {
			return value
		}
	}
	t.Fatal("StatefulSet fixture not found")
	return nil
}

func findTestNamespace(t *testing.T, objects []runtime.Object) *corev1.Namespace {
	t.Helper()
	for _, object := range objects {
		if value, ok := object.(*corev1.Namespace); ok {
			return value
		}
	}
	t.Fatal("Namespace fixture not found")
	return nil
}

func findTestPVC(t *testing.T, objects []runtime.Object) *corev1.PersistentVolumeClaim {
	t.Helper()
	for _, object := range objects {
		if value, ok := object.(*corev1.PersistentVolumeClaim); ok {
			return value
		}
	}
	t.Fatal("PVC fixture not found")
	return nil
}

func findTestSecret(t *testing.T, objects []runtime.Object, name string) *corev1.Secret {
	t.Helper()
	for _, object := range objects {
		if value, ok := object.(*corev1.Secret); ok && value.Name == name {
			return value
		}
	}
	t.Fatalf("Secret fixture %q not found", name)
	return nil
}

func assertJobMutationBoundary(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	for _, action := range actions {
		namespace := action.GetNamespace()
		if namespace == "r1shop-dev" || namespace == "r1shop-prod" {
			switch action.GetVerb() {
			case "create", "update", "patch", "delete", "delete-collection", "deletecollection":
				t.Fatalf("write escaped into protected namespace: %s %s/%s", action.GetVerb(), namespace, action.GetResource().Resource)
			}
		}
		switch action.GetVerb() {
		case "get", "list":
			continue
		case "create":
			create, ok := action.(interface {
				ktesting.CreateAction
				GetCreateOptions() metav1.CreateOptions
			})
			if !ok {
				t.Fatalf("create action has unexpected type %T", action)
			}
			if len(create.GetCreateOptions().DryRun) != 0 {
				continue
			}
			if action.GetResource().Group != "batch" || action.GetResource().Resource != "jobs" ||
				action.GetNamespace() != testNamespace {
				t.Fatalf("persistent create escaped exact Job boundary: %#v", action)
			}
		default:
			t.Fatalf("forbidden Kubernetes verb %q observed for %s", action.GetVerb(), action.GetResource().String())
		}
	}
}
