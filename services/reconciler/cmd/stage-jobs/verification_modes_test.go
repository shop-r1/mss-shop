package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

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

	"github.com/shop-r1/mss-shop/services/internal/legacyreceipt"
)

func TestVerificationModesParseAndRenderOnlyTheirFixedContracts(t *testing.T) {
	t.Parallel()
	readiness, err := parseOptions([]string{
		"--mode", string(modeReadiness),
		"--environment", testNamespace,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", testRevision,
		"--image-digest", testDigest,
	})
	if err != nil || readiness.mode != modeReadiness || readiness.receiptFile != "" {
		t.Fatalf("fixed readiness options rejected: %+v %v", readiness, err)
	}
	receiptPath := "/trusted/repository/docs/evidence/legacy-import/" + testReceipt + "/receipt.json"
	verifier, err := parseOptions([]string{
		"--mode", string(modeVerifier),
		"--environment", testNamespace,
		"--kubeconfig", "/trusted/dev.kubeconfig",
		"--revision", testRevision,
		"--image-digest", testDigest,
		"--import-receipt-sha256", testReceipt,
		"--receipt-file", receiptPath,
	})
	if err != nil || verifier.mode != modeVerifier || verifier.receiptFile != receiptPath {
		t.Fatalf("fixed verifier options rejected: %+v %v", verifier, err)
	}
	for _, unsafe := range [][]string{
		{"--mode", string(modeReadiness), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest, "--import-receipt-sha256", testReceipt},
		{"--mode", string(modeVerifier), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest, "--import-receipt-sha256", testReceipt, "--receipt-file", "receipt.json"},
		{"--mode", string(modeVerifier), "--environment", testNamespace, "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest, "--import-receipt-sha256", testZeroReceipt, "--receipt-file", receiptPath},
		{"--mode", string(modeVerifier), "--environment", "r1shop-dev", "--kubeconfig", "/trusted/dev.kubeconfig", "--revision", testRevision, "--image-digest", testDigest, "--import-receipt-sha256", testReceipt, "--receipt-file", receiptPath},
	} {
		if _, err := parseOptions(unsafe); err == nil {
			t.Fatalf("unsafe verification options accepted: %v", unsafe)
		}
	}

	for _, mode := range []jobMode{modeReadiness, modeVerifier} {
		receipt := ""
		if mode == modeVerifier {
			receipt = testReceipt
		}
		job := renderTestJob(t, mode, receipt)
		container := job.Spec.Template.Spec.Containers[0]
		encoded, marshalErr := json.Marshal(job)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		wantRole := "isolated-readiness"
		wantCommand := "/usr/local/bin/mss-shop-legacy-readiness"
		if mode == modeVerifier {
			wantRole = "legacy-verifier"
			wantCommand = "/usr/local/bin/mss-shop-legacy-verifier"
		}
		if job.Namespace != testNamespace || job.Spec.Template.Labels["r1shop.io/network-role"] != wantRole ||
			container.Image != "ghcr.io/shop-r1/mss-shop-legacy-importer:"+testRevision+"@"+testDigest ||
			job.Annotations[imageDigestKey] != testDigest || len(container.Command) != 1 ||
			container.Command[0] != wantCommand || strings.Contains(string(encoded), "mss-shop-legacy-source-auth") ||
			strings.Contains(string(encoded), "legacy-source") {
			t.Fatalf("%s escaped its fixed image/network/source boundary", mode)
		}
		if mode == modeVerifier && (job.Annotations[receiptKey] != testReceipt ||
			!strings.Contains(string(encoded), receiptConfigMapName)) {
			t.Fatal("verifier lacks its exact receipt and immutable ConfigMap binding")
		}
	}
}

func TestReceiptEvidenceRequiresTheCompiledInventoryAndExactImmutableConfigMap(t *testing.T) {
	t.Parallel()
	receiptBytes, receiptSHA := validStageReceipt(t)
	if err := validateReceiptEvidence(receiptBytes, receiptSHA); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}
	desired, err := desiredReceiptConfigMap(testRevision, receiptSHA, receiptBytes)
	if err != nil {
		t.Fatalf("build receipt ConfigMap: %v", err)
	}
	if desired.Name != receiptConfigMapName || desired.Namespace != testNamespace ||
		desired.Immutable == nil || !*desired.Immutable || len(desired.Data) != 1 ||
		desired.Data["receipt.json"] != string(receiptBytes) || desired.BinaryData != nil ||
		desired.Annotations[receiptKey] != receiptSHA || desired.Annotations[revisionKey] != testRevision {
		t.Fatalf("unsafe receipt ConfigMap: %#v", desired)
	}

	var receipt legacyreceipt.Receipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Tables[0], receipt.Tables[1] = receipt.Tables[1], receipt.Tables[0]
	unsafe := encodeStageReceipt(t, receipt)
	if err := validateReceiptEvidence(unsafe, receipt.SHA256); err == nil {
		t.Fatal("receipt with a drifted compiled inventory was accepted")
	}
	receiptBytes, receiptSHA = validStageReceipt(t)
	if err := validateReceiptEvidence(receiptBytes, strings.Repeat("0", 64)); err == nil {
		t.Fatal("zero receipt binding was accepted")
	}
}

func TestReadinessRequiresOwnedReadyPostgresAndRedisWithoutReadingSource(t *testing.T) {
	desired := renderTestJob(t, modeReadiness, "")
	for _, test := range []struct {
		name   string
		mutate func([]runtime.Object) []runtime.Object
	}{
		{name: "valid"},
		{name: "redis-missing", mutate: func(objects []runtime.Object) []runtime.Object {
			return withoutNamedObject(objects, "mss-shop-redis")
		}},
		{name: "redis-not-ready", mutate: func(objects []runtime.Object) []runtime.Object {
			findNamedStatefulSet(t, objects, "mss-shop-redis").Status.ReadyReplicas = 0
			return objects
		}},
		{name: "redis-pvc-pending", mutate: func(objects []runtime.Object) []runtime.Object {
			findNamedPVC(t, objects, "mss-shop-redis-data").Status.Phase = corev1.ClaimPending
			return objects
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := verificationPrerequisites(t, modeReadiness)
			if test.mutate != nil {
				objects = test.mutate(objects)
			}
			harness := newVerificationHarness(t, objects)
			result, err := converge(context.Background(), harness.client, desired, modeReadiness, "", false)
			if test.name == "valid" {
				if err != nil || !result.dryRun || harness.jobDryRuns != 1 || harness.jobCreates != 0 {
					t.Fatalf("valid readiness dry-run failed: result=%+v err=%v", result, err)
				}
			} else if err == nil || harness.jobCreates != 0 {
				t.Fatalf("unsafe readiness target accepted: result=%+v", result)
			}
			for _, action := range harness.client.Actions() {
				if action.GetResource().Resource == "secrets" {
					if get, ok := action.(ktesting.GetAction); ok && get.GetName() == "mss-shop-legacy-source-auth" {
						t.Fatal("readiness read the legacy source Secret")
					}
				}
			}
			assertVerificationMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestVerifierCreateOrderIsConfigMapThenNamespaceAndExactConfigMapThenJob(t *testing.T) {
	receiptBytes, receiptSHA := validStageReceipt(t)
	desired := renderTestJob(t, modeVerifier, receiptSHA)
	harness := newVerificationHarness(t, verificationPrerequisites(t, modeVerifier))
	result, err := converge(context.Background(), harness.client, desired, modeVerifier, receiptSHA, true, receiptBytes)
	if err != nil {
		t.Fatalf("create fixed verifier delivery: %v", err)
	}
	if !result.created || !result.dryRun || result.exactRetry || harness.configMapDryRuns != 1 ||
		harness.configMapCreates != 1 || harness.jobDryRuns != 1 || harness.jobCreates != 1 {
		t.Fatalf("unexpected create-only result: %+v harness=%+v", result, harness)
	}
	assertFirstActionIsNamespaceGet(t, harness.client.Actions())
	assertReceiptRecheckBeforeJobCreate(t, harness.client.Actions())
	assertVerificationMutationBoundary(t, harness.client.Actions())
	for _, action := range harness.client.Actions() {
		if action.GetResource().Resource == "secrets" {
			if get, ok := action.(ktesting.GetAction); ok && get.GetName() == "mss-shop-legacy-source-auth" {
				t.Fatal("verifier read the legacy source Secret")
			}
		}
	}
}

func TestVerifierExistingJobRequiresByteExactReceiptConfigMap(t *testing.T) {
	receiptBytes, receiptSHA := validStageReceipt(t)
	desired := renderTestJob(t, modeVerifier, receiptSHA)
	desiredReceipt, err := desiredReceiptConfigMap(testRevision, receiptSHA, receiptBytes)
	if err != nil {
		t.Fatal(err)
	}
	existingJob := testPersistedJob(desired)
	for _, test := range []struct {
		name      string
		configMap *corev1.ConfigMap
		wantOK    bool
	}{
		{name: "exact", configMap: testServerReceiptConfigMap(desiredReceipt, true), wantOK: true},
		{name: "missing"},
		{name: "different-bytes", configMap: func() *corev1.ConfigMap {
			value := testServerReceiptConfigMap(desiredReceipt, true)
			value.Data["receipt.json"] += "\n"
			return value
		}()},
		{name: "different-revision", configMap: func() *corev1.ConfigMap {
			value := testServerReceiptConfigMap(desiredReceipt, true)
			value.Annotations[revisionKey] = strings.Repeat("f", 40)
			return value
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := verificationPrerequisites(t, modeVerifier)
			objects = append(objects, existingJob.DeepCopy())
			if test.configMap != nil {
				objects = append(objects, test.configMap.DeepCopy())
			}
			harness := newVerificationHarness(t, objects)
			result, err := converge(context.Background(), harness.client, desired, modeVerifier, receiptSHA, true, receiptBytes)
			if test.wantOK {
				if err != nil || !result.exactRetry || result.created || harness.configMapCreates != 0 || harness.jobCreates != 0 {
					t.Fatalf("exact retry rejected or mutated state: result=%+v err=%v", result, err)
				}
			} else if err == nil || harness.configMapDryRuns != 0 || harness.configMapCreates != 0 ||
				harness.jobDryRuns != 0 || harness.jobCreates != 0 {
				t.Fatalf("incomplete verifier identity accepted or repaired implicitly: result=%+v", result)
			}
			assertVerificationMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestVerifierDryRunAlreadyExistsRaceCannotSucceedWithoutReceiptConfigMap(t *testing.T) {
	receiptBytes, receiptSHA := validStageReceipt(t)
	desired := renderTestJob(t, modeVerifier, receiptSHA)
	harness := newVerificationHarness(t, verificationPrerequisites(t, modeVerifier))
	harness.jobDryRunRace = desired
	result, err := converge(context.Background(), harness.client, desired, modeVerifier, receiptSHA, true, receiptBytes)
	if err == nil || result.created || result.exactRetry || harness.configMapCreates != 0 || harness.jobCreates != 0 {
		t.Fatalf("orphan verifier Job race was reported as success: result=%+v err=%v", result, err)
	}
	assertVerificationMutationBoundary(t, harness.client.Actions())
}

func TestVerifierReceiptConfigMapAlreadyExistsRaceRequiresByteEquality(t *testing.T) {
	receiptBytes, receiptSHA := validStageReceipt(t)
	desired := renderTestJob(t, modeVerifier, receiptSHA)
	for _, test := range []struct {
		name    string
		mutate  func(*corev1.ConfigMap)
		wantErr bool
	}{
		{name: "exact"},
		{name: "different-content", mutate: func(configMap *corev1.ConfigMap) {
			configMap.Data["receipt.json"] += "\n"
		}, wantErr: true},
		{name: "foreign-binding", mutate: func(configMap *corev1.ConfigMap) {
			configMap.Annotations[operatorBindingKey] = "foreign"
		}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newVerificationHarness(t, verificationPrerequisites(t, modeVerifier))
			harness.configMapRace = true
			harness.configMapRaceMutate = test.mutate
			result, err := converge(context.Background(), harness.client, desired, modeVerifier, receiptSHA, true, receiptBytes)
			if test.wantErr {
				if err == nil || result.created || harness.jobCreates != 0 {
					t.Fatalf("incompatible receipt race was accepted: result=%+v err=%v", result, err)
				}
			} else if err != nil || !result.created || harness.jobCreates != 1 {
				t.Fatalf("byte-exact receipt race was rejected: result=%+v err=%v", result, err)
			}
			if harness.configMapCreates != 1 {
				t.Fatalf("receipt race create count = %d, want 1", harness.configMapCreates)
			}
			assertVerificationMutationBoundary(t, harness.client.Actions())
		})
	}
}

func TestVerifierRejectsGlobalReceiptCollisionBeforeAnyWrite(t *testing.T) {
	receiptBytes, receiptSHA := validStageReceipt(t)
	desired := renderTestJob(t, modeVerifier, receiptSHA)
	desiredReceipt, err := desiredReceiptConfigMap(testRevision, receiptSHA, receiptBytes)
	if err != nil {
		t.Fatal(err)
	}
	foreign := testServerReceiptConfigMap(desiredReceipt, true)
	foreign.Namespace = "foreign-dev"
	foreign.ResourceVersion = "foreign"
	objects := append(verificationPrerequisites(t, modeVerifier), foreign)
	harness := newVerificationHarness(t, objects)
	if _, err := converge(context.Background(), harness.client, desired, modeVerifier, receiptSHA, true, receiptBytes); err == nil {
		t.Fatal("global receipt identity collision was accepted")
	}
	if harness.configMapDryRuns != 0 || harness.configMapCreates != 0 || harness.jobDryRuns != 0 || harness.jobCreates != 0 {
		t.Fatal("global receipt collision reached a create action")
	}
	assertFirstActionIsNamespaceGet(t, harness.client.Actions())
	assertVerificationMutationBoundary(t, harness.client.Actions())
}

func TestVerificationNamespaceFailureHasOneGetAndZeroGlobalReadsOrWrites(t *testing.T) {
	receiptBytes, receiptSHA := validStageReceipt(t)
	for _, mode := range []jobMode{modeReadiness, modeVerifier} {
		t.Run(string(mode), func(t *testing.T) {
			objects := verificationPrerequisites(t, mode)
			findTestNamespace(t, objects).Annotations[testBindingKey] = "foreign"
			harness := newVerificationHarness(t, objects)
			receipt := ""
			var evidence [][]byte
			if mode == modeVerifier {
				receipt = receiptSHA
				evidence = append(evidence, receiptBytes)
			}
			desired := renderTestJob(t, mode, receipt)
			if _, err := converge(context.Background(), harness.client, desired, mode, receipt, true, evidence...); err == nil {
				t.Fatal("foreign Namespace passed verification stage")
			}
			if len(harness.client.Actions()) != 1 {
				t.Fatalf("foreign Namespace actions = %#v", harness.client.Actions())
			}
			assertFirstActionIsNamespaceGet(t, harness.client.Actions())
			if harness.configMapDryRuns != 0 || harness.configMapCreates != 0 || harness.jobDryRuns != 0 || harness.jobCreates != 0 {
				t.Fatal("foreign Namespace reached create")
			}
		})
	}
}

type verificationHarness struct {
	client              *fake.Clientset
	configMapDryRuns    int
	configMapCreates    int
	jobDryRuns          int
	jobCreates          int
	jobDryRunRace       *batchv1.Job
	configMapRace       bool
	configMapRaceMutate func(*corev1.ConfigMap)
}

func newVerificationHarness(t *testing.T, objects []runtime.Object) *verificationHarness {
	t.Helper()
	client := fake.NewSimpleClientset(objects...)
	harness := &verificationHarness{client: client}
	client.PrependReactor("create", "configmaps", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(interface {
			ktesting.CreateAction
			GetCreateOptions() metav1.CreateOptions
		})
		configMap := create.GetObject().(*corev1.ConfigMap)
		if len(create.GetCreateOptions().DryRun) != 0 {
			harness.configMapDryRuns++
			return true, testServerReceiptConfigMap(configMap, false), nil
		}
		harness.configMapCreates++
		stored := testServerReceiptConfigMap(configMap, true)
		if harness.configMapRace {
			if harness.configMapRaceMutate != nil {
				harness.configMapRaceMutate(stored)
			}
			if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("configmaps"), stored, configMap.Namespace); err != nil {
				t.Fatalf("persist concurrent receipt ConfigMap: %v", err)
			}
			return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "configmaps"}, configMap.Name)
		}
		if err := client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("configmaps"), stored, configMap.Namespace); err != nil {
			t.Fatalf("persist receipt ConfigMap: %v", err)
		}
		return true, stored.DeepCopy(), nil
	})
	client.PrependReactor("create", "jobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(interface {
			ktesting.CreateAction
			GetCreateOptions() metav1.CreateOptions
		})
		job := create.GetObject().(*batchv1.Job)
		if len(create.GetCreateOptions().DryRun) != 0 {
			harness.jobDryRuns++
			if harness.jobDryRunRace != nil {
				concurrent := testPersistedJob(harness.jobDryRunRace)
				if err := client.Tracker().Create(batchv1.SchemeGroupVersion.WithResource("jobs"), concurrent, job.Namespace); err != nil {
					t.Fatalf("persist dry-run concurrent Job: %v", err)
				}
				return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Group: "batch", Resource: "jobs"}, job.Name)
			}
			return true, testServerJob(job, false), nil
		}
		harness.jobCreates++
		stored := testServerJob(job, true)
		if err := client.Tracker().Create(batchv1.SchemeGroupVersion.WithResource("jobs"), stored, job.Namespace); err != nil {
			t.Fatalf("persist verifier Job: %v", err)
		}
		return true, stored.DeepCopy(), nil
	})
	return harness
}

func verificationPrerequisites(t *testing.T, mode jobMode) []runtime.Object {
	t.Helper()
	objects := testJobPrerequisites(modeImporter, "")
	filtered := make([]runtime.Object, 0, len(objects)+4)
	for _, object := range objects {
		if secret, ok := object.(*corev1.Secret); ok && secret.Name == "mss-shop-legacy-source-auth" {
			continue
		}
		filtered = append(filtered, object)
	}
	if mode == modeReadiness {
		filtered = append(filtered,
			testCredentialSecret("mss-shop-redis-auth", corev1.SecretTypeOpaque, map[string][]byte{
				"password": []byte(strings.Repeat("r", 43)),
			}),
			testCredentialSecret("mss-shop-redis-tls", corev1.SecretTypeTLS, map[string][]byte{
				"ca.crt": []byte("test-redis-ca"), corev1.TLSCertKey: []byte("test-redis-cert"), corev1.TLSPrivateKeyKey: []byte("test-redis-key"),
			}),
			testRedisStatefulSetFixture(),
			testRedisPVCFixture(),
		)
	}
	return filtered
}

func testRedisStatefulSetFixture() *appsv1.StatefulSet {
	one := int32(1)
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mss-shop-redis", Namespace: testNamespace, UID: types.UID("redis-sts-uid"),
			ResourceVersion: "40", Generation: 3,
			Labels: testInfrastructureLabels("mss-shop-redis", "cache"),
			Annotations: map[string]string{
				testBindingKey:     testNamespace + ":StatefulSet:mss-shop-redis",
				testInfrastructure: testContract,
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &one, ServiceName: "mss-shop-redis",
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name": "mss-shop-redis", "app.kubernetes.io/instance": testNamespace,
			}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "redis", Image: redisImage}},
				Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "mss-shop-redis-data"},
				}}},
			}},
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 3, Replicas: 1, ReadyReplicas: 1, CurrentReplicas: 1,
			UpdatedReplicas: 1, AvailableReplicas: 1, CurrentRevision: "redis-revision", UpdateRevision: "redis-revision",
		},
	}
}

func testRedisPVCFixture() *corev1.PersistentVolumeClaim {
	storageClass := "local"
	volumeMode := corev1.PersistentVolumeFilesystem
	quantity := resource.MustParse("2Gi")
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mss-shop-redis-data", Namespace: testNamespace, UID: types.UID("redis-pvc-uid"), ResourceVersion: "50",
			Labels: testInfrastructureLabels("mss-shop-redis", "cache"),
			Annotations: map[string]string{
				testBindingKey:     testNamespace + ":PersistentVolumeClaim:mss-shop-redis-data",
				testInfrastructure: testContract,
				"volume.kubernetes.io/storage-provisioner": "openebs.io/local",
				"volume.kubernetes.io/selected-node":       "dev-node",
				"pv.kubernetes.io/bind-completed":          "yes",
				"pv.kubernetes.io/bound-by-controller":     "yes",
			},
			Finalizers: []string{"kubernetes.io/pvc-protection"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, StorageClassName: &storageClass,
			VolumeMode: &volumeMode, VolumeName: "pv-mss-shop-redis-data",
			Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: quantity}},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound, AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{corev1.ResourceStorage: quantity},
		},
	}
}

func validStageReceipt(t *testing.T) ([]byte, string) {
	t.Helper()
	receipt := legacyreceipt.Receipt{
		Version: legacyreceipt.Version, TargetDatabase: "mss_shop_dev",
		ManifestSHA256: legacyManifestSHA256, SchemaSHA256: strings.Repeat("4", 64),
		Tables: make([]legacyreceipt.Table, 0, len(importedTableNames)),
	}
	for _, name := range importedTableNames {
		table := legacyreceipt.Table{
			Name: name, Mode: "copied", SourceRows: 3, TargetRows: 3,
			SourceSHA256: strings.Repeat("1", 64), TargetSHA256: strings.Repeat("1", 64),
		}
		if name == "orders" || name == "order_goods" {
			table.Mode = "structure-only"
			table.SourceRows = 7
			table.TargetRows = 0
			table.SourceSHA256 = strings.Repeat("2", 64)
			table.TargetSHA256 = strings.Repeat("3", 64)
		}
		receipt.Tables = append(receipt.Tables, table)
	}
	encoded := encodeStageReceipt(t, receipt)
	var finalized legacyreceipt.Receipt
	if err := json.Unmarshal(encoded, &finalized); err != nil {
		t.Fatal(err)
	}
	return encoded, finalized.SHA256
}

func encodeStageReceipt(t *testing.T, receipt legacyreceipt.Receipt) []byte {
	t.Helper()
	payload := struct {
		Version        string                `json:"version"`
		TargetDatabase string                `json:"targetDatabase"`
		ManifestSHA256 string                `json:"manifestSHA256"`
		SchemaSHA256   string                `json:"schemaSHA256"`
		Tables         []legacyreceipt.Table `json:"tables"`
	}{receipt.Version, receipt.TargetDatabase, receipt.ManifestSHA256, receipt.SchemaSHA256, receipt.Tables}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	receipt.SHA256 = hex.EncodeToString(digest[:])
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func testServerReceiptConfigMap(desired *corev1.ConfigMap, persisted bool) *corev1.ConfigMap {
	configMap := desired.DeepCopy()
	configMap.UID = types.UID("receipt-configmap-uid")
	if persisted {
		configMap.ResourceVersion = "200"
		configMap.CreationTimestamp = metav1.Now()
	}
	return configMap
}

func withoutNamedObject(objects []runtime.Object, name string) []runtime.Object {
	result := make([]runtime.Object, 0, len(objects))
	for _, object := range objects {
		if meta, ok := object.(metav1.Object); ok && meta.GetName() == name {
			continue
		}
		result = append(result, object)
	}
	return result
}

func findNamedStatefulSet(t *testing.T, objects []runtime.Object, name string) *appsv1.StatefulSet {
	t.Helper()
	for _, object := range objects {
		if value, ok := object.(*appsv1.StatefulSet); ok && value.Name == name {
			return value
		}
	}
	t.Fatalf("StatefulSet fixture %q not found", name)
	return nil
}

func findNamedPVC(t *testing.T, objects []runtime.Object, name string) *corev1.PersistentVolumeClaim {
	t.Helper()
	for _, object := range objects {
		if value, ok := object.(*corev1.PersistentVolumeClaim); ok && value.Name == name {
			return value
		}
	}
	t.Fatalf("PVC fixture %q not found", name)
	return nil
}

func assertFirstActionIsNamespaceGet(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	if len(actions) == 0 {
		t.Fatal("no Namespace gate action observed")
	}
	get, ok := actions[0].(ktesting.GetAction)
	if !ok || actions[0].GetVerb() != "get" || actions[0].GetResource().Resource != "namespaces" ||
		actions[0].GetNamespace() != "" || get.GetName() != testNamespace {
		t.Fatalf("first cluster action is not the exact Namespace GET: %#v", actions[0])
	}
}

func assertReceiptRecheckBeforeJobCreate(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	realConfigMapCreate := -1
	realJobCreate := -1
	for index, action := range actions {
		create, ok := action.(interface {
			ktesting.CreateAction
			GetCreateOptions() metav1.CreateOptions
		})
		if !ok || len(create.GetCreateOptions().DryRun) != 0 {
			continue
		}
		if action.GetResource().Resource == "configmaps" {
			realConfigMapCreate = index
		}
		if action.GetResource().Resource == "jobs" {
			realJobCreate = index
		}
	}
	if realConfigMapCreate < 0 || realJobCreate <= realConfigMapCreate {
		t.Fatalf("persistent create order lacks ConfigMap before Job: cm=%d job=%d", realConfigMapCreate, realJobCreate)
	}
	namespaceAfterReceipt := false
	exactReceiptAfterNamespace := false
	for index := realConfigMapCreate + 1; index < realJobCreate; index++ {
		action := actions[index]
		get, ok := action.(ktesting.GetAction)
		if !ok {
			continue
		}
		if action.GetResource().Resource == "namespaces" && get.GetName() == testNamespace {
			namespaceAfterReceipt = true
			continue
		}
		if namespaceAfterReceipt && action.GetResource().Resource == "configmaps" &&
			action.GetNamespace() == testNamespace && get.GetName() == receiptConfigMapName {
			exactReceiptAfterNamespace = true
		}
	}
	if !namespaceAfterReceipt || !exactReceiptAfterNamespace {
		t.Fatal("receipt ConfigMap was not followed by Namespace and byte-exact ConfigMap GETs before Job Create")
	}
}

func assertVerificationMutationBoundary(t *testing.T, actions []ktesting.Action) {
	t.Helper()
	for _, action := range actions {
		if action.GetNamespace() == "r1shop-dev" || action.GetNamespace() == "r1shop-prod" {
			switch action.GetVerb() {
			case "create", "update", "patch", "delete", "delete-collection", "deletecollection":
				t.Fatalf("write escaped protected namespace: %#v", action)
			}
		}
		switch action.GetVerb() {
		case "get", "list":
		case "create":
			create := action.(interface {
				ktesting.CreateAction
				GetCreateOptions() metav1.CreateOptions
			})
			if len(create.GetCreateOptions().DryRun) != 0 {
				continue
			}
			if action.GetNamespace() != testNamespace ||
				(action.GetResource().Resource != "jobs" && action.GetResource().Resource != "configmaps") {
				t.Fatalf("persistent create escaped fixed evidence/Job boundary: %#v", action)
			}
		default:
			t.Fatalf("forbidden Kubernetes verb %q observed", action.GetVerb())
		}
	}
}
