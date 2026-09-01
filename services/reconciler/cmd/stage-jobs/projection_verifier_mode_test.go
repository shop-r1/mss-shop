package main

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestProjectionVerifierOptionsAndManifestAreExact(t *testing.T) {
	t.Parallel()
	arguments := []string{
		"--mode", string(modeProjection),
		"--environment", testNamespace,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", testRevision,
		"--image-digest", testDigest,
		"--import-receipt-sha256", testReceipt,
	}
	options, err := parseOptions(arguments)
	if err != nil || options.mode != modeProjection || options.receiptFile != "" || options.create {
		t.Fatalf("fixed projection verifier options rejected: %+v, %v", options, err)
	}
	for _, unsafe := range [][]string{
		arguments[:len(arguments)-2],
		append(append([]string(nil), arguments...), "--receipt-file", "/trusted/receipt.json"),
		{"--mode", string(modeProjection), "--environment", "r1shop-dev", "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest, "--import-receipt-sha256", testReceipt},
		{"--mode", string(modeProjection), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testZeroDigest, "--import-receipt-sha256", testReceipt},
	} {
		if _, err := parseOptions(unsafe); err == nil {
			t.Fatalf("unsafe projection verifier options accepted: %v", unsafe)
		}
	}

	job := renderTestJob(t, modeProjection, testReceipt)
	container := job.Spec.Template.Spec.Containers[0]
	encoded, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if job.Namespace != testNamespace || job.Name != "mss-shop-ml-projection-"+testRevision ||
		job.Annotations[revisionKey] != testRevision || job.Annotations[imageDigestKey] != testDigest ||
		job.Annotations[receiptKey] != testReceipt ||
		job.Spec.Template.Annotations[receiptKey] != testReceipt ||
		job.Spec.Template.Labels["r1shop.io/network-role"] != "legacy-verifier" ||
		job.Spec.Template.Spec.AutomountServiceAccountToken == nil ||
		*job.Spec.Template.Spec.AutomountServiceAccountToken ||
		container.Name != "member-levels-projection-verifier" ||
		container.Image != "ghcr.io/shop-r1/mss-shop-reconciler:"+testRevision+"@"+testDigest ||
		len(container.Command) != 1 || container.Command[0] != "/usr/local/bin/mss-shop-member-levels-projection-verifier" ||
		len(job.Spec.Template.Spec.Volumes) != 1 || len(container.VolumeMounts) != 1 {
		t.Fatalf("projection verifier escaped its exact immutable boundary: %s", encoded)
	}
	for _, forbidden := range []string{
		"mss-shop-postgres-auth", "mss-shop-reconciler-bootstrap", "mss-shop-legacy-source-auth",
		"mss-shop-legacy-import-receipt", "mss-shop-redis", "database-migrator-dsn",
		"database-runtime-password", "initial-admin-password",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("projection verifier manifest exposes forbidden material %q", forbidden)
		}
	}
	if strings.Count(string(encoded), "database-runtime-dsn") != 1 ||
		strings.Count(string(encoded), "mss-shop-mall-admin-aussibuy-runtime") != 1 ||
		strings.Count(string(encoded), "mss-shop-postgres-tls") != 1 {
		t.Fatal("projection verifier does not use only the fixed runtime DSN and PostgreSQL CA")
	}
}

func TestProjectionVerifierDryRunAndCreateAreCreateOnly(t *testing.T) {
	desired := renderTestJob(t, modeProjection, testReceipt)
	for _, create := range []bool{false, true} {
		t.Run(map[bool]string{false: "dry-run", true: "create"}[create], func(t *testing.T) {
			harness := newVerificationHarness(t, projectionPrerequisites(t))
			result, err := converge(context.Background(), harness.client, desired, modeProjection, testReceipt, create)
			if err != nil {
				t.Fatalf("stage projection verifier: %v", err)
			}
			if !result.dryRun || result.created != create || result.exactRetry || harness.jobDryRuns != 1 ||
				harness.jobCreates != map[bool]int{false: 0, true: 1}[create] ||
				harness.configMapDryRuns != 0 || harness.configMapCreates != 0 {
				t.Fatalf("unexpected create-only result: result=%+v harness=%+v", result, harness)
			}
			assertFirstActionIsNamespaceGet(t, harness.client.Actions())
			assertVerificationMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestProjectionVerifierRejectsRuntimeSecretAndReconcilerDriftBeforeCreate(t *testing.T) {
	desired := renderTestJob(t, modeProjection, testReceipt)
	tests := []struct {
		name   string
		mutate func([]runtime.Object)
	}{
		{name: "runtime-secret-missing", mutate: func(objects []runtime.Object) {
			removeRuntimeSecret(objects)
		}},
		{name: "runtime-secret-immutable", mutate: func(objects []runtime.Object) {
			immutable := true
			projectionRuntimeSecret(t, objects).Immutable = &immutable
		}},
		{name: "runtime-secret-extra-key", mutate: func(objects []runtime.Object) {
			projectionRuntimeSecret(t, objects).Data["foreign"] = []byte("value")
		}},
		{name: "runtime-dsn-wrong-role", mutate: func(objects []runtime.Object) {
			secret := projectionRuntimeSecret(t, objects)
			secret.Data["database-runtime-dsn"] = projectionRuntimeDSN("mss_shop_bootstrap", secret.Data["database-runtime-password"])
		}},
		{name: "runtime-dsn-wrong-schema", mutate: func(objects []runtime.Object) {
			secret := projectionRuntimeSecret(t, objects)
			dsn := string(secret.Data["database-runtime-dsn"])
			secret.Data["database-runtime-dsn"] = []byte(strings.Replace(dsn, "mss_m_aussibuy_core", "mss_m_aussibuy_biz", 1))
		}},
		{name: "reconciler-not-complete", mutate: func(objects []runtime.Object) {
			job := projectionReconcilerJob(t, objects)
			job.Status.Succeeded, job.Status.CompletionTime, job.Status.Conditions = 0, nil, nil
		}},
		{name: "reconciler-image-digest", mutate: func(objects []runtime.Object) {
			job := projectionReconcilerJob(t, objects)
			job.Spec.Template.Spec.Containers[0].Image = "ghcr.io/shop-r1/mss-shop-reconciler:" + testRevision + "@sha256:" + strings.Repeat("2", 64)
		}},
		{name: "reconciler-receipt", mutate: func(objects []runtime.Object) {
			job := projectionReconcilerJob(t, objects)
			job.Annotations[receiptKey] = strings.Repeat("b", 64)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			objects := projectionPrerequisites(t)
			test.mutate(objects)
			harness := newVerificationHarness(t, compactRuntimeObjects(objects))
			if _, err := converge(context.Background(), harness.client, desired, modeProjection, testReceipt, true); err == nil {
				t.Fatal("unsafe projection verification prerequisite accepted")
			}
			if harness.jobCreates != 0 || harness.configMapCreates != 0 {
				t.Fatal("unsafe prerequisite reached a persistent create")
			}
			assertVerificationMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestProjectionRuntimeSecretAcceptsOnlyActiveOrRetiredReconcilerShapes(t *testing.T) {
	t.Parallel()
	active := testProjectionRuntimeSecret()
	if err := validateMallRuntimeSecret(active); err != nil {
		t.Fatalf("active reconciler Secret rejected: %v", err)
	}
	retired := active.DeepCopy()
	delete(retired.Data, "initial-admin-password")
	retired.Annotations["r1shop.io/initial-admin-password-retired"] = "confirmed-password-rotated"
	if err := validateMallRuntimeSecret(retired); err != nil {
		t.Fatalf("retired reconciler Secret rejected: %v", err)
	}
	retired.Annotations["r1shop.io/initial-admin-password-retired"] = "unreviewed"
	if err := validateMallRuntimeSecret(retired); err == nil {
		t.Fatal("unreviewed retirement marker accepted")
	}
	unsafeDSN := append([]byte(nil), active.Data["database-runtime-dsn"]...)
	active.Data["database-runtime-dsn"] = append(unsafeDSN, []byte("&password=secret-that-must-not-appear")...)
	if err := validateMallRuntimeSecret(active); err == nil || strings.Contains(err.Error(), "secret-that-must-not-appear") {
		t.Fatal("unsafe DSN accepted or disclosed")
	}
}

func projectionPrerequisites(t *testing.T) []runtime.Object {
	t.Helper()
	objects := verificationPrerequisites(t, modeVerifier)
	objects = append(objects, testProjectionRuntimeSecret(), testSuccessfulReconcilerJob(t))
	return objects
}

func testProjectionRuntimeSecret() *corev1.Secret {
	runtimePassword := []byte(strings.Repeat("r", 43))
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mss-shop-mall-admin-aussibuy-runtime", Namespace: testNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "mss-shop-admin", "app.kubernetes.io/component": "mall-admin",
				"app.kubernetes.io/part-of": "mss-shop", "app.kubernetes.io/managed-by": "mss-shop-reconciler",
			},
			Annotations: map[string]string{
				"r1shop.io/reconciler-binding": testNamespace + ":Secret:mss-shop-mall-admin-aussibuy-runtime",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"database-runtime-password":  runtimePassword,
			"database-migrator-password": []byte(strings.Repeat("m", 43)),
			"database-runtime-dsn":       projectionRuntimeDSN("mss_m_aussibuy_runtime", runtimePassword),
			"database-migrator-dsn":      []byte("not-mounted-but-nonempty"),
			"auth-key":                   []byte(strings.Repeat("a", 48)),
			"identity-key":               []byte(strings.Repeat("i", 48)),
			"initial-admin-password":     []byte("Safe-Test-Admin-Password-123"),
			"redis-password":             []byte(strings.Repeat("x", 43)),
		},
	}
}

func projectionRuntimeDSN(role string, password []byte) []byte {
	query := url.Values{
		"search_path": []string{"mss_m_aussibuy_core"},
		"sslmode":     []string{"verify-full"},
		"sslrootcert": []string{"/etc/mss-shop/postgres-tls/ca.crt"},
	}
	return []byte((&url.URL{
		Scheme: "postgres", User: url.UserPassword(role, string(password)),
		Host: net.JoinHostPort("mss-shop-postgres.mss-shop-dev.svc", "5432"),
		Path: "/mss_shop_dev", RawQuery: query.Encode(),
	}).String())
}

func testSuccessfulReconcilerJob(t *testing.T) *batchv1.Job {
	t.Helper()
	job := testPersistedJob(renderTestJob(t, modeReconciler, testReceipt))
	completed := metav1.Now()
	job.Status.Succeeded = 1
	job.Status.CompletionTime = &completed
	job.Status.Conditions = []batchv1.JobCondition{{
		Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: completed,
	}}
	return job
}

func projectionRuntimeSecret(t *testing.T, objects []runtime.Object) *corev1.Secret {
	t.Helper()
	for _, object := range objects {
		if secret, ok := object.(*corev1.Secret); ok && secret.Name == "mss-shop-mall-admin-aussibuy-runtime" {
			return secret
		}
	}
	t.Fatal("projection runtime Secret fixture not found")
	return nil
}

func projectionReconcilerJob(t *testing.T, objects []runtime.Object) *batchv1.Job {
	t.Helper()
	for _, object := range objects {
		if job, ok := object.(*batchv1.Job); ok && job.Name == "mss-shop-reconciler-"+testRevision {
			return job
		}
	}
	t.Fatal("reconciler prerequisite Job fixture not found")
	return nil
}

func removeRuntimeSecret(objects []runtime.Object) {
	for index, object := range objects {
		if secret, ok := object.(*corev1.Secret); ok && secret.Name == "mss-shop-mall-admin-aussibuy-runtime" {
			objects[index] = nil
			return
		}
	}
}

func compactRuntimeObjects(objects []runtime.Object) []runtime.Object {
	result := make([]runtime.Object, 0, len(objects))
	for _, object := range objects {
		if object != nil {
			result = append(result, object)
		}
	}
	return result
}
